package shellenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/projectbluefin/chairlift/internal/dryrun"
)

// withDryRun pins the process-wide dry-run switch for one test and restores
// the previous value afterwards.
func withDryRun(t *testing.T, on bool) {
	t.Helper()
	orig := dryrun.Enabled()
	dryrun.Set(on)
	t.Cleanup(func() { dryrun.Set(orig) })
}

func TestResolveShellTargetHonorsZDOTDIRAndXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	zdotdir := filepath.Join(t.TempDir(), "zdot")
	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", zdotdir)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	zsh, ok := ResolveShellTarget("/usr/bin/zsh")
	if !ok {
		t.Fatal("ResolveShellTarget(zsh) ok = false, want true")
	}
	if want := filepath.Join(zdotdir, ".zshrc"); zsh.ConfigFile != want {
		t.Errorf("zsh ConfigFile = %q, want %q (ZDOTDIR must win over HOME)", zsh.ConfigFile, want)
	}

	fish, ok := ResolveShellTarget("fish")
	if !ok {
		t.Fatal("ResolveShellTarget(fish) ok = false, want true")
	}
	if want := filepath.Join(xdg, "fish", "config.fish"); fish.ConfigFile != want {
		t.Errorf("fish ConfigFile = %q, want %q (XDG_CONFIG_HOME must win over HOME/.config)", fish.ConfigFile, want)
	}

	bash, ok := ResolveShellTarget("/bin/bash")
	if !ok {
		t.Fatal("ResolveShellTarget(bash) ok = false, want true")
	}
	if want := filepath.Join(home, ".bashrc"); bash.ConfigFile != want {
		t.Errorf("bash ConfigFile = %q, want %q (bash ignores ZDOTDIR and XDG_CONFIG_HOME)", bash.ConfigFile, want)
	}
}

func TestCurrentShellTargetReadsSHELL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	t.Setenv("SHELL", "/usr/bin/fish")
	got, ok := CurrentShellTarget()
	if !ok || got.Shell != "fish" || got.Script != "bling.fish" {
		t.Fatalf("CurrentShellTarget() with SHELL=fish = %+v, %v; want fish/bling.fish, true", got, ok)
	}
	if want := filepath.Join(home, ".config", "fish", "config.fish"); got.ConfigFile != want {
		t.Errorf("CurrentShellTarget() ConfigFile = %q, want %q", got.ConfigFile, want)
	}

	for _, shell := range []string{"", "/bin/tcsh", "/usr/bin/nu"} {
		t.Setenv("SHELL", shell)
		if got, ok := CurrentShellTarget(); ok {
			t.Errorf("CurrentShellTarget() with SHELL=%q = %+v, true; want unsupported", shell, got)
		}
	}
}

func TestBlingInstalledDetection(t *testing.T) {
	dir := t.TempDir()
	const blingDir = "/opt/bling"

	if IsBlingInstalledDir(ShellTarget{Script: "bling.sh"}, blingDir) {
		t.Error("IsBlingInstalledDir with empty ConfigFile = true, want false")
	}

	missing := ShellTarget{Script: "bling.sh", ConfigFile: filepath.Join(dir, "absent")}
	if IsBlingInstalledDir(missing, blingDir) {
		t.Error("IsBlingInstalledDir with missing config file = true, want false")
	}

	rc := filepath.Join(dir, ".bashrc")
	target := ShellTarget{Shell: "bash", Script: "bling.sh", ConfigFile: rc}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(rc, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("export PATH=$HOME/bin:$PATH\n")
	if IsBlingInstalledDir(target, blingDir) {
		t.Error("IsBlingInstalledDir without a source line = true, want false")
	}

	write("source /elsewhere/bling.sh\n")
	if IsBlingInstalledDir(target, blingDir) {
		t.Error("IsBlingInstalledDir with another directory's source line = true, want false")
	}

	write("source /opt/bling/bling.fish\n")
	if IsBlingInstalledDir(target, blingDir) {
		t.Error("IsBlingInstalledDir with another script's source line = true, want false")
	}

	write("# mine\ntest -f /opt/bling/bling.sh && source /opt/bling/bling.sh\n")
	if !IsBlingInstalledDir(target, blingDir) {
		t.Error("IsBlingInstalledDir with the matching source line = false, want true")
	}
	if IsBlingInstalled(target) {
		t.Error("IsBlingInstalled (default directory) matched a line for /opt/bling, want false")
	}

	write("source " + defaultBlingDir + "/bling.sh\n")
	if !IsBlingInstalled(target) {
		t.Errorf("IsBlingInstalled with a %s source line = false, want true", defaultBlingDir)
	}
}

func TestEnableBlingUnsupportedShellTouchesNothing(t *testing.T) {
	withDryRun(t, false)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/tcsh")

	if err := EnableBling(); err != nil {
		t.Fatalf("EnableBling() with unsupported shell err = %v, want nil", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("EnableBling() with unsupported shell wrote %d entries into HOME, want none", len(entries))
	}
}

func TestEnableBlingWritesCurrentShellConfig(t *testing.T) {
	withDryRun(t, false)
	home := t.TempDir()
	xdg := filepath.Join(home, "not-yet-created", "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("SHELL", "/usr/bin/fish")

	if err := EnableBling(); err != nil {
		t.Fatalf("EnableBling() err = %v", err)
	}

	cfg := filepath.Join(xdg, "fish", "config.fish")
	content, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("EnableBling() did not create %s: %v", cfg, err)
	}
	want := "\n### bling.fish source start\n" +
		"test -f " + defaultBlingDir + "/bling.fish && source " + defaultBlingDir + "/bling.fish\n" +
		"### bling.fish source end\n"
	if string(content) != want {
		t.Errorf("config.fish = %q, want %q", content, want)
	}

	target, _ := CurrentShellTarget()
	if !IsBlingInstalled(target) {
		t.Error("IsBlingInstalled after EnableBling() = false, want true")
	}

	if err := EnableBling(); err != nil {
		t.Fatalf("second EnableBling() err = %v", err)
	}
	again, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != want {
		t.Errorf("second EnableBling() changed config.fish to %q", again)
	}
}

func TestEnableBlingTargetAppendsAfterExistingContent(t *testing.T) {
	withDryRun(t, false)
	rc := filepath.Join(t.TempDir(), ".zshrc")
	const existing = "export EDITOR=vim\nalias ll='ls -l'"
	if err := os.WriteFile(rc, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	target := ShellTarget{Shell: "zsh", Script: "bling.sh", ConfigFile: rc}

	if err := EnableBlingTarget(target, "/opt/bling"); err != nil {
		t.Fatalf("EnableBlingTarget() err = %v", err)
	}
	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), existing+"\n### bling.sh source start\n") {
		t.Errorf("existing content not preserved ahead of the bling block: %q", content)
	}
	if !strings.HasSuffix(string(content), "### bling.sh source end\n") {
		t.Errorf("bling block not terminated by its end marker: %q", content)
	}
	if strings.Count(string(content), "source /opt/bling/bling.sh") != 1 {
		t.Errorf("want exactly one source line, got %q", content)
	}
	info, err := os.Stat(rc)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("EnableBlingTarget changed the rc file mode to %o, want 600", perm)
	}
}

func TestEnableBlingTargetDryRunLeavesExistingFileUntouched(t *testing.T) {
	withDryRun(t, true)
	rc := filepath.Join(t.TempDir(), ".bashrc")
	const existing = "export FOO=1\n"
	if err := os.WriteFile(rc, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	target := ShellTarget{Shell: "bash", Script: "bling.sh", ConfigFile: rc}

	if err := EnableBlingTarget(target, "/opt/bling"); err != nil {
		t.Fatalf("EnableBlingTarget() dry-run err = %v", err)
	}
	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != existing {
		t.Errorf("dry-run modified the rc file: %q", content)
	}
}

func TestEnableBlingTargetReportsMkdirFailure(t *testing.T) {
	withDryRun(t, false)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	target := ShellTarget{
		Shell:      "fish",
		Script:     "bling.fish",
		ConfigFile: filepath.Join(blocker, "fish", "config.fish"),
	}

	err := EnableBlingTarget(target, "/opt/bling")
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("EnableBlingTarget() under a regular file err = %v, want ENOTDIR", err)
	}
}

func TestEnableBlingTargetReportsOpenFailure(t *testing.T) {
	withDryRun(t, false)
	rcDir := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.Mkdir(rcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := ShellTarget{Shell: "bash", Script: "bling.sh", ConfigFile: rcDir}

	err := EnableBlingTarget(target, "/opt/bling")
	if !errors.Is(err, syscall.EISDIR) {
		t.Fatalf("EnableBlingTarget() on a directory err = %v, want EISDIR", err)
	}
}
