package main

import (
	"encoding/json"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
	"strings"
)

func newDoneCmd() *cobra.Command {
	var cascade bool
	var note string
	var noteFile string
	var resultStr string
	var format string
	var claimNext bool
	var claimUnder string
	var claimAny bool
	var setCriterion []string
	var quiet bool
	var forceCloseWithPending bool
	var allPassed bool
	var allState string
	cmd := &cobra.Command{
		Use:   "done <id> [<id>...]",
		Short: "Mark one or more tasks as done",
		Long: `Mark one or more tasks as done, atomically. Use --cascade to close a task and all open descendants in one call. Use -m to record a completion note (or -F <path> to read it from a file), and --result for structured JSON output. Idempotent: already-done tasks are reported, not re-recorded.

Tip: pass --claim-next to atomically close this task and claim the next available leaf, collapsing the close-then-advance flow into one call. The ack ends with the same briefing that 'job claim' / 'job show' produces, so you don't need a follow-up 'show' on the new claim.`,
		Args: cobra.MinimumNArgs(1),
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

			// Catch the `done <id> "prose"` footgun early with a specific
			// suggestion, before job.RunDone emits a generic "task not found".
			for i, a := range args {
				if !looksLikeShortID(a) {
					return fmt.Errorf("done: %q does not look like a task ID (5-char alphanumeric). Did you mean `-m %q`? (positional arg #%d)",
						a, a, i+1)
				}
			}

			resolvedNote, _, rerr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:       "done",
				InlineName: "message",
				Inline:     note,
				File:       noteFile,
			})
			if rerr != nil {
				return rerr
			}
			note = resolvedNote

			var resultRaw json.RawMessage
			if resultStr != "" {
				if !json.Valid([]byte(resultStr)) {
					return fmt.Errorf("--result: invalid JSON: %s", resultStr)
				}
				resultRaw = json.RawMessage(resultStr)
			}

			// Apply --criterion updates before close so the soft pending check
			// reflects the operator's just-recorded state changes. Also remember
			// (target, label) pairs the operator marked explicitly so the
			// --all-passed / --all=<state> bulk pass below leaves them alone.
			explicit := make(map[string]map[string]bool)
			for _, kv := range setCriterion {
				label, state, perr := parseCriterionAssignment(kv)
				if perr != nil {
					return perr
				}
				// Apply to the first arg by default. When closing multiple ids,
				// require an explicit "id:label=state" form to disambiguate.
				targetID := args[0]
				labelOnly := label
				if colon := strings.Index(label, ":"); colon > 0 && len(args) > 1 {
					targetID = label[:colon]
					labelOnly = label[colon+1:]
				}
				if _, err := job.RunSetCriterion(db, targetID, labelOnly, state, actor); err != nil {
					return err
				}
				if explicit[targetID] == nil {
					explicit[targetID] = make(map[string]bool)
				}
				explicit[targetID][labelOnly] = true
			}

			// Resolve --all-passed / --all=<state> into a single bulk state.
			// --all-passed is the spelled-out shorthand for the dominant close
			// shape; --all=<state> covers the rarer skipped/failed/passed
			// override.
			bulkCriteriaState := ""
			if allPassed && allState != "" {
				return fmt.Errorf("--all-passed and --all are mutually exclusive")
			}
			if allPassed {
				bulkCriteriaState = string(job.CriterionPassed)
			} else if allState != "" {
				validated, verr := job.ValidateCriterionState(allState)
				if verr != nil {
					return fmt.Errorf("--all: %w", verr)
				}
				if validated == job.CriterionPending {
					return fmt.Errorf("--all: cannot bulk-mark criteria as pending — use --criterion to undo individual rows")
				}
				bulkCriteriaState = string(validated)
			}

			// Apply the bulk state to every still-pending criterion that the
			// operator did not name explicitly. Tracked per-target so the close
			// ack can echo the per-target count back as an audit line.
			bulkTouched := make(map[string]int)
			if bulkCriteriaState != "" {
				for _, id := range args {
					task, terr := job.GetTaskByShortID(db, id)
					if terr != nil {
						return terr
					}
					if task == nil {
						return fmt.Errorf("task %q not found", id)
					}
					criteria, gerr := job.GetCriteria(db, task.ID)
					if gerr != nil {
						return gerr
					}
					for _, c := range criteria {
						if c.State != job.CriterionPending {
							continue
						}
						if explicit[id][c.Label] {
							continue
						}
						if _, err := job.RunSetCriterion(db, id, c.Label, job.CriterionState(bulkCriteriaState), actor); err != nil {
							return err
						}
						bulkTouched[id]++
					}
				}
			}

			// Soft pending warning: count pending criteria across all closed
			// targets in one query and surface a single line so the operator
			// notices but the close still proceeds.
			pendingByID, _ := job.PendingCriteriaByShortID(db, args)

			closed, alreadyDone, err := job.RunDone(db, args, cascade, note, resultRaw, actor, forceCloseWithPending, bulkCriteriaState)
			if err != nil {
				return err
			}

			lastCtxID := args[len(args)-1]

			// Collect all auto-closed ancestor IDs across all closed results so
			// job.ComputeDoneContext can distinguish "already-done parent" from
			// "just-auto-closed parent".
			autoClosedSet := make(map[string]bool)
			for _, c := range closed {
				for _, anc := range c.AutoClosedAncestors {
					autoClosedSet[anc.ShortID] = true
				}
			}

			// Surfaced: is per-closed-task info (Decision 5,
			// project/2026-08-28-issues-ux.md), so every closed task gets its
			// own DoneContext; the trailing Next:/Parent:/whole-tree lines
			// still describe only the last id, matching the existing ack.
			ctxByID := make(map[string]*job.DoneContext, len(closed))
			for _, c := range closed {
				cc, cerr := job.ComputeDoneContext(db, c.ShortID, autoClosedSet)
				if cerr != nil {
					return cerr
				}
				ctxByID[c.ShortID] = cc
			}
			var ctx *job.DoneContext
			if lastCtxID != "" {
				if cc, ok := ctxByID[lastCtxID]; ok {
					ctx = cc
				} else {
					c, cerr := job.ComputeDoneContext(db, lastCtxID, autoClosedSet)
					if cerr != nil {
						return cerr
					}
					ctx = c
				}
			}

			// --claim-next: attempt to claim the next leaf. Done is irreversible,
			// claim is opportunistic — if the leaf got taken between done and
			// claim, we report a status line instead of erroring.
			var claimed *job.Task
			var claimRaceTaken string
			if claimNext && len(closed) > 0 {
				// Scope the follow-on claim. Default: the root subtree of the
				// just-closed task, so a focused session never gets handed an
				// unrelated leaf in a different root. --under <id> overrides the
				// scope explicitly; --any restores the old repo-global behavior.
				// Precedence: --under > --any > default(root-of-closed).
				var t *job.Task
				var cerr error
				switch {
				case claimUnder != "":
					t, cerr = job.RunClaimNextFiltered(db, claimUnder, "", "", actor, false, false, false)
				case claimAny:
					t, cerr = job.RunClaimNextFiltered(db, "", "", "", actor, false, false, false)
				default:
					t, cerr = job.RunClaimNextUnderRootOf(db, lastCtxID, "", actor, false)
				}
				if cerr == nil {
					claimed = t
				} else {
					// Distinguish "no work available" from "someone grabbed it".
					// job.RunClaimNext wraps job.RunNext, which emits "No available tasks."
					// on empty. Other errors (race on claim, task not found) we
					// surface as a race status line with whatever detail we have.
					msg := cerr.Error()
					if !strings.Contains(msg, "No available tasks") {
						claimRaceTaken = msg
					}
				}
			}

			if format == "json" {
				return renderDoneJSON(cmd.OutOrStdout(), closed, alreadyDone, ctxByID, ctx)
			}

			// Idempotent single-ID already-done: preserve Phase 3 wording.
			if len(closed) == 0 && len(alreadyDone) == 1 && len(args) == 1 {
				fmt.Fprintf(cmd.OutOrStdout(), "Already done: %s\n", alreadyDone[0])
				return nil
			}

			// Suppress the ack's Next line when --claim-next successfully
			// claimed the next leaf: the Claimed line below already names
			// the target. Race-lost claims still surface Next as a useful
			// fallback ("you didn't get anything, try this instead").
			renderDoneAckWithOptions(cmd.OutOrStdout(), closed, alreadyDone, ctxByID, ctx, doneAckOptions{
				suppressNext: claimed != nil,
				actor:        actor,
			})

			if claimed != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Claimed: %s %q (expires in %s) as=%s\n",
					claimed.ShortID, claimed.Title, job.FormatDuration(job.DefaultClaimTTLSeconds), actor)
				if !quiet {
					renderClaimBriefing(cmd.OutOrStdout(), db, claimed.ShortID)
				}
			} else if claimRaceTaken != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Next leaf unavailable: %s\n", claimRaceTaken)
			}
			for _, id := range args {
				if n, ok := pendingByID[id]; ok {
					fmt.Fprintf(cmd.OutOrStdout(), "  Note: %s closed with %d pending criteria.\n", id, n)
				}
			}
			// Audit line for the bulk shorthand: report the count actually
			// touched per target, so a `--all-passed` against an 8-criterion
			// task surfaces "Marked 8 criteria passed before closing." while
			// a no-op (everything already marked) stays silent.
			for _, id := range args {
				n := bulkTouched[id]
				if n == 0 {
					continue
				}
				noun := "criteria"
				if n == 1 {
					noun = "criterion"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  Marked %d %s %s before closing.\n", n, noun, bulkCriteriaState)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&cascade, "cascade", false, "close the target and all open descendants")
	cmd.Flags().StringVarP(&note, "message", "m", "", "record a completion note (supports @path and `-` for stdin)")
	registerFileFlag(cmd, &noteFile, "completion note", "message")
	cmd.Flags().StringVar(&resultStr, "result", "", "structured JSON result recorded on the done event")
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	cmd.Flags().BoolVar(&claimNext, "claim-next", false, "after closing, atomically claim the next available leaf in the closed task's root subtree")
	cmd.Flags().StringVar(&claimUnder, "under", "", "with --claim-next: scope the follow-on claim to this subtree instead of the closed task's root")
	cmd.Flags().BoolVar(&claimAny, "any", false, "with --claim-next: claim the next available leaf repo-wide (the old global behavior)")
	cmd.Flags().StringArrayVar(&setCriterion, "criterion", nil, "update an acceptance criterion before close, format \"label=passed\" (repeatable; for multi-id closes use \"id:label=state\")")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress the trailing show briefing on a follow-on --claim-next claim")
	cmd.Flags().BoolVar(&forceCloseWithPending, "force-close-with-pending", false, "close the task even when criteria remain pending; the unmarked labels are recorded as a waiver on the done event")
	cmd.Flags().BoolVar(&allPassed, "all-passed", false, "mark every still-pending criterion as passed before closing (shorthand for --all=passed)")
	cmd.Flags().StringVar(&allState, "all", "", "mark every still-pending criterion with the named state before closing (passed|skipped|failed)")
	return cmd
}
