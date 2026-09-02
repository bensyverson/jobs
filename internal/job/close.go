package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Closing and reopening work: `done`, `cancel`'s shared leaf-frontier
// cascade, the strict-criteria gate, and `reopen`.
//
// Every state change here is an event. A cascade is not derived at apply
// time — the handler decides which descendants and which ancestors close,
// and emits a done or canceled event for each, so a replay on another
// machine reproduces exactly the same rows
// (project/2026-09-01-git-native-event-log.md, "Apply never derives").

type ClosedResult struct {
	ShortID             string
	Title               string
	Note                string
	CascadeClosed       []string
	AutoClosedAncestors []AutoClosedAncestor
}

// AutoClosedAncestor names an ancestor that was auto-closed by the
// leaf-frontier cascade (when its last open child closed). Walking from
// the closer upward; the first entry is the direct parent. Status is
// "done" or "canceled" — the destination the cascade chose for this
// ancestor based on its sibling mix.
type AutoClosedAncestor struct {
	ShortID string
	Title   string
	Status  string
}

// cascadeAutoCloseAncestors walks the ancestor chain from taskID upward,
// auto-closing each ancestor whose open children have all been closed
// (status is now "done" or "canceled"). Destination per ancestor is
// status-aware: if any sibling closed as "done", the ancestor cascades
// to "done"; if every sibling is "canceled", the ancestor cascades to
// "canceled". triggerKind labels the event ("done" or "cancel") so the
// log can distinguish the two cascade flavours. The cascade never closes an
// issue-tree root — it stops there instead, leaving the root open — since an
// issue tree is open-ended by design. Returns the ordered list of
// auto-closed ancestors, nearest-parent first.
func cascadeAutoCloseAncestors(tx dbtx, b *eventBatch, taskID int64, triggerShortID, triggerKind, actor string) ([]AutoClosedAncestor, error) {
	var result []AutoClosedAncestor
	cursorID := taskID
	for {
		var parentID *int64
		if err := tx.QueryRow(
			"SELECT parent_id FROM tasks WHERE id = ?", cursorID,
		).Scan(&parentID); err != nil {
			return nil, err
		}
		if parentID == nil {
			return result, nil
		}

		row := tx.QueryRow(`
			SELECT id, short_id, parent_id, title, description, status, sort_key,
			       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
			FROM tasks WHERE id = ?`, *parentID)
		p, err := scanTask(row)
		if err != nil {
			return nil, err
		}
		// If the parent is already done/canceled, stop the cascade — nothing
		// to do, and we shouldn't walk past it.
		if p.Status == "done" || p.Status == "canceled" {
			return result, nil
		}

		open, err := countOpenChildren(tx, p.ID)
		if err != nil {
			return nil, err
		}
		if open > 0 {
			return result, nil
		}

		// An issue-tree root is open-ended by design (see tree-kinds docs):
		// its lifetime is not bounded by the plan that surfaced it. The
		// cascade stops here without closing it — a bug filed under it later
		// must land on a still-open root. Intermediate parents inside an
		// issue tree (an issue with its own task children) are not roots and
		// still auto-close normally; only the root itself is exempt. An
		// explicit `job done`/`job cancel` on the root is unaffected — that
		// path never goes through this cascade.
		if p.ParentID == nil && p.Kind.IsIssue() {
			return result, nil
		}

		// Destination: any done sibling → "done"; otherwise "canceled".
		destination := "canceled"
		var doneSiblings int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM tasks WHERE parent_id = ? AND status = 'done' AND deleted_at IS NULL",
			p.ID,
		).Scan(&doneSiblings); err != nil {
			return nil, err
		}
		if doneSiblings > 0 {
			destination = "done"
		}

		wasStatus := p.Status
		// Event type mirrors destination so audit logs show the same verb
		// the status landed on. detail carries both trigger_kind and
		// cascade_status so consumers can tell apart done-triggered vs
		// cancel-triggered cascades without inspecting sibling history.
		var wasClaimedBy string
		var wasExpiresAt int64
		// Auto-closed parents are typically not claimed (adding any open
		// child auto-releases a parent's claim), but record the
		// breadcrumbs anyway in case some path skipped that release.
		if wasStatus == "claimed" {
			if p.ClaimedBy != nil {
				wasClaimedBy = *p.ClaimedBy
			}
			if p.ClaimExpiresAt != nil {
				wasExpiresAt = *p.ClaimExpiresAt
			}
		}
		if destination == "done" {
			if err := b.emit(tx, EventDone, p.ShortID, actor, DonePayload{
				AutoClosed:    true,
				TriggerKind:   triggerKind,
				TriggeredBy:   triggerShortID,
				CascadeStatus: destination,
				WasStatus:     wasStatus,
				WasClaimedBy:  wasClaimedBy,
				WasExpiresAt:  wasExpiresAt,
			}); err != nil {
				return nil, err
			}
			if err := recordBlocksUnblockedOn(tx, p.ID, p.ShortID, actor); err != nil {
				return nil, err
			}
		} else {
			if err := b.emit(tx, EventCanceled, p.ShortID, actor, CanceledPayload{
				AutoClosed:    true,
				TriggerKind:   triggerKind,
				TriggeredBy:   triggerShortID,
				CascadeStatus: destination,
				WasStatus:     wasStatus,
				WasClaimedBy:  wasClaimedBy,
				WasExpiresAt:  wasExpiresAt,
			}); err != nil {
				return nil, err
			}
			if err := recordBlocksUnblockedOnCancel(tx, p.ID, p.ShortID, actor); err != nil {
				return nil, err
			}
		}

		result = append(result, AutoClosedAncestor{ShortID: p.ShortID, Title: p.Title, Status: destination})
		cursorID = p.ID
	}
}

// strictCloseTarget is the minimal pair the strict-close error formatter
// needs from each target — the operator-facing short ID plus the title used
// in the per-task header line.
type strictCloseTarget struct {
	ShortID string
	Title   string
}

// formatStrictCloseError builds the multi-line refusal returned by the
// strict-default close gate. The leading line uses a stable "cannot close:
// N pending criteria" prefix so retry-with-override automation can grep for
// it; the body lists each unmarked criterion under its task header so the
// override is informed; the trailing line names the override flag.
func formatStrictCloseError(targets []strictCloseTarget, pending map[string][]string) error {
	total := 0
	for _, labels := range pending {
		total += len(labels)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cannot close: %d pending criteria", total)
	if len(pending) > 1 {
		fmt.Fprintf(&b, " across %d tasks", len(pending))
	}
	for _, tgt := range targets {
		labels := pending[tgt.ShortID]
		if len(labels) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n  %s %q:", tgt.ShortID, tgt.Title)
		for _, l := range labels {
			fmt.Fprintf(&b, "\n    [ ] %s", l)
		}
	}
	b.WriteString("\nOverride: --force-close-with-pending")
	return fmt.Errorf("%s", b.String())
}

// RunDone closes one or more tasks atomically. If cascade is true, each target
// expands to include all open descendants. If a target has unmarked pending
// criteria and cascade is false, RunDone refuses with a "cannot close: N
// pending criteria" error unless forceCloseWithPending is true; in that case
// the unmarked labels are persisted on the done event under "criteria_waived"
// so a reviewer can see what was deferred. When bulkCriteriaState is non-
// empty, it is recorded on the done event under "criteria_bulk_state" so the
// close shape (e.g. "all marked passed via --all-passed") survives in the
// event log. Returns per-target results, a list of already-done targets that
// were skipped, or an error (all-or-nothing).
func RunDone(db *sql.DB, ids []string, cascade bool, note string, result json.RawMessage, actor string, forceCloseWithPending bool, bulkCriteriaState string) (closed []*ClosedResult, alreadyDone []string, err error) {
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("done requires at least one task id")
	}

	err = commit(db, func(tx dbtx, b *eventBatch) error {
		closed, alreadyDone = nil, nil
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}

		// Phase A: validate every id and resolve to a task, partition already-done.
		type target struct {
			shortID string
			task    *Task
		}
		var targets []target
		seenExplicit := make(map[int64]bool)
		for _, id := range ids {
			if err := checkClaimOwnership(tx, id, actor); err != nil {
				return err
			}
			t, err := GetTaskByShortID(tx, id)
			if err != nil {
				return err
			}
			if t == nil {
				return fmt.Errorf("task %q not found", id)
			}
			if t.Status == "done" {
				alreadyDone = append(alreadyDone, id)
				continue
			}
			if seenExplicit[t.ID] {
				continue
			}
			seenExplicit[t.ID] = true
			targets = append(targets, target{shortID: id, task: t})
		}

		// Phase A.2: for each target, validate or expand via cascade.
		type plan struct {
			target        target
			cascadeTasks  []*Task
			cascadeShorts []string
		}
		var plans []plan
		seenCascade := make(map[int64]bool)
		for _, tgt := range targets {
			incomplete, err := findIncompleteDescendants(tx, tgt.task.ID)
			if err != nil {
				return err
			}
			if len(incomplete) > 0 && !cascade {
				var descs []string
				for _, t := range incomplete {
					descs = append(descs, fmt.Sprintf("%s (%s)", t.ShortID, t.Title))
				}
				return fmt.Errorf("task %s has incomplete subtasks: %s (run 'job done --cascade %s' to close all).",
					tgt.shortID, strings.Join(descs, ", "), tgt.shortID)
			}
			var cTasks []*Task
			var cShorts []string
			if cascade {
				for _, d := range incomplete {
					if seenExplicit[d.ID] || seenCascade[d.ID] {
						continue
					}
					seenCascade[d.ID] = true
					cTasks = append(cTasks, d)
					cShorts = append(cShorts, d.ShortID)
				}
			}
			plans = append(plans, plan{target: tgt, cascadeTasks: cTasks, cascadeShorts: cShorts})
		}

		// Strict-default close gate: refuse if a target has pending criteria,
		// unless --cascade is set ("children own the criteria") or the operator
		// has opted in to the override. Override callers carry the unmarked
		// labels through to the done-event detail as a recorded waiver.
		pendingByTarget := make(map[string][]string, len(targets))
		if !cascade {
			for _, tgt := range targets {
				cs, err := GetCriteria(tx, tgt.task.ID)
				if err != nil {
					return err
				}
				for _, c := range cs {
					if c.State == CriterionPending {
						pendingByTarget[tgt.shortID] = append(pendingByTarget[tgt.shortID], c.Label)
					}
				}
			}
			if len(pendingByTarget) > 0 && !forceCloseWithPending {
				ordered := make([]strictCloseTarget, 0, len(targets))
				for _, tgt := range targets {
					ordered = append(ordered, strictCloseTarget{ShortID: tgt.shortID, Title: tgt.task.Title})
				}
				return formatStrictCloseError(ordered, pendingByTarget)
			}
		}

		var resultVal any
		if len(result) > 0 {
			var parsed any
			if err := json.Unmarshal(result, &parsed); err != nil {
				return fmt.Errorf("--result: invalid JSON: %s", err)
			}
			resultVal = parsed
		}

		// Phase B: emit. Every state write below is an event; apply does the
		// rest, and the cascade planning reads the tree back between them.
		for _, p := range plans {
			// Close cascaded descendants first.
			for _, child := range p.cascadeTasks {
				childPayload := DonePayload{
					Cascade:               new(true),
					CascadeClosedByParent: p.target.shortID,
					WasStatus:             child.Status,
				}
				if child.Status == "claimed" {
					if child.ClaimedBy != nil {
						childPayload.WasClaimedBy = *child.ClaimedBy
					}
					if child.ClaimExpiresAt != nil {
						childPayload.WasExpiresAt = *child.ClaimExpiresAt
					}
				}
				if err := b.emit(tx, EventDone, child.ShortID, actor, childPayload); err != nil {
					return err
				}
				if err := recordBlocksUnblockedOn(tx, child.ID, child.ShortID, actor); err != nil {
					return err
				}
			}

			// Close the explicit target.
			targetTask := p.target.task
			wasStatus := targetTask.Status
			payload := DonePayload{
				Note:          note,
				Cascade:       new(cascade),
				CascadeClosed: p.cascadeShorts,
				WasStatus:     wasStatus,
			}
			if wasStatus == "claimed" {
				if targetTask.ClaimedBy != nil {
					payload.WasClaimedBy = *targetTask.ClaimedBy
				}
				if targetTask.ClaimExpiresAt != nil {
					payload.WasExpiresAt = *targetTask.ClaimExpiresAt
				}
			}
			if resultVal != nil {
				payload.Result = resultVal
			}
			if waived := pendingByTarget[p.target.shortID]; len(waived) > 0 {
				payload.CriteriaWaived = waived
			}
			if bulkCriteriaState != "" {
				payload.CriteriaBulkState = bulkCriteriaState
			}
			if err := b.emit(tx, EventDone, p.target.shortID, actor, payload); err != nil {
				return err
			}
			if err := recordBlocksUnblockedOn(tx, p.target.task.ID, p.target.shortID, actor); err != nil {
				return err
			}

			// Leaf-frontier cascade: after closing this target, auto-close any
			// ancestors whose last open child has just been closed.
			autoClosed, err := cascadeAutoCloseAncestors(tx, b, p.target.task.ID, p.target.shortID, "done", actor)
			if err != nil {
				return err
			}

			closed = append(closed, &ClosedResult{
				ShortID:             p.target.shortID,
				Title:               p.target.task.Title,
				Note:                note,
				CascadeClosed:       p.cascadeShorts,
				AutoClosedAncestors: autoClosed,
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return closed, alreadyDone, nil
}

func recordBlocksUnblockedOn(tx dbtx, blockerID int64, blockerShortID, actor string) error {
	rows, err := tx.Query("SELECT blocked_id FROM blocks WHERE blocker_id = ?", blockerID)
	if err != nil {
		return err
	}
	var unblockedIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		unblockedIDs = append(unblockedIDs, id)
	}
	rows.Close()
	if len(unblockedIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec("DELETE FROM blocks WHERE blocker_id = ?", blockerID); err != nil {
		return err
	}
	for _, id := range unblockedIDs {
		var blockedShortID string
		if err := tx.QueryRow("SELECT short_id FROM tasks WHERE id = ?", id).Scan(&blockedShortID); err != nil {
			return err
		}
		if err := recordEvent(tx, id, EventUnblocked, actor, UnblockedPayload{
			BlockedID: blockedShortID,
			BlockerID: blockerShortID,
			Reason:    "blocker_done",
		}); err != nil {
			return err
		}
	}
	return nil
}

func RunReopen(db *sql.DB, shortID string, cascade bool, actor string) ([]string, error) {
	var reopenedChildren []string
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		reopenedChildren = nil
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		if err := checkClaimOwnership(tx, shortID, actor); err != nil {
			return err
		}

		task, err := GetTaskByShortID(tx, shortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", shortID)
		}
		if task.Status != "done" && task.Status != "canceled" {
			return fmt.Errorf("task %s is not done or canceled (status: %s)", shortID, task.Status)
		}
		fromStatus := task.Status

		if cascade {
			descendants, err := findClosedDescendants(tx, task.ID)
			if err != nil {
				return err
			}
			for _, d := range descendants {
				if err := b.emit(tx, EventReopened, d.ShortID, actor, ReopenedPayload{
					Cascade:          false,
					ReopenedChildren: []string{},
					FromStatus:       d.Status,
				}); err != nil {
					return err
				}
				reopenedChildren = append(reopenedChildren, d.ShortID)
			}
		}

		return b.emit(tx, EventReopened, shortID, actor, ReopenedPayload{
			Cascade:          cascade,
			ReopenedChildren: reopenedChildren,
			FromStatus:       fromStatus,
		})
	})
	if err != nil {
		return nil, err
	}
	return reopenedChildren, nil
}
