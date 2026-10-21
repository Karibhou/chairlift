package devtools

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/projectbluefin/chairlift/internal/dryrun"
)

const dockerShowCall = "systemctl --user show docker.service --property=LoadState,ActiveState"

func dockerInstallCalls() []string {
	return []string{
		"brew install docker",
		"brew install docker-engine",
		"brew install rootlesskit",
		"brew install slirp4netns",
		"brew install docker-compose",
		"brew install lazydocker",
		"brew install dive",
	}
}

func fakeRootlessDocker(t *testing.T, report string) (string, string) {
	t.Helper()
	dir := fakeSystemctl(t, report)
	calls := filepath.Join(dir, "calls")
	t.Setenv("DOCKER_TEST_CALLS", calls)
	t.Setenv("DOCKER_TEST_STATE", dir)
	t.Setenv("DOCKER_TEST_REPORT", report)
	t.Setenv("DOCKER_TEST_FAIL", "")
	t.Setenv("DOCKER_TEST_SYSTEM_SOCKET", "")
	t.Setenv("DOCKER_TEST_STATUS_ERROR", "")
	t.Setenv("DOCKER_TEST_SETUP_ERROR", "")
	t.Setenv("HOME", filepath.Join(dir, "home"))
	for name, body := range map[string]string{
		"brew": `printf 'brew %s\n' "$*" >> "$DOCKER_TEST_CALLS"
[ "$DOCKER_TEST_FAIL" = "$2" ] && exit 11
exit 0
`,
		"dockerd-rootless-setuptool.sh": `printf 'setup %s\n' "$*" >> "$DOCKER_TEST_CALLS"
[ -n "$DOCKER_TEST_SETUP_ERROR" ] && { printf '%s\n' "$DOCKER_TEST_SETUP_ERROR" >&2; exit 12; }
[ "$DOCKER_TEST_FAIL" = setup ] && exit 12
# Match the upstream installer's refusal when a rootful socket is writable.
if [ -w "$DOCKER_TEST_SYSTEM_SOCKET" ] && [ "$*" != 'install --force' ]; then
  echo 'Aborting because rootful Docker is running and accessible' >&2
  exit 16
fi
exit 0
`,
		"docker": `printf 'docker %s\n' "$*" >> "$DOCKER_TEST_CALLS"
exit 0
`,
		"systemctl": `printf 'systemctl %s\n' "$*" >> "$DOCKER_TEST_CALLS"
case "$*" in
'--user show docker.service --property=LoadState,ActiveState')
  [ -n "$DOCKER_TEST_STATUS_ERROR" ] && { printf '%s\n' "$DOCKER_TEST_STATUS_ERROR" >&2; exit 14; }
  [ "$DOCKER_TEST_FAIL" = status ] && exit 14
  if [ "$DOCKER_TEST_FAIL" = post-status ] && { [ -f "$DOCKER_TEST_STATE/started" ] || [ -f "$DOCKER_TEST_STATE/stopped" ]; }; then
    exit 14
  fi
  if [ -f "$DOCKER_TEST_STATE/stopped" ]; then
    printf 'LoadState=loaded\nActiveState=inactive\n'
  elif [ -f "$DOCKER_TEST_STATE/started" ]; then
    printf 'LoadState=loaded\nActiveState=active\n'
  else
    printf '%s' "$DOCKER_TEST_REPORT"
  fi
  ;;
'--user enable --now docker.service')
  [ "$DOCKER_TEST_FAIL" = start ] && exit 13
  : > "$DOCKER_TEST_STATE/started"
  ;;
'--user disable --now docker.service')
  [ "$DOCKER_TEST_FAIL" = disable ] && exit 15
  [ "$DOCKER_TEST_FAIL" = disable-noop ] && exit 0
  : > "$DOCKER_TEST_STATE/stopped"
  ;;
*) exit 99 ;;
esac
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	previous := dryrun.Enabled()
	dryrun.Set(false)
	t.Cleanup(func() { dryrun.Set(previous) })
	return calls, dir
}

func TestDockerCLIPresenceDoesNotProvideADaemonButAllowsRootlessSetup(t *testing.T) {
	calls, _ := fakeRootlessDocker(t, "LoadState=not-found\nActiveState=inactive\n")
	socket, hits := pingServer(t, http.StatusOK, "OK")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
	state, err := DockerStatus(context.Background())
	if err != nil || state.Available || state.Active || state.Ready || state.Unavailable != "" {
		t.Fatalf("CLI-only supported host = %+v %v", state, err)
	}
	if hits.Load() != 0 {
		t.Fatal("a missing user unit was probed as running")
	}
	if err := SetDocker(context.Background(), true, nil); err != nil {
		t.Fatalf("supported host could not install rootless Docker: %v", err)
	}
	want := append([]string{dockerShowCall, dockerShowCall}, dockerInstallCalls()...)
	want = append(want, "setup install --force", "systemctl --user enable --now docker.service", dockerShowCall)
	assertCalls(t, calls, want...)
	state, err = DockerStatus(context.Background())
	if err != nil || !state.Available || !state.Active || !state.Ready || hits.Load() != 2 {
		t.Fatalf("installed user daemon not observed ready: %+v %v, pings %d", state, err, hits.Load())
	}
}

func TestDockerCoexistsWithAnAccessibleSystemSocket(t *testing.T) {
	fakeRootlessDocker(t, "LoadState=not-found\nActiveState=inactive\n")
	systemSocket, systemHits := pingServer(t, http.StatusOK, "OK")
	t.Setenv("DOCKER_TEST_SYSTEM_SOCKET", systemSocket)
	userSocket, userHits := pingServer(t, http.StatusOK, "OK")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(userSocket))
	if err := SetDocker(context.Background(), true, nil); err != nil {
		t.Fatalf("accessible system Docker blocked rootless setup: %v", err)
	}
	if userHits.Load() != 1 || systemHits.Load() != 0 {
		t.Fatalf("readiness must come only from rootless Docker: user pings %d, system pings %d", userHits.Load(), systemHits.Load())
	}
}

func TestDockerReadinessUsesOnlyTheUserRuntimeSocket(t *testing.T) {
	calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
	decoy, decoyHits := pingServer(t, http.StatusOK, "OK")
	t.Setenv("DOCKER_HOST", "unix://"+decoy)
	state, err := DockerStatus(context.Background())
	if err != nil || !state.Active || state.Ready || decoyHits.Load() != 0 {
		t.Fatalf("CLI/service/DOCKER_HOST supplied false readiness: %+v %v", state, err)
	}
	socket, hits := pingServer(t, http.StatusOK, "OK\n")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
	state, err = DockerStatus(context.Background())
	if err != nil || !state.Ready || hits.Load() != 1 || decoyHits.Load() != 0 {
		t.Fatalf("runtime socket not used exclusively: %+v %v, pings %d/%d", state, err, hits.Load(), decoyHits.Load())
	}
	assertCalls(t, calls, dockerShowCall, dockerShowCall)
}

func TestDockerInstallFailuresStopBeforeSetupAndPreserveObservedState(t *testing.T) {
	for i, install := range dockerInstallCalls() {
		pkg := strings.TrimPrefix(install, "brew install ")
		t.Run(pkg, func(t *testing.T) {
			calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
			socket, _ := pingServer(t, http.StatusOK, "OK")
			t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
			t.Setenv("DOCKER_TEST_FAIL", pkg)
			if err := SetDocker(context.Background(), true, nil); err == nil {
				t.Fatal("failed Homebrew install reported success")
			}
			want := append([]string{dockerShowCall}, dockerInstallCalls()[:i+1]...)
			assertCalls(t, calls, want...)
			state, err := DockerStatus(context.Background())
			if err != nil || !state.Active || !state.Ready {
				t.Fatalf("install failure hid the running daemon: %+v %v", state, err)
			}
		})
	}
}

func TestDockerSetupAndStartFailuresPreserveObservedState(t *testing.T) {
	for _, failure := range []string{"setup", "start"} {
		t.Run(failure, func(t *testing.T) {
			calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
			socket, _ := pingServer(t, http.StatusOK, "OK")
			t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
			t.Setenv("DOCKER_TEST_FAIL", failure)
			if err := SetDocker(context.Background(), true, nil); err == nil {
				t.Fatalf("failed %s reported success", failure)
			}
			want := append([]string{dockerShowCall}, dockerInstallCalls()...)
			want = append(want, "setup install --force")
			if failure == "start" {
				want = append(want, "systemctl --user enable --now docker.service")
			}
			assertCalls(t, calls, want...)
			state, err := DockerStatus(context.Background())
			if err != nil || !state.Active || !state.Ready {
				t.Fatalf("%s failure hid the running daemon: %+v %v", failure, state, err)
			}
		})
	}
}

func TestDockerStartRequiresObservedSocketReadiness(t *testing.T) {
	calls, _ := fakeRootlessDocker(t, "LoadState=not-found\nActiveState=inactive\n")
	if err := SetDocker(context.Background(), true, nil); err == nil {
		t.Fatal("successful setup and service start without a socket reported ready")
	}
	want := append([]string{dockerShowCall}, dockerInstallCalls()...)
	want = append(want, "setup install --force", "systemctl --user enable --now docker.service", dockerShowCall)
	assertCalls(t, calls, want...)
	state, err := DockerStatus(context.Background())
	if err != nil || !state.Active || state.Ready {
		t.Fatalf("unreachable started daemon state = %+v %v", state, err)
	}
}

func TestDockerDisableOnlyStopsTheUserServiceAndRetainsItsFiles(t *testing.T) {
	for _, failure := range []string{"", "disable", "disable-noop"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
			socket, _ := pingServer(t, http.StatusOK, "OK")
			t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
			paths := []string{
				filepath.Join(os.Getenv("HOME"), ".local", "share", "docker", "volumes", "project"),
				filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user", "docker.service"),
			}
			for _, path := range paths {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("keep Docker files"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("DOCKER_TEST_FAIL", failure)
			err := SetDocker(context.Background(), false, nil)
			if (err != nil) != (failure != "") {
				t.Fatalf("disable error = %v, failure mode %q", err, failure)
			}
			for _, call := range recordedCalls(t, calls) {
				if call != dockerShowCall && call != "systemctl --user disable --now docker.service" {
					t.Fatalf("disable ran an install, setup, or non-user command: %s", call)
				}
			}
			state, err := DockerStatus(context.Background())
			if err != nil || !state.Available || state.Active != (failure != "") || state.Ready != (failure != "") {
				t.Fatalf("disable must report observed state: %+v %v", state, err)
			}
			for _, path := range paths {
				if got, err := os.ReadFile(path); err != nil || string(got) != "keep Docker files" {
					t.Fatalf("disable changed %s: %q %v", path, got, err)
				}
			}
		})
	}
}

func TestDockerMutationRequiresAReadablePostApplyState(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
		socket, _ := pingServer(t, http.StatusOK, "OK")
		t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
		t.Setenv("DOCKER_TEST_FAIL", "post-status")
		if err := SetDocker(context.Background(), enabled, nil); err == nil {
			t.Fatalf("mutation enabled=%v accepted an unreadable post-apply state", enabled)
		}
	}
}

func TestDockerDryRunPreviewsEveryMutationWithoutChangingState(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		calls, _ := fakeRootlessDocker(t, "LoadState=not-found\nActiveState=inactive\n")
		dryrun.Set(true)
		var output bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&output)
		err := SetDocker(context.Background(), enabled, nil)
		log.SetOutput(previous)
		if err != nil {
			t.Fatalf("dry-run required a real post-apply daemon: %v", err)
		}
		for _, call := range recordedCalls(t, calls) {
			if call != dockerShowCall {
				t.Fatalf("dry-run executed a mutation: %s", call)
			}
		}
		if enabled {
			for _, install := range dockerInstallCalls() {
				if !strings.Contains(output.String(), install) {
					t.Fatalf("dry-run omitted %s: %s", install, &output)
				}
			}
			if !strings.Contains(output.String(), "dockerd-rootless-setuptool.sh") || !strings.Contains(output.String(), "install") {
				t.Fatalf("dry-run omitted rootless setup: %s", &output)
			}
		}
		if !strings.Contains(output.String(), "--user") || !strings.Contains(output.String(), "--now") || !strings.Contains(output.String(), "docker.service") {
			t.Fatalf("dry-run omitted user service mutation: %s", &output)
		}
		state, err := DockerStatus(context.Background())
		if err != nil || state.Available || state.Active || state.Ready {
			t.Fatalf("dry-run invented daemon state: %+v %v", state, err)
		}
	}
}

func TestDockerRootlessPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		goos    string
		runtime string
		missing string
	}{
		{"supported", "linux", "/run/user/1000", ""},
		{"not Linux", "darwin", "/run/user/1000", ""},
		{"no runtime", "linux", "", ""},
		{"relative runtime", "linux", "runtime", ""},
		{"missing uidmap", "linux", "/run/user/1000", "newuidmap"},
		{"missing gidmap", "linux", "/run/user/1000", "newgidmap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := rootlessDockerUnavailable(tc.goos, tc.runtime, func(name string) (string, error) {
				if name != "newuidmap" && name != "newgidmap" {
					t.Fatalf("unexpected prerequisite lookup: %s", name)
				}
				if name == tc.missing {
					return "", errors.New("not installed")
				}
				return "/usr/bin/" + name, nil
			})
			if (reason == "") != (tc.name == "supported") {
				t.Fatalf("unavailable reason = %q", reason)
			}
		})
	}
}

func TestDockerUnsupportedHostRefusesInstallButCanStopExistingService(t *testing.T) {
	calls, dir := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
	if err := os.Remove(filepath.Join(dir, "newgidmap")); err != nil {
		t.Fatal(err)
	}
	socket, _ := pingServer(t, http.StatusOK, "OK")
	t.Setenv("XDG_RUNTIME_DIR", filepath.Dir(socket))
	state, err := DockerStatus(context.Background())
	if err != nil || state.Unavailable == "" || !state.Active || !state.Ready {
		t.Fatalf("unsupported host lost observed user daemon: %+v %v", state, err)
	}
	if err := SetDocker(context.Background(), true, nil); err == nil {
		t.Fatal("host without uidmap prerequisites installed Docker")
	}
	assertCalls(t, calls, dockerShowCall, dockerShowCall)
	if err := SetDocker(context.Background(), false, nil); err != nil {
		t.Fatalf("unsupported host could not stop existing user daemon: %v", err)
	}
}

func TestDockerInvalidRuntimeRefusesSetupWithoutInventingAStoppedService(t *testing.T) {
	for _, runtimeDir := range []string{"", "relative-runtime"} {
		calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
		t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
		state, err := DockerStatus(context.Background())
		if err != nil || state.Unavailable == "" || !state.Active || state.Ready {
			t.Fatalf("invalid runtime %q lost observed service state: %+v %v", runtimeDir, state, err)
		}
		if err := SetDocker(context.Background(), true, nil); err == nil {
			t.Fatalf("runtime %q allowed rootless setup", runtimeDir)
		}
		assertCalls(t, calls, dockerShowCall, dockerShowCall)
	}
}

func TestDockerUserBusFailuresRemainErrorsAndExplainMissingSession(t *testing.T) {
	for _, tc := range []struct {
		message string
		session bool
	}{
		{"Failed to connect to user scope bus via local transport: No medium found", true},
		{"Failed to connect to bus: No such file or directory", true},
		{"Failed to connect to user scope bus via local transport: Permission denied", true},
		{"unexpected systemctl failure", false},
	} {
		t.Run(tc.message, func(t *testing.T) {
			calls, _ := fakeRootlessDocker(t, "LoadState=loaded\nActiveState=active\n")
			t.Setenv("DOCKER_TEST_STATUS_ERROR", tc.message)
			state, err := DockerStatus(context.Background())
			var prerequisite *PrerequisiteError
			if err == nil || errors.As(err, &prerequisite) != tc.session || (state.Unavailable != "") != tc.session {
				t.Fatalf("failed observation lost its error or prerequisite classification: %+v %v", state, err)
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 14 {
				t.Fatalf("lost systemctl failure: %v", err)
			}
			if err := SetDocker(context.Background(), true, nil); err == nil {
				t.Fatal("an unavailable user bus allowed installation")
			}
			assertCalls(t, calls, dockerShowCall, dockerShowCall)
		})
	}
}

func TestDockerMissingSetupRequirementsPreserveTheInstallerFailure(t *testing.T) {
	for _, diagnostic := range []string{
		"Missing system requirements.\n# Add subuid entry for account\n# Add subgid entry for account",
		"Missing system requirements.\n# Install iptables\n# Load nf_tables module",
	} {
		calls, _ := fakeRootlessDocker(t, "LoadState=not-found\nActiveState=inactive\n")
		t.Setenv("DOCKER_TEST_SETUP_ERROR", diagnostic)
		err := SetDocker(context.Background(), true, nil)
		var prerequisite *PrerequisiteError
		var exit *exec.ExitError
		if !errors.As(err, &prerequisite) || !errors.As(err, &exit) || exit.ExitCode() != 12 {
			t.Fatalf("missing setup requirements lost actionable classification or cause: %v", err)
		}
		want := append([]string{dockerShowCall}, dockerInstallCalls()...)
		assertCalls(t, calls, append(want, "setup install --force")...)
	}
}
