package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newLabelAddCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "add (<id> <name> [<name>...] | <id> [<id>...] with <name> [<name>...])",
		Short: "Add one or more labels to one or more tasks",
		Long: "Add one or more labels to a task, or with `with`, to several tasks in one atomic call: an unknown id fails the whole call and records nothing. Idempotent: names that are already attached are reported, not re-recorded. Emits one 'labeled' event per task that gains a label.\n\n" +
			"  job label add abc12 p0 infra\n" +
			"  job label add abc12 def34 with reviewed",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := parseLabelArgs("add", args)
			if err != nil {
				return err
			}
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			actor, err := requireAs(db)
			if err != nil {
				return err
			}

			results, err := job.RunLabelAddMany(db, targets.ids, targets.names, actor)
			if err != nil {
				return err
			}

			if format == "json" {
				if targets.many {
					return job.RenderLabelJSONMany(cmd.OutOrStdout(), results)
				}
				return job.RenderLabelJSON(cmd.OutOrStdout(), results[0])
			}
			for _, res := range results {
				job.RenderLabelAck(cmd.OutOrStdout(), res)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
