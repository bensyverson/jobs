package main

import (
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newReopenCmd() *cobra.Command {
	var cascade bool
	var noClaim bool
	cmd := &cobra.Command{
		Use:   "reopen <id>",
		Short: "Reopen a completed or canceled task",
		Long:  "Reopen a completed or canceled task, setting it back to available. Use --cascade to also reopen all done/canceled descendants. By default the task is auto-claimed; use --no-claim to skip.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			actor, err := requireAs(db)
			if err != nil {
				return err
			}

			task, err := job.GetTaskByShortID(db, args[0])
			if err != nil {
				return err
			}
			if task == nil {
				return fmt.Errorf("task %q not found", args[0])
			}
			title := task.Title

			reopened, err := job.RunReopen(db, args[0], cascade, actor)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if len(reopened) > 0 {
				fmt.Fprintf(out, "Reopened: %s %q (and %d subtasks)\n", args[0], title, len(reopened))
			} else {
				fmt.Fprintf(out, "Reopened: %s %q\n", args[0], title)
			}

			if !noClaim && !cascade {
				if err := job.RunClaim(db, args[0], "", "", actor, false); err != nil {
					return err
				}
				durStr := job.FormatDuration(job.DefaultClaimTTLSeconds)
				fmt.Fprintf(out, "  claimed by %s (expires in %s)\n", actor, durStr)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&cascade, "cascade", false, "also reopen all done descendants")
	cmd.Flags().BoolVar(&noClaim, "no-claim", false, "skip auto-claim after reopen")
	return cmd
}
