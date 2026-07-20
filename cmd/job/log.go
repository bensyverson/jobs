package main

import (
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newLogCmd() *cobra.Command {
	var format string
	var since string
	var actor string
	cmd := &cobra.Command{
		Use:   "log [id|all]",
		Short: "Show event history for a task and its descendants (or all trees)",
		Long:  "Show event history for a task and its descendants. With no positional arg (or the literal 'all'), streams events from every top-level task in the database. Filters (--since, --actor) compose with the chosen scope.\n\n--since accepts either an RFC3339 timestamp (`2026-04-28T10:00:00Z`) or a relative duration (`5m`, `2h`, `1d`). Use --actor to scope to events emitted by a specific identity (e.g. \"what did alice just close?\").",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			sincePtr, err := job.ParseSince(since)
			if err != nil {
				return err
			}

			shortID := ""
			if len(args) == 1 && args[0] != "all" {
				shortID = args[0]
			}

			events, err := job.RunLogFiltered(db, shortID, sincePtr, actor)
			if err != nil {
				return err
			}

			if format == "json" {
				b, err := job.FormatEventLogJSON(events)
				if err != nil {
					return err
				}
				cmd.OutOrStdout().Write(b)
				fmt.Fprintln(cmd.OutOrStdout())
			} else {
				job.RenderEventLogMarkdown(cmd.OutOrStdout(), events)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().StringVarP(&since, "since", "s", "", "only events at or after this RFC3339 timestamp or relative duration (e.g. 5m, 2h)")
	cmd.Flags().StringVar(&actor, "actor", "", "only events emitted by this actor (named differently from the global --as flag, which is the writer identity)")
	return cmd
}
