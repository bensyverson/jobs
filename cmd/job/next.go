package main

import (
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newNextCmd() *cobra.Command {
	var format string
	var labelFilter string
	var includeParents bool
	cmd := &cobra.Command{
		Use:   "next [parent] [all]",
		Short: "Show the next available task (or all of them with `all`)",
		Long:  "Show the next available (unblocked, unclaimed, not done) task. By default only leaves (tasks with no open children) are surfaced — tasks with open children are descended through, not returned. Pass --include-parents to surface any available task regardless of whether it has open children. With 'all' (in either position), returns the full claimable frontier instead. Use --label <name> to filter to tasks carrying that label. Without a parent, searches the entire tree.",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			var parentShortID string
			showAll := false
			for _, a := range args {
				if a == "all" {
					showAll = true
				} else {
					parentShortID = a
				}
			}

			// `next` is read-only: resolve the identity softly (like
			// status/orient) so the actor's focus scopes the single-next
			// walk. `all` stays forest-wide — focus narrows the default,
			// never the explicit "show me everything" form.
			actor, _ := job.ResolveIdentity(db, asFlag)

			if showAll {
				tasks, err := job.RunNextAllFiltered(db, parentShortID, actor, labelFilter, includeParents)
				if err != nil {
					return err
				}
				if format == "json" {
					return job.RenderNextAllJSON(cmd.OutOrStdout(), tasks)
				}
				job.RenderNextAllText(cmd.OutOrStdout(), tasks)
				return nil
			}

			task, err := job.RunNextFiltered(db, parentShortID, actor, labelFilter, includeParents)
			if err != nil {
				return err
			}

			if format == "json" {
				job.RenderTaskJSON(cmd.OutOrStdout(), task)
				fmt.Fprintln(cmd.OutOrStdout())
			} else {
				job.RenderNextText(cmd.OutOrStdout(), task)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().StringVarP(&labelFilter, "label", "l", "", "filter to tasks carrying this label")
	cmd.Flags().BoolVar(&includeParents, "include-parents", false, "surface tasks with open children (legacy behavior)")
	return cmd
}
