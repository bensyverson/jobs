package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// project makes a temp tree with a .jobs.db at its root and returns the root
// and a nested folder beneath it.
func project(t *testing.T) (root, nested string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".jobs.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	nested = filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, nested
}

func payload(t *testing.T, cwd, tool string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"session_id":      "abc123",
		"cwd":             cwd,
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_input":      map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPlanMode_DeniesUnderAJobsDB(t *testing.T) {
	_, nested := project(t)
	out := PlanMode(payload(t, nested, "EnterPlanMode"), t.TempDir())
	if out == nil {
		t.Fatal("PlanMode returned no decision under a .jobs.db")
	}
	got := out.HookSpecificOutput
	if got.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", got.HookEventName)
	}
	if got.PermissionDecision != DecisionDeny {
		t.Errorf("permissionDecision = %q", got.PermissionDecision)
	}
	for _, want := range []string{"job import", "job done <id> -F <file>", "leaf by leaf"} {
		if !strings.Contains(got.PermissionDecisionReason, want) {
			t.Errorf("reason missing %q: %s", want, got.PermissionDecisionReason)
		}
	}
	if strings.Contains(got.PermissionDecisionReason, "-m") {
		t.Errorf("reason must not teach -m: %s", got.PermissionDecisionReason)
	}
}

// The reply is exactly Claude Code's PreToolUse shape: field names are the
// wire contract.
func TestPlanMode_ReplyWireShape(t *testing.T) {
	_, nested := project(t)
	out := PlanMode(payload(t, nested, "EnterPlanMode"), t.TempDir())
	if out == nil {
		t.Fatal("no decision")
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]map[string]string
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("reply is not the expected shape: %v\n%s", err, b)
	}
	h := generic["hookSpecificOutput"]
	if h["hookEventName"] != "PreToolUse" || h["permissionDecision"] != "deny" || h["permissionDecisionReason"] == "" {
		t.Errorf("unexpected reply: %s", b)
	}
}

// Same call, two folders: the .jobs.db is the only difference.
func TestPlanMode_SilentOutsideAProject(t *testing.T) {
	_, nested := project(t)
	if out := PlanMode(payload(t, nested, "EnterPlanMode"), t.TempDir()); out == nil {
		t.Fatal("control: PlanMode did not deny inside a project")
	}
	if out := PlanMode(payload(t, t.TempDir(), "EnterPlanMode"), t.TempDir()); out != nil {
		t.Errorf("PlanMode denied outside a project: %+v", out)
	}
}

// A fresh clone has .jobs/log and no cache yet; job treats that as a project,
// so the hook does too.
func TestPlanMode_DeniesInAFreshCloneWithOnlyTheLog(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".jobs", "log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := PlanMode(payload(t, root, "EnterPlanMode"), t.TempDir()); out == nil {
		t.Error("PlanMode allowed plan mode in a clone with .jobs/log")
	}
}

func TestPlanMode_NoCwdFallsBackToTheGivenDir(t *testing.T) {
	_, nested := project(t)
	b := []byte(`{"hook_event_name":"PreToolUse","tool_name":"EnterPlanMode"}`)
	if out := PlanMode(b, nested); out == nil {
		t.Error("no cwd in payload: expected fallback dir under a .jobs.db to deny")
	}
	if out := PlanMode(b, t.TempDir()); out != nil {
		t.Error("no cwd in payload: fallback dir outside a project should be silent")
	}
}

func TestPlanMode_UnreadablePayloadFallsBack(t *testing.T) {
	_, nested := project(t)
	for _, b := range [][]byte{nil, []byte("not json")} {
		if out := PlanMode(b, nested); out == nil {
			t.Errorf("payload %q: expected fallback dir to deny", b)
		}
	}
}

// Registered without a matcher, the hook would see every tool; it must only
// ever answer for EnterPlanMode.
func TestPlanMode_IgnoresOtherTools(t *testing.T) {
	_, nested := project(t)
	if out := PlanMode(payload(t, nested, "EnterPlanMode"), t.TempDir()); out == nil {
		t.Fatal("control: EnterPlanMode was not denied")
	}
	if out := PlanMode(payload(t, nested, "Bash"), t.TempDir()); out != nil {
		t.Errorf("PlanMode denied a Bash call: %+v", out)
	}
}
