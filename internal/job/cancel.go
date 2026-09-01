package job

import (
	"database/sql"
	"fmt"
	"slices"
)

type CanceledResult struct {
	ShortID             string
	Title               string
	WasStatus           string
	CascadeCanceled     []string
	AutoClosedAncestors []AutoClosedAncestor
}

type PurgedResult struct {
	ShortID       string
	Title         string
	CascadePurged []string
	EventsErased  int
}

// RunCancel cancels one or more tasks atomically. With cascade=true, each
// target expands to include all open (non-done, non-canceled) descendants.
// With purge=true, the task and its events are erased rather than transitioned.
// When both purge and cascade are true, the entire subtree is erased and
// requires explicit yes=true confirmation.
func RunCancel(
	db *sql.DB,
	ids []string,
	reason string,
	cascade, purge, yes bool,
	actor string,
) (canceled []*CanceledResult, alreadyCanceled []string, purged []*PurgedResult, err error) {
	if len(ids) == 0 {
		return nil, nil, nil, fmt.Errorf("cancel requires at least one task id")
	}
	if reason == "" {
		if purge {
			return nil, nil, nil, fmt.Errorf(`cancel --purge requires --reason "<text>"`)
		}
		return nil, nil, nil, fmt.Errorf(`cancel requires --reason "<text>"`)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, nil, nil, err
	}

	if purge {
		purged, err = executePurge(tx, ids, reason, cascade, yes, actor)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, nil, nil, err
		}
		return nil, nil, purged, nil
	}

	canceled, alreadyCanceled, err = executeCancel(tx, ids, reason, cascade, actor)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	return canceled, alreadyCanceled, nil, nil
}

func executeCancel(
	tx dbtx,
	ids []string,
	reason string,
	cascade bool,
	actor string,
) (canceled []*CanceledResult, alreadyCanceled []string, err error) {
	type target struct {
		shortID string
		task    *Task
	}
	var targets []target
	seenExplicit := make(map[int64]bool)
	for _, id := range ids {
		if err := checkClaimOwnership(tx, id, actor); err != nil {
			return nil, nil, err
		}
		t, err := GetTaskByShortID(tx, id)
		if err != nil {
			return nil, nil, err
		}
		if t == nil {
			return nil, nil, fmt.Errorf("task %q not found", id)
		}
		if t.Status == "done" {
			return nil, nil, fmt.Errorf("task %s is already done; cancel only applies to open work", id)
		}
		if t.Status == "canceled" {
			alreadyCanceled = append(alreadyCanceled, id)
			continue
		}
		if seenExplicit[t.ID] {
			continue
		}
		seenExplicit[t.ID] = true
		targets = append(targets, target{shortID: id, task: t})
	}

	type plan struct {
		target        target
		cascadeTasks  []*Task
		cascadeShorts []string
	}
	var plans []plan
	seenCascade := make(map[int64]bool)
	for _, tgt := range targets {
		open, err := findOpenDescendants(tx, tgt.task.ID)
		if err != nil {
			return nil, nil, err
		}
		var cTasks []*Task
		var cShorts []string
		if cascade {
			for _, d := range open {
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

	now := CurrentNowFunc().Unix()

	for _, p := range plans {
		// Cancel cascaded descendants first.
		for _, child := range p.cascadeTasks {
			wasStatus := child.Status
			childPayload := CanceledPayload{
				Reason:                reason,
				Cascade:               new(true),
				CascadeClosedByParent: p.target.shortID,
				WasStatus:             wasStatus,
			}
			if wasStatus == "claimed" {
				if child.ClaimedBy != nil {
					childPayload.WasClaimedBy = *child.ClaimedBy
				}
				if child.ClaimExpiresAt != nil {
					childPayload.WasExpiresAt = *child.ClaimExpiresAt
				}
			}
			if _, err := tx.Exec(
				"UPDATE tasks SET status = 'canceled', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
				now, child.ID,
			); err != nil {
				return nil, nil, err
			}
			if err := recordEvent(tx, child.ID, EventCanceled, actor, childPayload); err != nil {
				return nil, nil, err
			}
			if err := recordBlocksUnblockedOnCancel(tx, child.ID, child.ShortID, actor); err != nil {
				return nil, nil, err
			}
		}

		targetTask := p.target.task
		wasStatus := targetTask.Status
		targetPayload := CanceledPayload{
			Reason:        reason,
			Cascade:       new(cascade),
			CascadeClosed: p.cascadeShorts,
			WasStatus:     wasStatus,
		}
		if wasStatus == "claimed" {
			if targetTask.ClaimedBy != nil {
				targetPayload.WasClaimedBy = *targetTask.ClaimedBy
			}
			if targetTask.ClaimExpiresAt != nil {
				targetPayload.WasExpiresAt = *targetTask.ClaimExpiresAt
			}
		}
		if _, err := tx.Exec(
			"UPDATE tasks SET status = 'canceled', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
			now, targetTask.ID,
		); err != nil {
			return nil, nil, err
		}
		if err := recordEvent(tx, targetTask.ID, EventCanceled, actor, targetPayload); err != nil {
			return nil, nil, err
		}
		// Canceling a root ends that tree: release focus visibly.
		if targetTask.ParentID == nil {
			if err := releaseFocusOnRootClose(tx, targetTask); err != nil {
				return nil, nil, err
			}
		}
		if err := recordBlocksUnblockedOnCancel(tx, p.target.task.ID, p.target.shortID, actor); err != nil {
			return nil, nil, err
		}

		// Leaf-frontier cascade (symmetric to done): if this cancel closed
		// the last open child of an ancestor, the ancestor auto-closes too.
		// Destination per ancestor is status-aware — see
		// cascadeAutoCloseAncestors.
		autoClosed, err := cascadeAutoCloseAncestors(tx, p.target.task.ID, p.target.shortID, "cancel", actor, now)
		if err != nil {
			return nil, nil, err
		}

		canceled = append(canceled, &CanceledResult{
			ShortID:             p.target.shortID,
			Title:               p.target.task.Title,
			WasStatus:           wasStatus,
			CascadeCanceled:     p.cascadeShorts,
			AutoClosedAncestors: autoClosed,
		})
	}

	return canceled, alreadyCanceled, nil
}

// recordBlocksUnblockedOnCancel mirrors recordBlocksUnblockedOn from tasks.go,
// but stamps the unblock reason as "blocker_canceled".
func recordBlocksUnblockedOnCancel(tx dbtx, blockerID int64, blockerShortID, actor string) error {
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
			Reason:    "blocker_canceled",
		}); err != nil {
			return err
		}
	}
	return nil
}

func executePurge(
	tx dbtx,
	ids []string,
	reason string,
	cascade, yes bool,
	actor string,
) ([]*PurgedResult, error) {
	type target struct {
		shortID       string
		task          *Task
		descendants   []*Task
		descShortIDs  []string
		eventsToErase int
	}
	var targets []target
	totalSubtreeCount := 0
	for _, id := range ids {
		if err := checkClaimOwnership(tx, id, actor); err != nil {
			return nil, err
		}
		t, err := GetTaskByShortID(tx, id)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("task %q not found", id)
		}

		if cascade {
			descs, err := findAllDescendants(tx, t.ID)
			if err != nil {
				return nil, err
			}
			tg := target{shortID: id, task: t, descendants: descs}
			for _, d := range descs {
				tg.descShortIDs = append(tg.descShortIDs, d.ShortID)
			}
			totalSubtreeCount += 1 + len(descs)
			targets = append(targets, tg)
		} else {
			children, err := findAllDescendants(tx, t.ID)
			if err != nil {
				return nil, err
			}
			if len(children) > 0 {
				return nil, fmt.Errorf("task %s has subtasks; add --cascade --yes to purge the subtree", id)
			}
			totalSubtreeCount++
			targets = append(targets, target{shortID: id, task: t})
		}
	}

	if cascade && !yes {
		return nil, fmt.Errorf("cancel --purge --cascade requires --yes (irrecoverable erasure of %d tasks)", totalSubtreeCount)
	}

	var results []*PurgedResult
	for _, tg := range targets {
		// Collect every task id in the subtree (target + descendants).
		var allIDs []int64
		allIDs = append(allIDs, tg.task.ID)
		for _, d := range tg.descendants {
			allIDs = append(allIDs, d.ID)
		}

		// Count events about to be erased (for reporting).
		eventsErased := 0
		for _, tid := range allIDs {
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ?", tid).Scan(&n); err != nil {
				return nil, err
			}
			eventsErased += n
		}

		// Record the audit event before deletion. Stored on the parent or as
		// an orphan event when the purged task is a root.
		payload := PurgedPayload{
			Reason:        reason,
			PurgedID:      tg.shortID,
			PurgedTitle:   tg.task.Title,
			Cascade:       cascade,
			CascadePurged: tg.descShortIDs,
		}
		if payload.CascadePurged == nil {
			payload.CascadePurged = []string{}
		}
		if tg.task.ParentID != nil {
			if err := recordEvent(tx, *tg.task.ParentID, EventPurged, actor, payload); err != nil {
				return nil, err
			}
		} else {
			if err := recordOrphanEvent(tx, EventPurged, actor, payload); err != nil {
				return nil, err
			}
		}

		// Erase event rows, block rows, then task rows for the subtree.
		for _, tid := range allIDs {
			if _, err := tx.Exec("DELETE FROM events WHERE task_id = ?", tid); err != nil {
				return nil, err
			}
			if _, err := tx.Exec("DELETE FROM blocks WHERE blocker_id = ? OR blocked_id = ?", tid, tid); err != nil {
				return nil, err
			}
			// found-in survives every status change of either end, but not
			// purge: the task row itself is erased, so the reference has
			// nothing left to point at. Deleted explicitly rather than left
			// to ON DELETE CASCADE, because `PRAGMA foreign_keys=ON` is set
			// on one pooled connection and may not be in force here — the
			// same reason the blocks delete above is explicit.
			if _, err := tx.Exec("DELETE FROM found_in WHERE task_id = ? OR source_id = ?", tid, tid); err != nil {
				return nil, err
			}
		}
		// Children first to satisfy foreign-key chain (descendants are listed
		// in pre-order; reverse to delete leaves first).
		for _, v := range slices.Backward(tg.descendants) {
			if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", v.ID); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", tg.task.ID); err != nil {
			return nil, err
		}

		results = append(results, &PurgedResult{
			ShortID:       tg.shortID,
			Title:         tg.task.Title,
			CascadePurged: tg.descShortIDs,
			EventsErased:  eventsErased,
		})
	}

	return results, nil
}
