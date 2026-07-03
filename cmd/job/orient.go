package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newOrientCmd() *cobra.Command {
	var format string
	var scope string
	var full bool
	cmd := &cobra.Command{
		Use:   "orient [id]",
		Short: "Regenerate the plan tree around a target leaf for a fresh agent",
		Long: "Orient a fresh agent on a single leaf: emit the target's whole root tree, substantive notes " +
			"folded onto live nodes, criteria as a checklist, and a synthesized `orient:` header (target, " +
			"what it blocks, criteria tally, own_notes, weigh_notes).\n\n" +
			"The default output is the context-budget view: done tasks are reduced to title/id/status/closed " +
			"(done containers keep their desc — the plan narrative), their notes and criteria are elided, and " +
			"a single completion_note breadcrumb marks the most recent note-bearing close. Use `job show <id>` " +
			"for any elided history, or --full for the unelided view.\n\n" +
			"This is the worker's session-opener, complementing `job status` (the orchestrator's). With no id, " +
			"the target is the next available leaf; pass an id to target a specific task. The rendered tree " +
			"defaults to the target's whole root tree; --scope <id> limits it to a subtree.",
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

			view, err := job.RunOrientOpts(db, target, scope, actor, full)
			if err != nil {
				return err
			}
			return job.RenderOrientYAML(cmd.OutOrStdout(), view)
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "output format (yaml; md planned)")
	cmd.Flags().StringVar(&scope, "scope", "", "limit the rendered tree to this subtree")
	cmd.Flags().BoolVar(&full, "full", false, "keep desc, notes, and criteria on done tasks (unelided view)")
	return cmd
}
