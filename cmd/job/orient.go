package main

import (
	"errors"
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newOrientCmd() *cobra.Command {
	var format string
	var scope string
	var full bool
	var issues bool
	cmd := &cobra.Command{
		Use:   "orient [id]",
		Short: "Regenerate the plan tree around a target leaf for a fresh agent",
		Long: "Orient a fresh agent on a single leaf. Output is in reading order, so any prefix of it carries " +
			"the most useful context available at that length: the `orient:` header holds the target in full " +
			"(desc, criteria with state, every note), then its blockedBy and blocks (each with status and " +
			"latest note), then one line per sibling leaf (id, title, status, note count); the target's whole " +
			"root tree follows. An available task with an open blocker renders `status: blocked` and names " +
			"its blockers.\n\n" +
			"The default output is the context-budget view: an open task prints only its latest note, with " +
			"`+N earlier: job show <id>` when it has more; a closed (done or canceled) leaf is one line — " +
			"title, id, close date; a closed container whose whole subtree is closed keeps its desc and a " +
			"done-leaf count and no leaf rows; and a single completion_note breadcrumb marks the most recent " +
			"note-bearing close. Use `job show <id>` for any elided history, or --full for every node and " +
			"every note.\n\n" +
			"This is the worker's session-opener, complementing `job status` (the orchestrator's). With no id, " +
			"the target is the next available leaf in a task-tree; issue-trees are skipped unless --issues is " +
			"passed (see `job kind`). Pass an id to target a specific task — an explicit id always wins. The " +
			"rendered tree defaults to the target's whole root tree; --scope <id> limits it to a subtree.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch format {
			case "", "yaml":
				// default renderer
			case "md":
				return fmt.Errorf("--format md is not yet implemented (planned); use --format yaml (the default)")
			default:
				return fmt.Errorf("unknown --format %q (want yaml|md)", format)
			}

			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			var target string
			if len(args) == 1 {
				target = args[0]
			}

			// `orient` is read-only: resolve the writer identity softly so
			// next-leaf claim-expiry is attributed when an identity exists,
			// but never require --as the way write verbs do.
			actor, _ := job.ResolveIdentity(db, asFlag)

			view, err := job.RunOrientOpts(db, target, scope, actor, full, issues)
			if err != nil {
				// Orienting is a read; an empty tree (or an exhausted
				// focused root) is a valid answer, not a broken tool. next
				// and claim --next keep failing loudly for this same
				// condition — there the caller asked for a task and did
				// not get one.
				if errors.Is(err, job.ErrNoAvailableTasks) {
					noTasks, nerr := job.RunOrientNoTasks(db, actor, err.Error(), full, issues)
					if nerr != nil {
						return nerr
					}
					return job.RenderOrientNoTasksYAML(cmd.OutOrStdout(), noTasks)
				}
				return err
			}
			return job.RenderOrientYAML(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "output format (yaml; md planned)")
	cmd.Flags().StringVar(&scope, "scope", "", "limit the rendered tree to this subtree")
	cmd.Flags().BoolVar(&full, "full", false, "render every node in full with every note (unelided view)")
	cmd.Flags().BoolVar(&issues, "issues", false, "target the next available leaf in an issue-tree (see `job kind`)")
	return cmd
}
