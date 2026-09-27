package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

// newBlockCmd builds the canonical `block` parent verb with `add` and
// `remove` subcommands. The parent itself also accepts the legacy
// `block <blocked> by <blocker>` form for backward compatibility, emitting
// a one-line stderr deprecation notice on every invocation. New code
// should prefer `block add` / `block remove`.
func newBlockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block",
		Short: "Block a task on another, or remove a block",
		Long:  "Manage blocking relationships between tasks. Subcommands: 'add' (declare a block), 'remove' (clear one). The bare form `job block <blocked> by <blocker>` is a deprecated alias for `block add` and emits a stderr notice on use.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 3 || args[1] != "by" {
				return fmt.Errorf("usage: job block add <blocked> by <blocker>  (or: job block remove <blocked> by <blocker>)")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.ErrOrStderr(), "note: `job block <blocked> by <blocker>` is an alias for `job block add <blocked> by <blocker>`; prefer the canonical form.")
			return runBlockAdd(cmd, args[0], []string{args[2]})
		},
	}
	cmd.AddCommand(newBlockAddCmd())
	cmd.AddCommand(newBlockRemoveCmd())
	return cmd
}

func newBlockAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <blocked> by <blocker> [blocker ...]",
		Short: "Declare that one or more tasks block another",
		Long:  "Declare that the blocked task cannot proceed until the listed blocker tasks are done. Multiple blockers in one call are atomic — all-or-nothing in a single transaction. Circular dependencies are detected (across the full input set) and rejected — a parent counts as waiting on its open children, so blocking a task on its own ancestor is refused too. Duplicate blockers in the input collapse to a single edge.",
		Args:  blockEdgeArgs("add"),
		RunE: func(cmd *cobra.Command, args []string) error {
			blocked, blockers := parseBlockEdgeArgs(args)
			return runBlockAdd(cmd, blocked, blockers)
		},
	}
}

func newBlockRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <blocked> by <blocker> [blocker ...]",
		Short: "Remove one or more blocking relationships",
		Long:  "Manually remove blocking relationships. Multiple blockers in one call are atomic. Blocking relationships are also auto-removed when the blocker task is marked done.",
		Args:  blockEdgeArgs("remove"),
		RunE: func(cmd *cobra.Command, args []string) error {
			blocked, blockers := parseBlockEdgeArgs(args)
			return runBlockRemove(cmd, blocked, blockers)
		},
	}
}

// blockEdgeArgs validates the canonical `<blocked> by <blocker> [blocker ...]`
// shape used by both `block add` and `block remove`. The verb name is
// embedded in the usage error so the message is unambiguous.
func blockEdgeArgs(verb string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < 3 || args[1] != "by" {
			return fmt.Errorf("usage: job block %s <blocked> by <blocker> [blocker ...]", verb)
		}
		return nil
	}
}

func parseBlockEdgeArgs(args []string) (blocked string, blockers []string) {
	return args[0], args[2:]
}

func runBlockAdd(cmd *cobra.Command, blocked string, blockers []string) error {
	db, err := openDBFromCmd()
	if err != nil {
		return err
	}
	defer db.Close()

	actor, err := requireAs(db)
	if err != nil {
		return err
	}

	if err := job.RunBlockMany(db, blocked, blockers, actor); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, b := range blockers {
		fmt.Fprintf(out, "Blocked: %s (blocked by %s)\n", blocked, b)
	}
	if len(blockers) > 1 {
		fmt.Fprintf(out, "Added %d block edges on %s.\n", len(blockers), blocked)
	}
	return nil
}

func runBlockRemove(cmd *cobra.Command, blocked string, blockers []string) error {
	db, err := openDBFromCmd()
	if err != nil {
		return err
	}
	defer db.Close()

	actor, err := requireAs(db)
	if err != nil {
		return err
	}

	if err := job.RunUnblockMany(db, blocked, blockers, actor); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, b := range blockers {
		fmt.Fprintf(out, "Unblocked: %s (was blocked by %s)\n", blocked, b)
	}
	if len(blockers) > 1 {
		fmt.Fprintf(out, "Removed %d block edges on %s.\n", len(blockers), blocked)
	}
	return nil
}
