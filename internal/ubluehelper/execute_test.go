package ubluehelper

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

const rollbackRefusal = "error: Target image has the same fs-verity digest as the existing Some(Rollback) deployment"

// fakeRun records every command and answers each from results in order.
type fakeRun struct {
	calls   [][]string
	results []fakeResult
}

type fakeResult struct {
	stderr string
	err    error
}

func (f *fakeRun) run(_ context.Context, stderr io.Writer, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(f.calls) > len(f.results) {
		return nil
	}
	r := f.results[len(f.calls)-1]
	if stderr != nil {
		_, _ = io.WriteString(stderr, r.stderr)
	}
	return r.err
}

func staticRef(ref string, err error) RollbackRefLookup {
	return func(context.Context) (string, error) { return ref, err }
}

func TestSwitchImageOutcomes(t *testing.T) {
	const target = "ghcr.io/ublue-os/bluefin:stable"
	args := []string{"switch", "--enforce-container-sigpolicy", target}
	exitErr := errors.New("exit status 1")

	tests := []struct {
		name      string
		results   []fakeResult
		ref       RollbackRefLookup
		wantMsg   string
		wantErr   string
		wantCalls [][]string
	}{
		{
			name:      "switch succeeds",
			ref:       staticRef("", errors.New("must not be consulted")),
			wantMsg:   "switched to " + target + " — restart to apply",
			wantCalls: [][]string{append([]string{"bootc"}, args...)},
		},
		{
			name:      "refusal for rollback target falls back to bootc rollback",
			results:   []fakeResult{{stderr: rollbackRefusal, err: exitErr}, {}},
			ref:       staticRef(target, nil),
			wantMsg:   "switched to " + target + " (the previous deployment) — restart to apply",
			wantCalls: [][]string{append([]string{"bootc"}, args...), {"bootc", "rollback"}},
		},
		{
			name:      "fallback rollback failure reports both errors",
			results:   []fakeResult{{stderr: rollbackRefusal, err: exitErr}, {err: errors.New("rollback boom")}},
			ref:       staticRef(target, nil),
			wantErr:   "bootc switch failed: exit status 1; rollback to " + target + " failed: rollback boom",
			wantCalls: [][]string{append([]string{"bootc"}, args...), {"bootc", "rollback"}},
		},
		{
			name:      "refusal for a different rollback image does not roll back",
			results:   []fakeResult{{stderr: rollbackRefusal, err: exitErr}},
			ref:       staticRef("ghcr.io/ublue-os/bluefin:testing", nil),
			wantErr:   "bootc switch failed: exit status 1",
			wantCalls: [][]string{append([]string{"bootc"}, args...)},
		},
		{
			name:      "refusal with unreadable status does not roll back",
			results:   []fakeResult{{stderr: rollbackRefusal, err: exitErr}},
			ref:       staticRef(target, errors.New("status failed")),
			wantErr:   "bootc switch failed: exit status 1",
			wantCalls: [][]string{append([]string{"bootc"}, args...)},
		},
		{
			name:      "unrelated failure does not consult rollback",
			results:   []fakeResult{{stderr: "error: connection refused", err: exitErr}},
			ref:       staticRef(target, nil),
			wantErr:   "bootc switch failed: exit status 1",
			wantCalls: [][]string{append([]string{"bootc"}, args...)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRun{results: tc.results}
			msg, err := SwitchImage(context.Background(), f.run, tc.ref, args, "bootc switch failed")
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg != tc.wantMsg {
				t.Errorf("msg = %q, want %q", msg, tc.wantMsg)
			}
			if !reflect.DeepEqual(f.calls, tc.wantCalls) {
				t.Errorf("calls = %v, want %v", f.calls, tc.wantCalls)
			}
		})
	}
}

func TestApplyDevGroupsSkipsFailedGroupsAndCountsTheRest(t *testing.T) {
	groups := DevGroups()
	results := make([]fakeResult, len(groups))
	results[0] = fakeResult{err: errors.New("group does not exist")}
	f := &fakeRun{results: results}
	var stdout, stderr bytes.Buffer

	applied, err := ApplyDevGroups(context.Background(), f.run, func(string) error { return nil },
		CommandDXEnable, "alice", false, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied != len(groups)-1 {
		t.Errorf("applied = %d, want %d", applied, len(groups)-1)
	}
	if len(f.calls) != len(groups) {
		t.Errorf("ran %d commands, want %d", len(f.calls), len(groups))
	}
	if got := SkippedGroups(stderr.String()); !reflect.DeepEqual(got, []string{groups[0]}) {
		t.Errorf("SkippedGroups(stderr) = %v, want [%s]", got, groups[0])
	}
}

func TestApplyDevGroupsSkipsGroupsEnsureCannotPrepare(t *testing.T) {
	groups := DevGroups()
	f := &fakeRun{}
	var stdout, stderr bytes.Buffer
	ensure := func(group string) error {
		if group == groups[1] {
			return errors.New("not in /usr/lib/group")
		}
		return nil
	}

	applied, err := ApplyDevGroups(context.Background(), f.run, ensure, CommandDXEnable, "alice", false, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applied != len(groups)-1 || len(f.calls) != len(groups)-1 {
		t.Errorf("applied = %d, calls = %d, want %d each", applied, len(f.calls), len(groups)-1)
	}
	if got := SkippedGroups(stderr.String()); !reflect.DeepEqual(got, []string{groups[1]}) {
		t.Errorf("SkippedGroups(stderr) = %v, want [%s]", got, groups[1])
	}
}

func TestApplyDevGroupsDisableNeverPreparesGroups(t *testing.T) {
	f := &fakeRun{}
	ensure := func(group string) error {
		t.Errorf("ensure(%q) called for dx-disable", group)
		return nil
	}
	applied, err := ApplyDevGroups(context.Background(), f.run, ensure, CommandDXDisable, "alice", false, io.Discard, io.Discard)
	if err != nil || applied != len(DevGroups()) {
		t.Fatalf("applied = %d, err = %v; want %d, nil", applied, err, len(DevGroups()))
	}
	for _, call := range f.calls {
		if call[0] != "gpasswd" {
			t.Errorf("dx-disable ran %v, want gpasswd", call)
		}
	}
}

func TestApplyDevGroupsFailsWhenNothingApplied(t *testing.T) {
	groups := DevGroups()
	results := make([]fakeResult, len(groups))
	for i := range results {
		results[i] = fakeResult{err: errors.New("nope")}
	}
	f := &fakeRun{results: results}

	applied, err := ApplyDevGroups(context.Background(), f.run, func(string) error { return nil },
		CommandDXEnable, "alice", false, io.Discard, io.Discard)
	if !errors.Is(err, ErrNoDevGroupsChanged) || applied != 0 {
		t.Fatalf("applied = %d, err = %v; want 0, ErrNoDevGroupsChanged", applied, err)
	}
}

func TestApplyDevGroupsDryRunRunsNothing(t *testing.T) {
	f := &fakeRun{}
	var stdout bytes.Buffer

	applied, err := ApplyDevGroups(context.Background(), f.run, func(string) error { return nil },
		CommandDXEnable, "alice", true, &stdout, io.Discard)
	if err != nil || applied != len(DevGroups()) {
		t.Fatalf("applied = %d, err = %v; want %d, nil", applied, err, len(DevGroups()))
	}
	if len(f.calls) != 0 {
		t.Errorf("dry run executed %v", f.calls)
	}
	if got := strings.Count(stdout.String(), "[DRY-RUN] would execute: usermod"); got != len(DevGroups()) {
		t.Errorf("dry-run lines = %d, want %d:\n%s", got, len(DevGroups()), stdout.String())
	}
}

func TestApplyDevGroupsRejectsNonDevGroupCommands(t *testing.T) {
	f := &fakeRun{}
	if _, err := ApplyDevGroups(context.Background(), f.run, func(string) error { return nil },
		CommandRestart, "alice", false, io.Discard, io.Discard); err == nil {
		t.Fatal("ApplyDevGroups accepted restart")
	}
	if len(f.calls) != 0 {
		t.Errorf("ran %v for an unsupported command", f.calls)
	}
}
