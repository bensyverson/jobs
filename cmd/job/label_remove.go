package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newLabelRemoveCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "remove (<id> <name> [<name>...] | <id> [<id>...] with <name> [<name>...])",
		Short: "Remove one or more labels from one or more tasks",
		Long: "Remove one or more labels from a task, or with `with`, from several tasks in one atomic call: an unknown id fails the whole call and records nothing. Idempotent: names that are not present are reported, not re-recorded. Emits one 'unlabeled' event per task that loses a label.\n\n" +
			"  job label remove abc12 infra\n" +
			"  job label remove abc12 def34 with reviewed",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := parseLabelArgs("remove", args)
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

			results, err := job.RunLabelRemoveMany(db, targets.ids, targets.names, actor)
			if err != nil {
				return err
			}

			if format == "json" {
				if targets.many {
					return job.RenderUnlabelJSONMany(cmd.OutOrStdout(), results)
				}
				return job.RenderUnlabelJSON(cmd.OutOrStdout(), results[0])
			}
			for _, res := range results {
				job.RenderUnlabelAck(cmd.OutOrStdout(), res)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
