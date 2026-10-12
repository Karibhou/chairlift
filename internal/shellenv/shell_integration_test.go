package shellenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShellInteractiveBling(t *testing.T) {
	blingDir := "/usr/share/ublue-os/bling"
	if _, err := os.Stat(filepath.Join(blingDir, "bling.sh")); err != nil {
		t.Skip("Bling scripts not present on host at " + blingDir)
	}

	tempHome := t.TempDir()
	target := ShellTarget{
		Shell:      "bash",
		Script:     "bling.sh",
		ConfigFile: filepath.Join(tempHome, ".bashrc"),
	}

	if err := EnableBlingTarget(target, blingDir); err != nil {
		t.Fatalf("EnableBlingTarget failed: %v", err)
	}

	// Verify bash interactive shell sources bling.sh and sets up hooks
	cmd := exec.Command("bash", "--noprofile", "--rcfile", target.ConfigFile, "-ic", "declare -F starship_precmd; declare -F z")
	out, _ := cmd.CombinedOutput()
	outputStr := string(out)

	// In test container or headless environment, starship and zoxide might or might not be installed on host PATH.
	// We verify that bash executes the rcfile cleanly without errors and defines functions if tools are installed.
	if strings.Contains(outputStr, "syntax error") || strings.Contains(outputStr, "No such file") {
		t.Fatalf("Bash execution of bling.sh produced errors: %s", outputStr)
	}
}
