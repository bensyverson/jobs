package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var format string
	var usage bool
	var since string
	cmd := &cobra.Command{
		Use:     "status [id]",
		Aliases: []string{"summary"},
		Short:   "Show a session preamble and work landscape, or a single subtree",
		Long:    "Without an argument, prints a session preamble (claimed/open/done counts, time since last event, identity) followed by the forest-level rollup — one row per top-level task with its own subtree counts. With an id, scopes the renderer to the subtree rooted at that task and skips the session preamble (the preamble is DB-wide metadata and doesn't belong on a subtree view). Pass --format=json for the same data in a machine-parsable shape. `job summary [id]` is a deprecated alias and emits a stderr notice on every call. No --as required.\n\nWith --usage, prints an activity report instead of the briefing + rollup: status counts (open/claimed/done/canceled/blocked, zero-counts omitted in md), completion & cancellation rates, event count, first/last event timestamps, velocity (done events / calendar days), and DB file size. All-time by default; --since <RFC3339|duration> (same grammar as `job log`) scopes to a window. Positional [id] scopes to a subtree. --since without --usage is a no-op and errors.",
		Args:    cobra.MaximumNArgs(1),
		PreRun: func(cmd *cobra.Command, args []string) {
			if cmd.CalledAs() == "summary" {
				fmt.Fprintln(cmd.ErrOrStderr(), "note: `job summary` is a deprecated alias for `job status`; prefer the canonical form.")
			}
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "md" && format != "json" {
				return fmt.Errorf("status: --format must be one of md, json (got %q)", format)
			}

			sincePtr, err := job.ParseSince(since)
			if err != nil {
				return err
			}
			if sincePtr != nil && !usage {
				return fmt.Errorf("--since requires --usage on `job status`; --since is otherwise a no-op here")
			}

			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			if usage {
				return runUsage(cmd, args, db, sincePtr, format)
			}

			out := cmd.OutOrStdout()

			var target *job.Task
			if len(args) == 1 {
				target, err = job.GetTaskByShortID(db, args[0])
				if err != nil {
					return err
				}
				if target == nil {
					return fmt.Errorf("task %q not found", args[0])
				}
			}

			// Snapshot stale claims BEFORE running anything that
			// auto-expires them (RunStatus below, plus any downstream
			// code paths). Without this, RunStatus clears stale claims
			// as a side effect and there's nothing left to surface.
			var scopeID *int64
			if target != nil {
				scopeID = &target.ID
			}
			stales, err := job.FindStaleClaims(db, scopeID)
			if err != nil {
				return err
			}

			var decisions []*job.Task

			if target != nil {
				rollup, err := job.BuildRollup(db, target, "")
				if err != nil {
					return err
				}
				decisions = rollup.DecisionTasks

				if format == "json" {
					return renderStatusSubtreeJSON(out, target, rollup, stales, decisions)
				}

				job.RenderSummary(out, rollup)

				nodes, err := job.RunListFiltered(db, job.ListFilter{ParentID: target.ShortID})
				if err != nil {
					return err
				}
				if len(nodes) > 0 {
					fmt.Fprintln(out)
					blockers, err := job.CollectBlockers(db, nodes)
					if err != nil {
						return err
					}
					labels, err := collectLabels(db, nodes)
					if err != nil {
						return err
					}
					job.RenderMarkdownList(out, nodes, blockers, labels, 0)
				}
			} else {
				s, err := job.RunStatus(db, asFlag)
				if err != nil {
					return err
				}
				// Forest scope is focus-aware: resolve the identity softly
				// (status is read-only, never requires --as) so the rollup
				// can scope Next to the actor's focused root.
				actor, _ := job.ResolveIdentity(db, asFlag)
				rollup, err := job.BuildRollup(db, nil, actor)
				if err != nil {
					return err
				}
				decisions = rollup.DecisionTasks

				if format == "json" {
					return renderStatusForestJSON(out, s, rollup, stales, decisions)
				}

				job.RenderStatus(out, s)
				if len(rollup.DirectChildren) > 0 {
					fmt.Fprintln(out)
					job.RenderSummary(out, rollup)
				}
			}

			if len(stales) > 0 {
				fmt.Fprintln(out)
				job.RenderStaleClaims(out, stales)
			}
			renderDecisionTasks(out, decisions)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().BoolVar(&usage, "usage", false, "print the activity report instead of the briefing + rollup")
	cmd.Flags().StringVarP(&since, "since", "s", "", "with --usage: report window (RFC3339 timestamp or relative duration, e.g. 5m, 2h, 7d)")
	return cmd
}

func renderDecisionTasks(w io.Writer, tasks []*job.Task) {
	for _, d := range tasks {
		fmt.Fprintf(w, "Decision: %s %q\n", d.ShortID, d.Title)
	}
}

// renderStatusForestJSON emits the forest-scope (no-id) JSON shape:
// identity + counts + last_activity_unix preamble, roots rollup, next
// claimable leaf, stale claims, and decision tasks.
func renderStatusForestJSON(w io.Writer, s *job.StatusSummary, rollup *job.Summary, stales []job.StaleClaim, decisions []*job.Task) error {
	payload := map[string]any{
		"identity": map[string]any{
			"default": s.IdentityDefault,
			"strict":  s.Strict,
		},
		"counts": map[string]any{
			"open":     s.Open,
			"claimed":  s.Claimed,
			"done":     s.Done,
			"canceled": s.Canceled,
		},
		"last_activity_unix": s.LastActivity,
		"roots":              rollupRowsJSON(rollup.DirectChildren),
		"next":               nextJSON(rollup.Next),
		"focus":              nextJSON(rollup.Focus),
		"stale":              staleJSON(stales),
		"decisions":          decisionsJSON(decisions),
	}
	return writeJSON(w, payload)
}

// renderStatusSubtreeJSON emits the subtree-scope JSON shape: the same
// fields the human form prints when scoped to a single task, dropping
// the DB-wide preamble (identity + counts).
func renderStatusSubtreeJSON(w io.Writer, target *job.Task, rollup *job.Summary, stales []job.StaleClaim, decisions []*job.Task) error {
	payload := map[string]any{
		"target":    rollupRowJSON(rollup.Target),
		"children":  rollupRowsJSON(rollup.DirectChildren),
		"next":      nextJSON(rollup.Next),
		"stale":     staleJSON(stales),
		"decisions": decisionsJSON(decisions),
	}
	return writeJSON(w, payload)
}

func rollupRowsJSON(rows []*job.SubtreeRollup) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, rollupRowJSON(r))
	}
	return out
}

func rollupRowJSON(r *job.SubtreeRollup) map[string]any {
	if r == nil {
		return nil
	}
	row := map[string]any{
		"short_id":   r.ShortID,
		"title":      r.Title,
		"status":     r.Status,
		"open":       r.Open,
		"done":       r.Done,
		"blocked":    r.Blocked,
		"available":  r.Available,
		"in_flight":  r.InFlight,
		"canceled":   r.Canceled,
		"is_blocked": r.IsBlocked,
		"has_kids":   r.HasKids,
	}
	if r.NextID != "" {
		row["next"] = r.NextID
	}
	if r.ClosedAt > 0 {
		row["closed_at_unix"] = r.ClosedAt
	}
	return row
}

func nextJSON(t *job.Task) map[string]any {
	if t == nil {
		return nil
	}
	return map[string]any{
		"short_id": t.ShortID,
		"title":    t.Title,
	}
}

func staleJSON(claims []job.StaleClaim) []map[string]any {
	out := make([]map[string]any, 0, len(claims))
	for _, c := range claims {
		out = append(out, map[string]any{
			"short_id":      c.ShortID,
			"title":         c.Title,
			"claimed_by":    c.ClaimedBy,
			"expired_at":    c.ExpiredAt,
			"seconds_stale": c.SecondsStale,
		})
	}
	return out
}

func decisionsJSON(tasks []*job.Task) []map[string]any {
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{
			"short_id": t.ShortID,
			"title":    t.Title,
		})
	}
	return out
}

func writeJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// runUsage renders the --usage activity report. Resolves a positional
// [id] to a subtree scope (mirroring `job status [id]` and `job log
// [id]`), then dispatches to the md or json renderer.
func runUsage(cmd *cobra.Command, args []string, db *sql.DB, sincePtr *int64, format string) error {
	var scopeID *int64
	if len(args) == 1 {
		target, err := job.GetTaskByShortID(db, args[0])
		if err != nil {
			return err
		}
		if target == nil {
			return fmt.Errorf("task %q not found", args[0])
		}
		scopeID = &target.ID
	}

	u, err := job.RunUsage(db, scopeID, sincePtr)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if format == "json" {
		b, err := job.MarshalUsageJSON(u)
		if err != nil {
			return err
		}
		_, _ = out.Write(b)
		fmt.Fprintln(out)
		return nil
	}
	job.RenderUsage(out, u)
	return nil
}
