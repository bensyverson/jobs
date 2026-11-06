# The plan-mode hook ships in `job` (2026-11-06)

`job hook plan-mode`, `job hook install` and `job hook uninstall` replace the hand-installed `~/.claude/hooks/block-plan-mode.sh`, so the setup that installs `job` can register the hook with one command (`<full path to job> hook install --harness <claude-code|opencode>`) and never edits an agent app's settings itself. Leaves `j8nZ80` and `31R27o` under `Ej54Hv`; usage is in [the setup reference](../docs/content/docs/reference/setup.md#hook).

## Claude Code's contract

Checked against <https://code.claude.com/docs/en/hooks> on 2026-11-06:

- The PreToolUse payload on stdin carries `cwd`, `hook_event_name`, `tool_name` and `tool_input`, among others.
- A deny is `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "…"}}` on stdout with exit 0. Exit 0 with no output means "no decision", and the call goes on through the normal permission flow.
- Hooks register under `hooks.PreToolUse` as `{"matcher": …, "hooks": [{"type": "command", "command": …}]}`.

The hook also ignores any `tool_name` other than `EnterPlanMode`, so a registration without a matcher can't make it deny every tool.

## OpenCode has nothing to block

OpenCode's plan mode is the built-in **Plan** primary agent, which the person cycles to with Tab (or the `switch_agent` keybind); the model has no tool that enters it (<https://opencode.ai/docs/agents/>, read 2026-11-06). OpenCode's plugins are JS/TS modules that can throw in `tool.execute.before` to block a *tool call* (<https://opencode.ai/docs/plugins/>), but switching agents is not a tool call, and the plugin page lists no agent-switch event to intercept. So `--harness opencode` installs nothing and exits 0. What would change that: an OpenCode event or tool for switching agents.

## Which path the hook runs

The hook command names the binary by full path, because a session started before Homebrew was installed has no `/opt/homebrew/bin` on `PATH`. `os.Executable()` may return the versioned `Cellar/job/<version>/bin/job` path, which the next `brew upgrade` deletes. `hook.StableExecutable` therefore maps `<prefix>/Cellar/<formula>/<version>/bin/<name>` to `<prefix>/bin/<name>` when that symlink resolves to the same file, and otherwise keeps the path it was given. It works for any Homebrew prefix, including `/usr/local`.
