package ubluehelper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
)

// Runner executes one privileged command. When stderr is non-nil it receives
// a copy of the command's standard error, so the caller can inspect it.
// cmd/chairlift-helper supplies the real implementation; tests supply fakes.
type Runner func(ctx context.Context, stderr io.Writer, name string, args ...string) error

// RollbackRefLookup returns the image reference of the host's rollback
// deployment, or "" when there is none.
type RollbackRefLookup func(ctx context.Context) (string, error)

// ErrNoDevGroupsChanged is returned by ApplyDevGroups when not a single
// developer group could be changed.
var ErrNoDevGroupsChanged = errors.New("no developer groups could be changed")

// SwitchImage runs a derived `bootc switch` argv and returns the line to
// report on success. Switching back to the image the host just left is a
// switch to the rollback deployment, which a composefs `bootc switch` refuses
// ("Target image has the same fs-verity digest as the existing
// Some(Rollback) deployment"); making that deployment the next boot with the
// fixed rollback argv is the switch the person asked for. failure prefixes
// the returned error.
func SwitchImage(ctx context.Context, run Runner, rollbackRef RollbackRefLookup, args []string, failure string) (string, error) {
	target := args[len(args)-1]
	var captured bytes.Buffer
	err := run(ctx, &captured, "bootc", args...)
	if err == nil {
		return fmt.Sprintf("switched to %s — restart to apply", target), nil
	}
	if IsSameRollbackRefusal(captured.String()) {
		if ref, refErr := rollbackRef(ctx); refErr == nil && ref == target {
			if rerr := run(ctx, nil, "bootc", RollbackArgs()...); rerr != nil {
				return "", fmt.Errorf("%s: %v; rollback to %s failed: %v", failure, err, target, rerr)
			}
			return fmt.Sprintf("switched to %s (the previous deployment) — restart to apply", target), nil
		}
	}
	return "", fmt.Errorf("%s: %v", failure, err)
}

// ApplyDevGroups adds username to, or removes it from, every developer group
// and returns how many groups were changed. A group that does not exist on
// this image is reported on stderr and skipped rather than failing the whole
// operation: the group set is shared across Bluefin, Bluefin LTS, and Dakota,
// which do not all ship the same ones. For dx-enable, ensure prepares each
// group first (copying an image-only group into /etc/group). Dry-run output
// goes to stdout. ErrNoDevGroupsChanged is returned when nothing applied.
func ApplyDevGroups(ctx context.Context, run Runner, ensure func(group string) error, command, username string, dryRun bool, stdout, stderr io.Writer) (int, error) {
	applied := 0
	for _, group := range DevGroups() {
		name, args, ok := GroupArgs(command, username, group)
		if !ok {
			return applied, fmt.Errorf("unsupported group operation for command %q", command)
		}

		if command == CommandDXEnable {
			if err := ensure(group); err != nil {
				fmt.Fprintln(stderr, SkippedGroupLine(group, err))
				continue
			}
		}

		if dryRun {
			fmt.Fprintf(stdout, "[DRY-RUN] would execute: %s %v\n", name, args)
			applied++
			continue
		}

		if err := run(ctx, nil, name, args...); err != nil {
			fmt.Fprintln(stderr, SkippedGroupLine(group, err))
			continue
		}
		applied++
	}

	if applied == 0 {
		return 0, ErrNoDevGroupsChanged
	}
	return applied, nil
}
