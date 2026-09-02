package job

import (
	"database/sql"
	"fmt"
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

	err = commit(db, func(tx dbtx, b *eventBatch) error {
		canceled, alreadyCanceled, purged = nil, nil, nil
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		if purge {
			var err error
			purged, err = executePurge(tx, b, ids, reason, cascade, yes, actor)
			return err
		}
		var err error
		canceled, alreadyCanceled, err = executeCancel(tx, b, ids, reason, cascade, actor)
		return err
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return canceled, alreadyCanceled, purged, nil
}

func executeCancel(
	tx dbtx,
	b *eventBatch,
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
			if err := b.emit(tx, EventCanceled, child.ShortID, actor, childPayload); err != nil {
				return nil, nil, err
			}
			if err := emitBlocksUnblockedOn(tx, b, child.ID, child.ShortID, UnblockBlockerCanceled, actor); err != nil {
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
		if err := b.emit(tx, EventCanceled, p.target.shortID, actor, targetPayload); err != nil {
			return nil, nil, err
		}
		if err := emitBlocksUnblockedOn(tx, b, p.target.task.ID, p.target.shortID, UnblockBlockerCanceled, actor); err != nil {
			return nil, nil, err
		}

		// Leaf-frontier cascade (symmetric to done): if this cancel closed
		// the last open child of an ancestor, the ancestor auto-closes too.
		// Destination per ancestor is status-aware — see
		// cascadeAutoCloseAncestors.
		autoClosed, err := cascadeAutoCloseAncestors(tx, b, p.target.task.ID, p.target.shortID, "cancel", actor)
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

func executePurge(
	tx dbtx,
	b *eventBatch,
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
		// The tombstone hangs on the purged task's parent, or on nothing at
		// all when a root is purged — an orphan event, the one shape whose
		// envelope carries no task. Applying it erases the subtree.
		tombstoneTask := ""
		if tg.task.ParentID != nil {
			parent, err := getTaskByID(tx, *tg.task.ParentID)
			if err != nil {
				return nil, err
			}
			if parent != nil {
				tombstoneTask = parent.ShortID
			}
		}
		if err := b.emit(tx, EventPurged, tombstoneTask, actor, payload); err != nil {
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
