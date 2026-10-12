package shellenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/projectbluefin/chairlift/internal/dryrun"
)

const (
	defaultBlingDir = "/usr/share/ublue-os/bling"
	blingStartMark  = "### %s source start"
	blingEndMark    = "### %s source end"
	blingSourceLine = "test -f %s/%s && source %s/%s"
)

// ShellTarget describes the script and target config file for a given shell.
type ShellTarget struct {
	Shell      string
	Script     string
	ConfigFile string
}

// ResolveShellTarget determines the bling script and user rc file for a given shell name.
// Returns target and true if supported (bash, zsh, fish), false otherwise.
func ResolveShellTarget(shellName string) (ShellTarget, bool) {
	home := os.Getenv("HOME")
	base := filepath.Base(shellName)
	switch base {
	case "bash":
		return ShellTarget{
			Shell:      "bash",
			Script:     "bling.sh",
			ConfigFile: filepath.Join(home, ".bashrc"),
		}, true
	case "zsh":
		zdotdir := os.Getenv("ZDOTDIR")
		if zdotdir == "" {
			zdotdir = home
		}
		return ShellTarget{
			Shell:      "zsh",
			Script:     "bling.sh",
			ConfigFile: filepath.Join(zdotdir, ".zshrc"),
		}, true
	case "fish":
		xdgConfig := os.Getenv("XDG_CONFIG_HOME")
		if xdgConfig == "" {
			xdgConfig = filepath.Join(home, ".config")
		}
		return ShellTarget{
			Shell:      "fish",
			Script:     "bling.fish",
			ConfigFile: filepath.Join(xdgConfig, "fish", "config.fish"),
		}, true
	default:
		return ShellTarget{}, false
	}
}

// CurrentShellTarget resolves the shell target for the user's $SHELL.
func CurrentShellTarget() (ShellTarget, bool) {
	return ResolveShellTarget(os.Getenv("SHELL"))
}

// IsBlingInstalled reports whether the bling source line is already present in the config file.
func IsBlingInstalled(target ShellTarget) bool {
	if target.ConfigFile == "" {
		return false
	}
	content, err := os.ReadFile(target.ConfigFile)
	if err != nil {
		return false
	}
	needle := fmt.Sprintf("source %s/%s", defaultBlingDir, target.Script)
	return strings.Contains(string(content), needle)
}

// EnableBling idempotently appends the bling source block to the shell config file.
// If already installed or shell is unsupported, it is a no-op.
// Under dry-run, no disk writes occur.
func EnableBling() error {
	target, ok := CurrentShellTarget()
	if !ok {
		return nil // Unsupported shell; do not touch
	}
	return EnableBlingTarget(target, defaultBlingDir)
}

// EnableBlingTarget idempotently writes the bling snippet to target's config file.
func EnableBlingTarget(target ShellTarget, blingDir string) error {
	if IsBlingInstalled(target) {
		return nil
	}
	if dryrun.Enabled() {
		return nil
	}

	dir := filepath.Dir(target.ConfigFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	snippet := fmt.Sprintf("\n"+blingStartMark+"\n"+blingSourceLine+"\n"+blingEndMark+"\n",
		target.Script, blingDir, target.Script, blingDir, target.Script, target.Script)

	f, err := os.OpenFile(target.ConfigFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(snippet); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
