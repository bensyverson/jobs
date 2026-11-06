package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code's settings keys and values this file reads and writes.
const (
	settingsHooks     = "hooks"
	groupHooks        = "hooks"
	settingsMatcher   = "matcher"
	handlerTypeCmd    = "command"
	planModeSubcmd    = " hook plan-mode"
	oldPlanModeScript = "block-plan-mode"
)

// handler is the part of a Claude Code hook handler this file reads.
type handler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

// matcherGroup is the group install writes: one matcher, one handler.
type matcherGroup struct {
	Matcher string    `json:"matcher"`
	Hooks   []handler `json:"hooks"`
}

// isJobPlanMode reports whether command runs some job binary's plan-mode
// hook — this one, an older Cellar path, or a dev build.
func isJobPlanMode(command string) bool {
	return strings.HasSuffix(strings.TrimSpace(command), planModeSubcmd)
}

// isPlanModeBlock reports whether command is any plan-mode block install
// supersedes: job's own, or the shell script it replaced.
func isPlanModeBlock(command string) bool {
	return isJobPlanMode(command) || strings.Contains(command, oldPlanModeScript)
}

// InstallClaudeCode registers command as a PreToolUse hook for EnterPlanMode
// in the Claude Code settings file at path, and returns one line per change.
// It replaces any older plan-mode block rather than adding a second, writes
// nothing when the hook is already there, and refuses a file that is not a
// JSON object.
func InstallClaudeCode(path, command string) ([]string, error) {
	s, err := loadSettings(path)
	if err != nil {
		return nil, err
	}
	groups, err := s.preToolUse()
	if err != nil {
		return nil, err
	}

	if alreadyInstalled(groups, command) {
		return []string{fmt.Sprintf("The plan-mode hook is already in %s; nothing changed", path)}, nil
	}

	groups, removed := removeHandlers(groups, isPlanModeBlock)
	groups = append(groups, encodeNoEscape(matcherGroup{
		Matcher: planModeTool,
		Hooks:   []handler{{Type: handlerTypeCmd, Command: command}},
	}))
	if err := s.save(groups); err != nil {
		return nil, err
	}

	var lines []string
	for _, c := range removed {
		lines = append(lines, fmt.Sprintf("Removed the old plan-mode hook %q from %s", c, path))
	}
	lines = append(lines, fmt.Sprintf("Added the plan-mode hook to %s (PreToolUse, matcher %s): %s", path, planModeTool, command))
	return lines, nil
}

// UninstallClaudeCode removes job's plan-mode hook from the Claude Code
// settings file at path, leaving every other hook — the old shell script
// included — where it is.
func UninstallClaudeCode(path string) ([]string, error) {
	nothing := []string{fmt.Sprintf("No job plan-mode hook in %s; nothing to remove", path)}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nothing, nil
	}
	s, err := loadSettings(path)
	if err != nil {
		return nil, err
	}
	groups, err := s.preToolUse()
	if err != nil {
		return nil, err
	}
	groups, removed := removeHandlers(groups, isJobPlanMode)
	if len(removed) == 0 {
		return nothing, nil
	}
	if err := s.save(groups); err != nil {
		return nil, err
	}
	var lines []string
	for _, c := range removed {
		lines = append(lines, fmt.Sprintf("Removed the plan-mode hook %q from %s", c, path))
	}
	return lines, nil
}

// settings is a loaded Claude Code settings file.
type settings struct {
	path  string
	root  object
	hooks object
}

func (s *settings) invalid(what string, err error) error {
	return fmt.Errorf("%s: %s (%v); nothing was written — fix the file and run this again", s.path, what, err)
}

// loadSettings reads path; a missing or blank file is an empty object.
func loadSettings(path string) (*settings, error) {
	s := &settings{path: path, root: object{}, hooks: object{}}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return s, nil
	}
	if s.root, err = parseObject(raw); err != nil {
		return nil, s.invalid("not a valid JSON object", err)
	}
	if h := s.root.get(settingsHooks); h != nil {
		if s.hooks, err = parseObject(h); err != nil {
			return nil, s.invalid(`"hooks" is not a JSON object`, err)
		}
	}
	return s, nil
}

// preToolUse returns the PreToolUse matcher groups, each as raw JSON.
func (s *settings) preToolUse() ([]json.RawMessage, error) {
	raw := s.hooks.get(preToolUseEvent)
	if raw == nil {
		return nil, nil
	}
	groups, err := parseArray(raw)
	if err != nil {
		return nil, s.invalid(`"hooks.PreToolUse" is not a JSON array`, err)
	}
	return groups, nil
}

// alreadyInstalled reports whether the only plan-mode block registered is
// exactly command, under the EnterPlanMode matcher.
func alreadyInstalled(groups []json.RawMessage, command string) bool {
	found, exact := 0, false
	for _, g := range groups {
		obj, err := parseObject(g)
		if err != nil {
			continue
		}
		var matcher string
		_ = json.Unmarshal(obj.get(settingsMatcher), &matcher)
		var hs []handler
		_ = json.Unmarshal(obj.get(groupHooks), &hs)
		for _, h := range hs {
			if !isPlanModeBlock(h.Command) {
				continue
			}
			found++
			exact = matcher == planModeTool && h.Type == handlerTypeCmd && h.Command == command
		}
	}
	return found == 1 && exact
}

// removeHandlers drops every handler whose command matches, and any group
// left with no handlers by that. Groups and handlers it does not touch keep
// their bytes.
func removeHandlers(groups []json.RawMessage, match func(string) bool) (kept []json.RawMessage, removed []string) {
	for _, g := range groups {
		obj, err := parseObject(g)
		if err != nil {
			kept = append(kept, g)
			continue
		}
		hs, err := parseArray(obj.get(groupHooks))
		if err != nil {
			kept = append(kept, g)
			continue
		}
		var left []json.RawMessage
		dropped := false
		for _, raw := range hs {
			var h handler
			if json.Unmarshal(raw, &h) == nil && match(h.Command) {
				removed = append(removed, h.Command)
				dropped = true
				continue
			}
			left = append(left, raw)
		}
		switch {
		case !dropped:
			kept = append(kept, g)
		case len(left) > 0:
			kept = append(kept, obj.set(groupHooks, marshalArray(left)).marshal())
		}
	}
	return kept, removed
}

// save writes groups back as hooks.PreToolUse.
func (s *settings) save(groups []json.RawMessage) error {
	s.hooks = s.hooks.set(preToolUseEvent, marshalArray(groups))
	s.root = s.root.set(settingsHooks, s.hooks.marshal())
	out, err := indent(s.root.marshal())
	if err != nil {
		return fmt.Errorf("render %s: %w", s.path, err)
	}
	return writeKeepingMode(s.path, out)
}

// writeKeepingMode writes b over path atomically — a temp file beside it,
// renamed into place — through a symlink to its target, keeping the file's
// mode. A new file gets 0644, as Claude Code's own settings have.
func writeKeepingMode(path string, b []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".*")
	if err != nil {
		return fmt.Errorf("create a temp file beside %s: %w", target, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("rename onto %s: %w", target, err)
	}
	return nil
}
