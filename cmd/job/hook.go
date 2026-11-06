package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bensyverson/jobs/internal/hook"
)

// `job hook` — job's agent-app hooks, and the verbs that register them. None
// of them opens a database or records an event, so none needs --as.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Agent-app hooks: block plan mode in job projects, and install that hook",
		Long: "job ships a Claude Code hook that turns plan mode away in any folder under a job project " +
			"and points the agent at the job workflow instead (draft a plan, `job import` it, work leaf by leaf).\n\n" +
			"  job hook install      register it in each agent app that is present\n" +
			"  job hook uninstall    remove it again\n" +
			"  job hook plan-mode    the hook itself; Claude Code runs it, you don't",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newHookPlanModeCmd(), newHookInstallCmd(), newHookUninstallCmd())
	return cmd
}

func newHookPlanModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plan-mode",
		Short: "Claude Code PreToolUse hook: deny EnterPlanMode under a .jobs.db",
		Long: "Reads a Claude Code PreToolUse payload on stdin. When its cwd (or, without one, this process's working directory) " +
			"is in a job project — a .jobs.db or .jobs/log there or in a parent — it prints a deny reply whose reason sends the agent to the job workflow. " +
			"Anywhere else it prints nothing. It always exits 0, so a broken payload never blocks the session.\n\n" +
			"Register it with `job hook install`, which writes:\n\n" +
			"  \"PreToolUse\": [{\"matcher\": \"EnterPlanMode\", \"hooks\": [{\"type\": \"command\", \"command\": \"/opt/homebrew/bin/job hook plan-mode\"}]}]",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, _ := io.ReadAll(cmd.InOrStdin())
			wd, _ := os.Getwd()
			out := hook.PlanMode(payload, wd)
			if out == nil {
				return nil
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			return enc.Encode(out)
		},
	}
}

func newHookInstallCmd() *cobra.Command {
	var harness string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Register the plan-mode hook in Claude Code's settings",
		Long: "Adds `job hook plan-mode` to ~/.claude/settings.json as a PreToolUse hook for EnterPlanMode, by this binary's full path " +
			"(the stable /opt/homebrew/bin/job on a Homebrew install), so a session whose PATH lacks Homebrew still finds it.\n\n" +
			"Every other key in the file is kept, in order. An older plan-mode block — another job path, or block-plan-mode.sh — is replaced, not doubled. " +
			"Running it again changes nothing. A settings file that is not valid JSON is refused and left alone.\n\n" +
			"--harness picks the app (claude-code or opencode); without it, each app with a config folder here gets the hook. " +
			"OpenCode has nothing to install: its plan mode is an agent the person picks, not a tool call a hook can block.\n\n" +
			"  job hook install\n" +
			"  job hook install --harness claude-code",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, targets, err := hookTargets(cmd, harness)
			if err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("find this binary's path: %w", err)
			}
			command := hook.PlanModeCommand(hook.StableExecutable(exe))
			return eachHarness(cmd, targets, func(h hook.Harness) ([]string, error) {
				return env.Install(h, command)
			})
		},
	}
	registerHarnessFlag(cmd, &harness, "install for")
	return cmd
}

func newHookUninstallCmd() *cobra.Command {
	var harness string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the plan-mode hook from Claude Code's settings",
		Long: "Removes `job hook plan-mode` — by any path — from ~/.claude/settings.json and leaves every other hook in place, block-plan-mode.sh included. " +
			"--harness picks the app; without it, each app with a config folder here.\n\n" +
			"  job hook uninstall --harness claude-code",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, targets, err := hookTargets(cmd, harness)
			if err != nil {
				return err
			}
			return eachHarness(cmd, targets, env.Uninstall)
		},
	}
	registerHarnessFlag(cmd, &harness, "remove from")
	return cmd
}

func registerHarnessFlag(cmd *cobra.Command, target *string, verb string) {
	cmd.Flags().StringVar(target, "harness", "",
		fmt.Sprintf("agent app to %s: claude-code or opencode (default: each app present)", verb))
}

// hookTargets resolves --harness, or the apps present when it is unset.
func hookTargets(cmd *cobra.Command, flag string) (hook.Env, []hook.Harness, error) {
	env, err := hook.EnvFromOS()
	if err != nil {
		return hook.Env{}, nil, err
	}
	if cmd.Flags().Changed("harness") {
		h, err := hook.ParseHarness(flag)
		if err != nil {
			return hook.Env{}, nil, err
		}
		return env, []hook.Harness{h}, nil
	}
	return env, env.Present(), nil
}

func eachHarness(cmd *cobra.Command, targets []hook.Harness, run func(hook.Harness) ([]string, error)) error {
	w := cmd.OutOrStdout()
	if len(targets) == 0 {
		names := make([]string, len(hook.Harnesses))
		for i, h := range hook.Harnesses {
			names[i] = string(h)
		}
		fmt.Fprintf(w, "No agent app found (looked for ~/.claude and ~/.config/opencode); nothing changed. Pass --harness %s to choose one.\n",
			strings.Join(names, "|"))
		return nil
	}
	for _, h := range targets {
		lines, err := run(h)
		if err != nil {
			return err
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}
	return nil
}
