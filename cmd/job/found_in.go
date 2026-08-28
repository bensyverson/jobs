package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

// newFoundInCmd builds the `found-in` verb: a provenance cross-reference
// recording the leaf that surfaced a task. It is deliberately *not* a
// blocking edge — see `job block` for that — so nothing about either task's
// claimability or close behaviour changes.
func newFoundInCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "found-in <task> in <source>",
		Short: "Record the task that surfaced this one, or clear the reference",
		Long: "Record where a task was found — typically the leaf being worked when a defect turned up — " +
			"without parenting it there and without creating a blocking relationship. One source per task; " +
			"setting a new one replaces the old. The reference survives the source being done, canceled, " +
			"canceled by cascade, or deleted. Use --clear to remove it.",
		Args: func(cmd *cobra.Command, args []string) error {
			if clear {
				if len(args) != 1 {
					return fmt.Errorf("found-in: --clear takes the task id alone; drop `in <source>`")
				}
				return nil
			}
			if len(args) != 3 || args[1] != "in" {
				return fmt.Errorf("usage: job found-in <task> in <source>  (or: job found-in <task> --clear)")
			}
			return nil
		},
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

			out := cmd.OutOrStdout()
			if clear {
				if err := job.RunClearFoundIn(db, args[0], actor); err != nil {
					return err
				}
				fmt.Fprintf(out, "Cleared: %s (no longer records where it was found)\n", args[0])
				return nil
			}

			previous, err := job.GetFoundInSource(db, args[0])
			if err != nil {
				return err
			}
			if err := job.RunSetFoundIn(db, args[0], args[2], actor); err != nil {
				return err
			}
			if previous != nil && previous.ShortID != args[2] {
				fmt.Fprintf(out, "Found in: %s was found in %s (replaces %s)\n", args[0], args[2], previous.ShortID)
			} else {
				fmt.Fprintf(out, "Found in: %s was found in %s\n", args[0], args[2])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the task's found-in reference")
	return cmd
}
