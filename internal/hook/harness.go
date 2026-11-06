package hook

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Harness is an agent app job can install its hook into.
type Harness string

const (
	ClaudeCode Harness = "claude-code"
	OpenCode   Harness = "opencode"
)

// Harnesses lists every harness, in the order install visits them.
var Harnesses = []Harness{ClaudeCode, OpenCode}

// ParseHarness reads a --harness value.
func ParseHarness(s string) (Harness, error) {
	for _, h := range Harnesses {
		if string(h) == s {
			return h, nil
		}
	}
	return "", fmt.Errorf("unknown harness %q: use claude-code or opencode, or leave --harness off to install for each app that is present", s)
}

// openCodeNothing is OpenCode's answer to install and uninstall alike. Its
// plan mode is the Plan agent, which the person switches to with Tab; the
// model has no tool call to enter it, so there is nothing for a hook to
// block (project/2026-11-06-plan-mode-hook.md).
const openCodeNothing = "OpenCode: nothing to install — its plan mode is an agent the person picks with Tab, not a tool the model calls, so there is nothing to block"

const openCodeNothingUninstall = "OpenCode: nothing to remove — job installs no hook for OpenCode"

// Env is where the agent apps keep their settings.
type Env struct {
	// Home is the user's home directory.
	Home string
	// ConfigHome is the XDG config directory, ~/.config unless overridden.
	ConfigHome string
}

// EnvFromOS reads Env from $HOME and $XDG_CONFIG_HOME.
func EnvFromOS() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, fmt.Errorf("find the home directory: %w", err)
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	return Env{Home: home, ConfigHome: config}, nil
}

// ClaudeSettings is the path of Claude Code's user settings.
func (e Env) ClaudeSettings() string {
	return filepath.Join(e.Home, ".claude", "settings.json")
}

// configDir is the folder whose presence says the app is set up here.
func (e Env) configDir(h Harness) string {
	switch h {
	case ClaudeCode:
		return filepath.Join(e.Home, ".claude")
	case OpenCode:
		return filepath.Join(e.ConfigHome, "opencode")
	}
	return ""
}

// Present lists the harnesses whose config folder exists.
func (e Env) Present() []Harness {
	var out []Harness
	for _, h := range Harnesses {
		if info, err := os.Stat(e.configDir(h)); err == nil && info.IsDir() {
			out = append(out, h)
		}
	}
	return out
}

// Install registers command as the plan-mode hook for h.
func (e Env) Install(h Harness, command string) ([]string, error) {
	switch h {
	case ClaudeCode:
		return InstallClaudeCode(e.ClaudeSettings(), command)
	case OpenCode:
		return []string{openCodeNothing}, nil
	}
	return nil, fmt.Errorf("unknown harness %q", h)
}

// Uninstall removes job's plan-mode hook for h.
func (e Env) Uninstall(h Harness) ([]string, error) {
	switch h {
	case ClaudeCode:
		return UninstallClaudeCode(e.ClaudeSettings())
	case OpenCode:
		return []string{openCodeNothingUninstall}, nil
	}
	return nil, fmt.Errorf("unknown harness %q", h)
}

// StableExecutable returns the path a hook should run this binary by. A
// Homebrew install runs from <prefix>/Cellar/job/<version>/bin/job, which
// the next upgrade deletes; when <prefix>/bin/job resolves to the same file,
// that symlink outlives upgrades and is returned instead. Any other path —
// the symlink itself, a go install, a dev build — is returned as given.
func StableExecutable(exe string) string {
	// .../Cellar/<formula>/<version>/bin/<name>
	parts := strings.Split(filepath.Clean(exe), string(filepath.Separator))
	n := len(parts)
	if n < 6 || parts[n-2] != "bin" || parts[n-5] != "Cellar" {
		return exe
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	link := filepath.Join(string(filepath.Separator), filepath.Join(parts[:n-5]...), "bin", parts[n-1])
	if target, err := filepath.EvalSymlinks(link); err == nil && target == resolved {
		return link
	}
	return exe
}

// PlanModeCommand is the hook command line for the binary at exe, quoted for
// the shell Claude Code runs it in when the path needs it.
func PlanModeCommand(exe string) string {
	return shellQuote(exe) + planModeSubcmd
}

func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_' || r == '+' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
