package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommand = "/opt/homebrew/bin/job hook plan-mode"

func writeSettings(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// planModeCommands returns every command registered for EnterPlanMode.
func planModeCommands(t *testing.T, body string) []string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("settings are not valid JSON: %v\n%s", err, body)
	}
	var out []string
	for _, g := range s.Hooks["PreToolUse"] {
		if g.Matcher != "EnterPlanMode" {
			continue
		}
		for _, h := range g.Hooks {
			if h.Type != "command" {
				t.Errorf("handler type = %q", h.Type)
			}
			out = append(out, h.Command)
		}
	}
	return out
}

const existingSettings = `{
  "model": "opus",
  "permissions": {
    "allow": ["Bash(ls:*)"],
    "deny": []
  },
  "hooks": {
    "PostToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "contextfill hook"}]}
    ],
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh", "timeout": 5}]}
    ]
  },
  "statusLine": {"type": "command", "command": "echo <hi> & bye"},
  "zzz": [1, 2.50, null, true]
}
`

func TestInstallClaudeCode_KeepsEveryKeyAndOrder(t *testing.T) {
	path := writeSettings(t, existingSettings, 0o644)
	lines, err := InstallClaudeCode(path, testCommand)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "Added") {
		t.Errorf("lines = %q", lines)
	}
	got := readFile(t, path)

	// Every original value survives untouched.
	var before, after map[string]any
	if err := json.Unmarshal([]byte(existingSettings), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &after); err != nil {
		t.Fatalf("result invalid: %v\n%s", err, got)
	}
	for _, k := range []string{"model", "permissions", "statusLine", "zzz"} {
		bb, _ := json.Marshal(before[k])
		ab, _ := json.Marshal(after[k])
		if string(bb) != string(ab) {
			t.Errorf("key %q changed: %s -> %s", k, bb, ab)
		}
	}
	hooksAfter := after["hooks"].(map[string]any)
	post, _ := json.Marshal(hooksAfter["PostToolUse"])
	if !strings.Contains(string(post), "contextfill hook") {
		t.Errorf("PostToolUse lost: %s", post)
	}
	pre, _ := json.Marshal(hooksAfter["PreToolUse"])
	if !strings.Contains(string(pre), "guard.sh") || !strings.Contains(string(pre), `"timeout":5`) {
		t.Errorf("existing PreToolUse entry lost: %s", pre)
	}
	if cmds := planModeCommands(t, got); len(cmds) != 1 || cmds[0] != testCommand {
		t.Errorf("plan-mode commands = %q", cmds)
	}

	// Key order is kept, and strings are not HTML-escaped.
	order := []string{`"model"`, `"permissions"`, `"hooks"`, `"statusLine"`, `"zzz"`}
	last := -1
	for _, k := range order {
		i := strings.Index(got, k)
		if i < last {
			t.Errorf("key %s moved:\n%s", k, got)
		}
		last = i
	}
	if strings.Index(got, `"PostToolUse"`) > strings.Index(got, `"PreToolUse"`) {
		t.Errorf("hooks order changed:\n%s", got)
	}
	if !strings.Contains(got, `echo <hi> & bye`) {
		t.Errorf("string was re-escaped:\n%s", got)
	}
	if !strings.Contains(got, "2.50") {
		t.Errorf("number was reformatted:\n%s", got)
	}
}

func TestInstallClaudeCode_SecondInstallChangesNothing(t *testing.T) {
	path := writeSettings(t, existingSettings, 0o644)
	if _, err := InstallClaudeCode(path, testCommand); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	info1, _ := os.Stat(path)
	lines, err := InstallClaudeCode(path, testCommand)
	if err != nil {
		t.Fatal(err)
	}
	if second := readFile(t, path); second != first {
		t.Errorf("second install changed the file:\n%s\n---\n%s", first, second)
	}
	info2, _ := os.Stat(path)
	if !info2.ModTime().Equal(info1.ModTime()) {
		t.Error("second install rewrote the file")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "already") {
		t.Errorf("second install should say nothing changed: %q", lines)
	}
}

func TestInstallClaudeCode_ReplacesTheOldScript(t *testing.T) {
	body := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "EnterPlanMode", "hooks": [{"type": "command", "command": "~/.claude/hooks/block-plan-mode.sh"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}
    ]
  }
}`
	path := writeSettings(t, body, 0o644)
	lines, err := InstallClaudeCode(path, testCommand)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "block-plan-mode.sh") {
		t.Errorf("old script still registered:\n%s", got)
	}
	if !strings.Contains(got, "guard.sh") {
		t.Errorf("unrelated entry lost:\n%s", got)
	}
	if cmds := planModeCommands(t, got); len(cmds) != 1 || cmds[0] != testCommand {
		t.Errorf("plan-mode commands = %q", cmds)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Removed") || !strings.Contains(joined, "block-plan-mode.sh") || !strings.Contains(joined, "Added") {
		t.Errorf("lines should report the removal and the addition: %q", lines)
	}
}

// An entry from another job binary (an old Cellar path, a dev build) is the
// same hook and is replaced, not doubled.
func TestInstallClaudeCode_ReplacesAnOlderJobPath(t *testing.T) {
	body := `{"hooks":{"PreToolUse":[{"matcher":"EnterPlanMode","hooks":[{"type":"command","command":"/opt/homebrew/Cellar/job/0.1.0/bin/job hook plan-mode"}]}]}}`
	path := writeSettings(t, body, 0o644)
	if _, err := InstallClaudeCode(path, testCommand); err != nil {
		t.Fatal(err)
	}
	if cmds := planModeCommands(t, readFile(t, path)); len(cmds) != 1 || cmds[0] != testCommand {
		t.Errorf("plan-mode commands = %q", cmds)
	}
}

// A group that holds the old script beside another handler keeps the other
// handler.
func TestInstallClaudeCode_KeepsNeighborsInASharedGroup(t *testing.T) {
	body := `{"hooks":{"PreToolUse":[{"matcher":"EnterPlanMode","hooks":[{"type":"command","command":"block-plan-mode.sh"},{"type":"command","command":"log.sh"}]}]}}`
	path := writeSettings(t, body, 0o644)
	if _, err := InstallClaudeCode(path, testCommand); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	cmds := planModeCommands(t, got)
	if strings.Join(cmds, ",") != "log.sh,"+testCommand {
		t.Errorf("plan-mode commands = %q\n%s", cmds, got)
	}
}

func TestInstallClaudeCode_CreatesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude", "settings.json")
	lines, err := InstallClaudeCode(path, testCommand)
	if err != nil {
		t.Fatal(err)
	}
	if cmds := planModeCommands(t, readFile(t, path)); len(cmds) != 1 {
		t.Errorf("plan-mode commands = %q", cmds)
	}
	if len(lines) != 1 {
		t.Errorf("lines = %q", lines)
	}
}

func TestInstallClaudeCode_RefusesInvalidJSON(t *testing.T) {
	for _, body := range []string{`{"model": "opus",`, `[1,2]`, `{"hooks": []}`, `{"hooks": {"PreToolUse": {}}}`} {
		path := writeSettings(t, body, 0o644)
		_, err := InstallClaudeCode(path, testCommand)
		if err == nil {
			t.Errorf("%s: install accepted it", body)
			continue
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error should name the file: %v", err)
		}
		if got := readFile(t, path); got != body {
			t.Errorf("%s: file was written: %s", body, got)
		}
	}
}

func TestInstallClaudeCode_KeepsTheFileMode(t *testing.T) {
	path := writeSettings(t, `{}`, 0o600)
	if _, err := InstallClaudeCode(path, testCommand); err != nil {
		t.Fatal(err)
	}
	if cmds := planModeCommands(t, readFile(t, path)); len(cmds) != 1 {
		t.Fatalf("not installed: %q", cmds)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

// A settings.json kept in a dotfiles repo is a symlink; the write lands in
// the target and the link survives.
func TestInstallClaudeCode_WritesThroughASymlink(t *testing.T) {
	target := writeSettings(t, `{"model":"opus"}`, 0o644)
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallClaudeCode(link, testCommand); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink replaced by a file")
	}
	if cmds := planModeCommands(t, readFile(t, target)); len(cmds) != 1 {
		t.Errorf("target not updated: %q", cmds)
	}
}

func TestUninstallClaudeCode_RemovesOnlyJobsEntry(t *testing.T) {
	body := `{
  "model": "opus",
  "hooks": {
    "PreToolUse": [
      {"matcher": "EnterPlanMode", "hooks": [{"type": "command", "command": "/opt/homebrew/bin/job hook plan-mode"}]},
      {"matcher": "EnterPlanMode", "hooks": [{"type": "command", "command": "block-plan-mode.sh"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "guard.sh"}]}
    ]
  }
}`
	path := writeSettings(t, body, 0o644)
	lines, err := UninstallClaudeCode(path)
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "job hook plan-mode") {
		t.Errorf("job's entry still there:\n%s", got)
	}
	for _, keep := range []string{"block-plan-mode.sh", "guard.sh", `"model"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("uninstall removed %s:\n%s", keep, got)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "Removed") {
		t.Errorf("lines = %q", lines)
	}
}

func TestUninstallClaudeCode_NothingToRemove(t *testing.T) {
	path := writeSettings(t, `{"model":"opus"}`, 0o644)
	lines, err := UninstallClaudeCode(path)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, path) != `{"model":"opus"}` {
		t.Error("uninstall rewrote a file it had nothing to remove from")
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "nothing") {
		t.Errorf("lines = %q", lines)
	}
	missing := filepath.Join(t.TempDir(), "settings.json")
	if _, err := UninstallClaudeCode(missing); err != nil {
		t.Errorf("uninstall with no file: %v", err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("uninstall created a file")
	}
}

func TestUninstallClaudeCode_RefusesInvalidJSON(t *testing.T) {
	path := writeSettings(t, `{nope`, 0o644)
	if _, err := UninstallClaudeCode(path); err == nil {
		t.Error("uninstall accepted invalid JSON")
	}
}
