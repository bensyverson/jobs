package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookCLI runs `job hook …` with a temporary HOME and XDG config home, so no
// test ever reads or writes the real ~/.claude or ~/.config.
func hookCLI(t *testing.T, home, stdin string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"hook"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestHookPlanMode_DeniesUnderAJobsDB(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".jobs.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "src")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	in := `{"cwd":` + jsonString(sub) + `,"hook_event_name":"PreToolUse","tool_name":"EnterPlanMode","tool_input":{}}`
	out, err := hookCLI(t, t.TempDir(), in, "plan-mode")
	if err != nil {
		t.Fatalf("plan-mode: %v\n%s", err, out)
	}
	var reply struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatalf("stdout is not the reply JSON: %v\n%s", err, out)
	}
	if reply.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(reply.HookSpecificOutput.PermissionDecisionReason, "job import") {
		t.Errorf("reply = %+v", reply)
	}
}

func TestHookPlanMode_SilentElsewhere(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".jobs.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	control := `{"cwd":` + jsonString(proj) + `,"tool_name":"EnterPlanMode"}`
	if out, err := hookCLI(t, t.TempDir(), control, "plan-mode"); err != nil || out == "" {
		t.Fatalf("control: inside a project the hook should reply: err=%v out=%q", err, out)
	}
	in := `{"cwd":` + jsonString(dir) + `,"tool_name":"EnterPlanMode"}`
	out, err := hookCLI(t, t.TempDir(), in, "plan-mode")
	if err != nil || out != "" {
		t.Errorf("outside a project: err=%v out=%q", err, out)
	}
}

func TestHookPlanMode_NoCwdUsesTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".jobs.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	out, err := hookCLI(t, t.TempDir(), `{"tool_name":"EnterPlanMode"}`, "plan-mode")
	if err != nil || !strings.Contains(out, `"deny"`) {
		t.Errorf("err=%v out=%q", err, out)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func claudeSettings(home string) string { return filepath.Join(home, ".claude", "settings.json") }

func TestHookInstall_ClaudeCodeByFlag(t *testing.T) {
	home := t.TempDir()
	out, err := hookCLI(t, home, "", "install", "--harness", "claude-code")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	b, err := os.ReadFile(claudeSettings(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "hook plan-mode") || !strings.Contains(string(b), "EnterPlanMode") {
		t.Errorf("settings = %s", b)
	}
	if !strings.Contains(out, "Added") {
		t.Errorf("out = %q", out)
	}
	out, err = hookCLI(t, home, "", "install", "--harness", "claude-code")
	if err != nil || !strings.Contains(out, "already") {
		t.Errorf("second install: err=%v out=%q", err, out)
	}
}

// bootstrap runs exactly this for OpenCode users and treats non-zero as a
// failure.
func TestHookInstall_OpenCodeExitsZero(t *testing.T) {
	home := t.TempDir()
	for _, verb := range []string{"install", "uninstall"} {
		out, err := hookCLI(t, home, "", verb, "--harness", "opencode")
		if err != nil {
			t.Fatalf("%s --harness opencode: %v\n%s", verb, err, out)
		}
		if !strings.Contains(out, "OpenCode") {
			t.Errorf("%s out = %q", verb, out)
		}
	}
	if _, err := os.Stat(claudeSettings(home)); err == nil {
		t.Error("--harness opencode touched Claude Code's settings")
	}
}

func TestHookInstall_NoFlagInstallsForEachPresentApp(t *testing.T) {
	home := t.TempDir()
	out, err := hookCLI(t, home, "", "install")
	if err != nil {
		t.Fatalf("no apps: %v\n%s", err, out)
	}
	if _, err := os.Stat(claudeSettings(home)); err == nil {
		t.Error("installed for Claude Code with no ~/.claude present")
	}
	if !strings.Contains(out, "No") {
		t.Errorf("no apps: should say none was found: %q", out)
	}

	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(home, ".config", "opencode")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	out, err = hookCLI(t, home, "", "install")
	if err != nil {
		t.Fatalf("both apps: %v\n%s", err, out)
	}
	if _, err := os.Stat(claudeSettings(home)); err != nil {
		t.Errorf("Claude Code not installed: %v", err)
	}
	if !strings.Contains(out, "Added") || !strings.Contains(out, "OpenCode") {
		t.Errorf("out should cover both apps: %q", out)
	}
}

func TestHookInstall_UnknownHarnessIsAnError(t *testing.T) {
	out, err := hookCLI(t, t.TempDir(), "", "install", "--harness", "cursor")
	if err == nil || !strings.Contains(err.Error(), "claude-code") {
		t.Errorf("err=%v out=%q", err, out)
	}
}

func TestHookUninstall_ClaudeCode(t *testing.T) {
	home := t.TempDir()
	if _, err := hookCLI(t, home, "", "install", "--harness", "claude-code"); err != nil {
		t.Fatal(err)
	}
	out, err := hookCLI(t, home, "", "uninstall", "--harness", "claude-code")
	if err != nil || !strings.Contains(out, "Removed") {
		t.Errorf("err=%v out=%q", err, out)
	}
	b, _ := os.ReadFile(claudeSettings(home))
	if strings.Contains(string(b), "hook plan-mode") {
		t.Errorf("entry still there: %s", b)
	}
}

func TestHookInstall_RefusesInvalidSettings(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeSettings(home), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := hookCLI(t, home, "", "install", "--harness", "claude-code")
	if err == nil || !strings.Contains(err.Error(), claudeSettings(home)) || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("install should refuse invalid settings, naming the file: %v", err)
	}
	if b, _ := os.ReadFile(claudeSettings(home)); string(b) != "{oops" {
		t.Errorf("file was written: %s", b)
	}
}
