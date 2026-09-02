package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newHeartbeatCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "heartbeat <id> [<id>...]",
		Short: "Extend your live claim(s) to at least 30 minutes from now",
		Long:  "Refresh one or more live claims held by the caller. Moves claim_expires_at to at least 30 minutes from now — never earlier than it already is, so a longer claim is not shortened — and emits a heartbeat event. All targets must currently be claimed by the caller; any other state errors and rolls back the whole call.",
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

			results, err := job.RunHeartbeat(db, args, actor)
			if err != nil {
				return err
			}

			if format == "json" {
				return job.RenderHeartbeatJSON(cmd.OutOrStdout(), results)
			}
			job.RenderHeartbeatAck(cmd.OutOrStdout(), results)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
