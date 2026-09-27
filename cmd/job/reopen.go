package main

import (
	"fmt"
	"io"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newReopenCmd() *cobra.Command {
	var cascade bool
	var noClaim bool
	cmd := &cobra.Command{
		Use:   "reopen <id>",
		Short: "Reopen a completed or canceled task",
		Long:  "Reopen a completed or canceled task, setting it back to available. Reopening also undoes what the close did on its own: ancestors the close auto-closed are reopened, and the block edges it removed from still-open dependents are restored. Use --cascade to also reopen all done/canceled descendants. By default the task is auto-claimed; use --no-claim to skip.",
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
			writeReopenAck(out, args[0], title, reopened)

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

// writeReopenAck prints the reopen line, then one line for each ancestor the
// reopen also brought back and each block edge it restored.
func writeReopenAck(out io.Writer, id, title string, res *job.ReopenResult) {
	if len(res.ReopenedChildren) > 0 {
		fmt.Fprintf(out, "Reopened: %s %q (and %d subtasks)\n", id, title, len(res.ReopenedChildren))
	} else {
		fmt.Fprintf(out, "Reopened: %s %q\n", id, title)
	}
	for _, a := range res.ReopenedAncestors {
		fmt.Fprintf(out, "  Auto-reopened: %s %q\n", a.ShortID, a.Title)
	}
	for _, r := range res.RestoredBlocks {
		fmt.Fprintf(out, "  Re-blocked: %s %q (blocked by %s)\n", r.BlockedID, r.BlockedTitle, r.BlockerID)
	}
}
