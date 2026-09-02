package main

import (
	"context"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
	"time"
)

func newTailCmd() *cobra.Command {
	var format string
	var eventsFlag string
	var usersFlag string
	var untilClose []string
	var timeoutStr string
	var quiet bool
	cmd := &cobra.Command{
		Use:   "tail [id|all]",
		Short: "Stream events in real-time for a task and its descendants (or all trees)",
		Long:  "Stream events for a task and its descendants. With no positional arg (or the literal 'all'), streams events from every top-level task in the database. Use --until-close <id> (repeatable) to block until each named task reaches done/canceled, then exit 0. --until-close with no value watches the positional task (errors in global scope). --timeout <duration> bounds the wait; on expiry exits 2. --quiet suppresses the streamed event output while preserving close/exit messages.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			shortID := ""
			if len(args) == 1 && args[0] != "all" {
				shortID = args[0]
			}

			if shortID != "" {
				task, err := job.GetTaskByShortID(db, shortID)
				if err != nil {
					return err
				}
				if task == nil {
					return fmt.Errorf("task %q not found", shortID)
				}
			}

			filter := job.EventFilter{
				Types: job.ParseFilterList(eventsFlag),
				Users: job.ParseFilterList(usersFlag),
			}

			// --until-close was passed (flag changed) but only self-sentinel
			// entries, or no value: default to the positional id. In global
			// scope there is no positional id, so the self-sentinel is an
			// error rather than silently producing a watch on "".
			if cmd.Flags().Changed("until-close") {
				cleaned := make([]string, 0, len(untilClose))
				sawSelf := false
				for _, id := range untilClose {
					if id == "" || id == "_" {
						sawSelf = true
						continue
					}
					cleaned = append(cleaned, id)
				}
				if sawSelf || len(cleaned) == 0 {
					if shortID == "" {
						return fmt.Errorf("--until-close requires an id in global scope (no positional task to default to)")
					}
					cleaned = append(cleaned, shortID)
				}
				untilClose = cleaned
			}

			var timeout time.Duration
			if timeoutStr != "" {
				secs, perr := job.ParseDuration(timeoutStr)
				if perr != nil {
					return perr
				}
				timeout = time.Duration(secs) * time.Second
			}

			if len(untilClose) > 0 {
				return job.RunTailUntilClose(
					cmd.Context(), db, shortID, untilClose, timeout,
					job.DefaultTailUntilClosePollInterval,
					quiet, format, filter, cmd.OutOrStdout(),
				)
			}

			if format != "json" {
				scopeLabel := shortID
				if scopeLabel == "" {
					scopeLabel = "all"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Tailing events for %s (Ctrl+C to stop)...\n", scopeLabel)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				<-cmd.Context().Done()
				cancel()
			}()

			names, err := job.LoadReplicaNames(db)
			if err != nil {
				return err
			}

			return job.RunTail(ctx, db, shortID, 1*time.Second, func(events []job.EventEntry) error {
				events = job.FilterEvents(events, filter)
				if len(events) == 0 {
					return nil
				}
				if format == "json" {
					return job.FormatEventLogJSONLines(cmd.OutOrStdout(), events)
				}
				job.RenderEventLogMarkdown(cmd.OutOrStdout(), events, names)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().StringVarP(&eventsFlag, "events", "e", "", "comma-separated list of event types to include (default: all except heartbeat)")
	cmd.Flags().StringVarP(&usersFlag, "users", "u", "", "comma-separated list of actor names to include")
	cmd.Flags().StringSliceVar(&untilClose, "until-close", nil, "block until the named task closes; repeatable; use --until-close=_ to default to the positional id")
	cmd.Flags().Lookup("until-close").NoOptDefVal = "_"
	cmd.Flags().StringVarP(&timeoutStr, "timeout", "t", "", "exit 2 if no close occurs in this duration (e.g. 30s, 5m)")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress event stream while waiting; preserves close and timeout messages")
	return cmd
}
