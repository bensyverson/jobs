package main

import (
	"database/sql"
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newClaimCmd() *cobra.Command {
	var force bool
	var quiet bool
	var next bool
	var includeParents bool
	var issues bool
	var format string
	var note string
	var noteFile string
	cmd := &cobra.Command{
		Use:   "claim <id> [duration]",
		Short: "Claim a task (duration optional, default 30m)",
		Long: `Claim a task, marking it as in-progress. Duration defaults to 30m. Supported units: s, m, h, d. Use --force to override an existing claim.

The ack's first line is the scriptable signal ("Claimed: <id> '<title>' (expires in <dur>) as=<actor>") followed by the full 'show <id>' briefing — claiming is the moment you want every detail you'd otherwise have to fetch with a follow-up 'show'.

Long-running claim example: ` + "`job claim abc12 2h`" + ` extends the default 30m TTL to 2 hours for work that genuinely needs the lock held longer than a typical session.

Tip: use 'job claim --next [parent] [duration]' to find and claim the next available leaf in one step, and 'job done <id> --claim-next' for the close-and-advance loop ('done <id>' followed atomically by claiming the next leaf).`,
		Args: cobra.RangeArgs(0, 2),
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

			// The body is resolved before the --next branch: `claim --next -m`
			// records its note on whichever leaf is picked, exactly as
			// `claim <id> -m` does, and a malformed body must abort before
			// anything is claimed.
			resolved, _, rerr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:       "claim",
				InlineName: "message",
				Inline:     note,
				File:       noteFile,
			})
			if rerr != nil {
				return rerr
			}

			if next {
				return runClaimNext(cmd, db, args, resolved, actor, force, includeParents, issues, quiet, format)
			}

			if len(args) < 1 {
				return fmt.Errorf("claim requires <id>, or pass --next [parent] to pick the next available leaf")
			}

			shortID := args[0]
			var duration string
			if len(args) >= 2 {
				duration = args[1]
			}

			pre, _ := job.GetTaskByShortID(db, shortID)
			prevClaimedBy := ""
			if force && pre != nil && pre.Status == "claimed" && pre.ClaimedBy != nil {
				prevClaimedBy = *pre.ClaimedBy
			}

			if err := job.RunClaim(db, shortID, duration, resolved, actor, force); err != nil {
				return err
			}

			title := ""
			if pre != nil {
				title = pre.Title
			}

			durStr := job.FormatDuration(job.DefaultClaimTTLSeconds)
			if duration != "" {
				durStr = duration
			}

			if force && prevClaimedBy != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Claimed: %s %q (overrode previous claim by %s, expires in %s) as=%s\n", shortID, title, prevClaimedBy, durStr, actor)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Claimed: %s %q (expires in %s) as=%s\n", shortID, title, durStr, actor)
			}

			if pre != nil {
				if hint := autoCloseCascadeHint(db, pre); hint != "" {
					fmt.Fprintln(cmd.OutOrStdout(), hint)
				}
			}

			if !quiet {
				renderClaimBriefing(cmd.OutOrStdout(), db, shortID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "override an existing claim")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress the trailing show briefing — keep only the one-line confirm")
	cmd.Flags().BoolVar(&next, "next", false, "find and claim the next available leaf (replaces standalone `claim-next`)")
	cmd.Flags().BoolVar(&includeParents, "include-parents", false, "with --next: permit claiming tasks with open children")
	cmd.Flags().BoolVar(&issues, "issues", false, "with --next: claim from the issue-trees instead of the task-trees (see `job kind`)")
	cmd.Flags().StringVar(&format, "format", "md", "with --next: output format (md|json)")
	cmd.Flags().StringVarP(&note, "message", "m", "", "record a starting note before claiming (supports @path and `-` for stdin)")
	registerFileFlag(cmd, &noteFile, "starting note", "message")
	return cmd
}

// runClaimNext is the body of `claim --next [parent] [duration]`.
func runClaimNext(cmd *cobra.Command, db *sql.DB, args []string, note, actor string, force, includeParents, issues, quiet bool, format string) error {
	parentShortID, duration := job.ParseNextParentAndDuration(args)
	task, err := job.RunClaimNextFiltered(db, parentShortID, duration, note, actor, force, includeParents, issues)
	if err != nil {
		return err
	}
	if format == "json" {
		job.RenderTaskJSON(cmd.OutOrStdout(), task)
		fmt.Fprintln(cmd.OutOrStdout())
		return nil
	}
	durStr := job.FormatDuration(job.DefaultClaimTTLSeconds)
	if duration != "" {
		durStr = duration
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Claimed: %s %q (expires in %s) as=%s\n", task.ShortID, task.Title, durStr, actor)
	if !quiet {
		renderClaimBriefing(cmd.OutOrStdout(), db, task.ShortID)
	}
	return nil
}

// autoCloseCascadeHint returns a one-line hint when claiming `task` would,
// on close, cascade-close the parent (because `task` is the last
// not-yet-done child of its parent).
func autoCloseCascadeHint(db *sql.DB, task *job.Task) string {
	if task.ParentID == nil {
		return ""
	}
	parent, err := job.GetTaskByID(db, *task.ParentID)
	if err != nil || parent == nil {
		return ""
	}
	openCount, _, err := job.CountOpenChildrenOfShortID(db, parent.ShortID)
	if err != nil {
		return ""
	}
	// `task` is itself one of the open children (it's about to be claimed,
	// not yet closed); so "1" means it's the last one standing.
	if openCount != 1 {
		return ""
	}
	return fmt.Sprintf("  Closing this task will auto-close parent %s. Verify scope first.", parent.ShortID)
}
