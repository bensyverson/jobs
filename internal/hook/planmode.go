// Package hook holds job's agent-app hooks and the code that registers them
// in each app's settings. Today that is one hook: a Claude Code PreToolUse
// hook that turns plan mode away in a job project and sends the agent to the
// job workflow instead.
package hook

import (
	"encoding/json"
	"path/filepath"

	job "github.com/bensyverson/jobs/internal/job"
)

// PermissionDecision is a PreToolUse hook's verdict on a tool call.
type PermissionDecision string

// DecisionDeny blocks the tool call and shows the model the reason.
const DecisionDeny PermissionDecision = "deny"

// preToolUseEvent is the hookEventName Claude Code expects in a PreToolUse
// reply.
const preToolUseEvent = "PreToolUse"

// planModeTool is the tool Claude Code calls to enter plan mode.
const planModeTool = "EnterPlanMode"

// HookPayload is the part of Claude Code's hook input this hook reads.
type HookPayload struct {
	Cwd           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
}

// PreToolUseDecision is the hookSpecificOutput of a PreToolUse reply.
type PreToolUseDecision struct {
	HookEventName            string             `json:"hookEventName"`
	PermissionDecision       PermissionDecision `json:"permissionDecision"`
	PermissionDecisionReason string             `json:"permissionDecisionReason"`
}

// PreToolUseOutput is the JSON a PreToolUse hook prints on stdout, exit 0.
type PreToolUseOutput struct {
	HookSpecificOutput PreToolUseDecision `json:"hookSpecificOutput"`
}

// planModeReason is what the model reads in place of plan mode.
const planModeReason = "Plan mode is off in this project: it tracks its work with job (a .jobs.db or .jobs/log is present). " +
	"Don't use EnterPlanMode or ExitPlanMode here. Instead, draft the plan as a `tasks:` block in a file " +
	"(`job schema` shows the format), `job import <file>` it as a parent with leaf tasks (preview with --dry-run), " +
	"then work leaf by leaf: `job claim <id>`, red/green TDD, and close each with `job done <id> -F <file>`."

// PlanMode decides a PreToolUse call for EnterPlanMode. It returns a deny
// when the payload's cwd — or fallbackDir, when the payload has none or
// cannot be read — is inside a job project, and nil when the hook has
// nothing to say, which lets plan mode proceed.
func PlanMode(payload []byte, fallbackDir string) *PreToolUseOutput {
	var in HookPayload
	// An unreadable payload still has a working directory to go on; failing
	// the hook would let plan mode through in a job project.
	_ = json.Unmarshal(payload, &in)
	if in.ToolName != "" && in.ToolName != planModeTool {
		return nil
	}
	dir := in.Cwd
	if dir == "" {
		dir = fallbackDir
	}
	abs, err := filepath.Abs(dir)
	if err != nil || job.FindDBFrom(abs) == "" {
		return nil
	}
	return &PreToolUseOutput{HookSpecificOutput: PreToolUseDecision{
		HookEventName:            preToolUseEvent,
		PermissionDecision:       DecisionDeny,
		PermissionDecisionReason: planModeReason,
	}}
}
