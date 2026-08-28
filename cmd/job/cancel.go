package main

import (
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newCancelCmd() *cobra.Command {
	var reason string
	var reasonFile string
	var cascade bool
	var purge bool
	var yes bool
	var format string
	cmd := &cobra.Command{
		Use:   "cancel <id> [<id>...]",
		Short: "Non-destructively stop work on one or more tasks",
		Long:  "Mark one or more tasks as canceled, atomically. --reason is required; -F <path> reads it from a file. --cascade also cancels open descendants. --purge erases the task and its events instead of transitioning state; --purge --cascade requires --yes.",
		Args:  cobra.MinimumNArgs(1),
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

			resolvedReason, _, rerr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:       "cancel",
				InlineName: "reason",
				Inline:     reason,
				File:       reasonFile,
			})
			if rerr != nil {
				return rerr
			}
			reason = resolvedReason

			canceled, alreadyCanceled, purged, err := job.RunCancel(db, args, reason, cascade, purge, yes, actor)
			if err != nil {
				return err
			}

			if format == "json" {
				return job.RenderCancelJSON(cmd.OutOrStdout(), canceled, alreadyCanceled, purged, reason)
			}

			if purge {
				job.RenderPurgeAck(cmd.OutOrStdout(), purged, reason)
				return nil
			}

			if len(canceled) == 0 && len(alreadyCanceled) == 1 && len(args) == 1 {
				fmt.Fprintf(cmd.OutOrStdout(), "Already canceled: %s\n", alreadyCanceled[0])
				return nil
			}

			job.RenderCancelAckAs(cmd.OutOrStdout(), canceled, alreadyCanceled, reason, actor)
			return nil
		},
	}
	cmd.Flags().StringVarP(&reason, "reason", "m", "", "human-readable reason, required (supports @path and `-` for stdin)")
	registerFileFlag(cmd, &reasonFile, "reason", "reason")
	cmd.Flags().BoolVar(&cascade, "cascade", false, "also cancel/purge open descendants")
	cmd.Flags().BoolVar(&purge, "purge", false, "erase the task row and its events instead of transitioning state")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm irrecoverable purge of a subtree (required with --purge --cascade)")
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
