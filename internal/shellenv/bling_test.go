package shellenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/projectbluefin/chairlift/internal/dryrun"
)

func TestResolveShellTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	tests := []struct {
		shellName   string
		wantShell   string
		wantScript  string
		wantConfig  string
		wantSuccess bool
	}{
		{"/bin/bash", "bash", "bling.sh", filepath.Join(home, ".bashrc"), true},
		{"bash", "bash", "bling.sh", filepath.Join(home, ".bashrc"), true},
		{"/usr/bin/zsh", "zsh", "bling.sh", filepath.Join(home, ".zshrc"), true},
		{"/usr/bin/fish", "fish", "bling.fish", filepath.Join(home, ".config", "fish", "config.fish"), true},
		{"/bin/csh", "", "", "", false},
		{"tcsh", "", "", "", false},
		{"", "", "", "", false},
	}

	for _, tc := range tests {
		got, ok := ResolveShellTarget(tc.shellName)
		if ok != tc.wantSuccess {
			t.Errorf("ResolveShellTarget(%q) ok = %v, want %v", tc.shellName, ok, tc.wantSuccess)
		}
		if ok {
			if got.Shell != tc.wantShell || got.Script != tc.wantScript || got.ConfigFile != tc.wantConfig {
				t.Errorf("ResolveShellTarget(%q) = %+v, want shell=%q script=%q config=%q",
					tc.shellName, got, tc.wantShell, tc.wantScript, tc.wantConfig)
			}
		}
	}
}

func TestEnableBlingTargetIdempotent(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".bashrc")
	target := ShellTarget{
		Shell:      "bash",
		Script:     "bling.sh",
		ConfigFile: configFile,
	}

	// 1. Enable in fresh file
	if err := EnableBlingTarget(target, "/usr/share/ublue-os/bling"); err != nil {
		t.Fatalf("first EnableBlingTarget() err = %v", err)
	}

	content, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("ReadFile err = %v", err)
	}
	wantSnippet := "### bling.sh source start\ntest -f /usr/share/ublue-os/bling/bling.sh && source /usr/share/ublue-os/bling/bling.sh\n### bling.sh source end"
	if !strings.Contains(string(content), wantSnippet) {
		t.Fatalf("configFile content %q does not contain snippet %q", string(content), wantSnippet)
	}

	// 2. Second enable should be a no-op
	if err := EnableBlingTarget(target, "/usr/share/ublue-os/bling"); err != nil {
		t.Fatalf("second EnableBlingTarget() err = %v", err)
	}
	content2, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("ReadFile 2 err = %v", err)
	}
	if string(content) != string(content2) {
		t.Errorf("EnableBlingTarget not idempotent: content changed on second call")
	}
}

func TestEnableBlingTargetHonorsDryRun(t *testing.T) {
	orig := dryrun.Enabled()
	dryrun.Set(true)
	defer dryrun.Set(orig)

	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, ".bashrc")
	target := ShellTarget{
		Shell:      "bash",
		Script:     "bling.sh",
		ConfigFile: configFile,
	}

	if err := EnableBlingTarget(target, "/usr/share/ublue-os/bling"); err != nil {
		t.Fatalf("EnableBlingTarget dryrun err = %v", err)
	}
	if _, err := os.Stat(configFile); !os.IsNotExist(err) {
		t.Errorf("file created under dry-run: %v", configFile)
	}
}
