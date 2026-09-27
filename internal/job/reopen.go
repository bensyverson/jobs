package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
)

// Reopening reverses a close, including the parts the close did on its own.
//
// Closing a task has two automatic consequences, each already an explicit
// event in the log: the leaf-frontier cascade closes an ancestor whose last
// open child just closed (a done/canceled event with auto_closed set), and
// every block edge the task held is dropped (an unblocked event whose reason
// is blocker_done or blocker_canceled). Both rest on the premise "this task
// is finished", which reopening makes false, so reopen undoes them with
// ordinary `reopened` and `blocked` events — no new event type, no apply
// semantics, and a replay reproduces it exactly.
//
// What a person did since is never undone. An ancestor is reopened only while
// its most recent close is still the automatic one, and the walk stops at the
// first ancestor that is open or was closed by hand. An edge is restored only
// while the most recent block/unblock on it is still the automatic unblock,
// the dependent is still open and present, and putting it back closes no
// cycle.
//
// `--cascade` applies the same rule downward. It undoes the target's own
// cascading close (`done --cascade` or `cancel --cascade`) and nothing else:
// the descendants it reopens are the ones that close listed in
// cascade_closed, which names every descendant it reached (grandchildren
// included), and only those whose most recent close is still the one the
// target's cascade gave them. A descendant closed separately, before or
// since, stays closed. When the target's last close cascaded to nothing,
// --cascade reopens nothing extra and the result says so.

// ReopenResult is what one `reopen` changed besides the named task.
type ReopenResult struct {
	// ReopenedChildren are the descendants `--cascade` reopened.
	ReopenedChildren []string
	// CascadeClosed is what the target's most recent close closed along with
	// it, as that close recorded it; empty when the close cascaded to nothing.
	// Set only with `--cascade`.
	CascadeClosed []string
	// CascadeLeftAlone are the CascadeClosed entries `--cascade` did not
	// reopen because a person reopened or closed them since.
	CascadeLeftAlone []string
	// ReopenedAncestors were closed by the cascade, nearest parent first.
	ReopenedAncestors []ReopenedAncestor
	// RestoredBlocks are the edges put back, grouped by blocker in the order
	// the blockers were reopened.
	RestoredBlocks []RestoredBlock
}

// ReopenedAncestor names an ancestor reopened because the cascade had closed it.
type ReopenedAncestor struct {
	ShortID string
	Title   string
}

// RestoredBlock is a block edge reopen put back.
type RestoredBlock struct {
	BlockedID    string
	BlockedTitle string
	BlockerID    string
}

// RunReopen returns a closed task to available. With cascade, the
// descendants the task's own cascading close closed are reopened too. Either
// way, the ancestors the cascade closed and the block edges the closes
// removed are restored.
func RunReopen(db *sql.DB, shortID string, cascade bool, actor string) (*ReopenResult, error) {
	var res *ReopenResult
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		res = &ReopenResult{}
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

		// Every task this command reopens, in emit order; each one's
		// dropped edges are restored once all of them are open, so an edge
		// between two of them is judged against the reopened state.
		var reopened []*Task

		if cascade {
			recorded, descendants, leftAlone, err := cascadeClosedDescendants(tx, task)
			if err != nil {
				return err
			}
			res.CascadeClosed = recorded
			res.CascadeLeftAlone = leftAlone
			for _, d := range descendants {
				if err := emitReopened(tx, b, d, actor); err != nil {
					return err
				}
				res.ReopenedChildren = append(res.ReopenedChildren, d.ShortID)
				reopened = append(reopened, d)
			}
		}

		// Read the ancestors before the target reopens: the walk asks
		// whether each close was automatic, which is history, not status.
		ancestors, err := autoClosedAncestors(tx, task)
		if err != nil {
			return err
		}

		if err := b.emit(tx, EventReopened, shortID, actor, ReopenedPayload{
			Cascade:          cascade,
			ReopenedChildren: res.ReopenedChildren,
			FromStatus:       task.Status,
		}); err != nil {
			return err
		}
		reopened = append(reopened, task)

		for _, a := range ancestors {
			if err := emitReopened(tx, b, a, actor); err != nil {
				return err
			}
			res.ReopenedAncestors = append(res.ReopenedAncestors, ReopenedAncestor{ShortID: a.ShortID, Title: a.Title})
			reopened = append(reopened, a)
		}

		for _, t := range reopened {
			restored, err := restoreDroppedBlocks(tx, b, t, actor)
			if err != nil {
				return err
			}
			res.RestoredBlocks = append(res.RestoredBlocks, restored...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// emitReopened records the reopen of a task reopened on another's account —
// a cascaded descendant or an auto-closed ancestor.
func emitReopened(tx dbtx, b *eventBatch, t *Task, actor string) error {
	return b.emit(tx, EventReopened, t.ShortID, actor, ReopenedPayload{
		Cascade:          false,
		ReopenedChildren: []string{},
		FromStatus:       t.Status,
	})
}

// autoClosedAncestors walks up from t and returns, nearest first, each
// ancestor that is closed and whose most recent close was the cascade's. It
// stops at the first ancestor that is open or was last closed by hand.
func autoClosedAncestors(tx dbtx, t *Task) ([]*Task, error) {
	var out []*Task
	for cursor := t; cursor.ParentID != nil; {
		parent, err := scanTask(tx.QueryRow(`
			SELECT id, short_id, parent_id, title, description, status, sort_key,
			       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
			FROM tasks WHERE id = ?`, *cursor.ParentID))
		if err != nil {
			return nil, err
		}
		if parent.Status != "done" && parent.Status != "canceled" {
			return out, nil
		}
		rec, err := lastClose(tx, parent.ID)
		if err != nil {
			return nil, err
		}
		if !rec.AutoClosed {
			return out, nil
		}
		out = append(out, parent)
		cursor = parent
	}
	return out, nil
}

// closeRecord is what a task's most recent done or canceled event says about
// how it closed. The zero value is a task that never closed.
type closeRecord struct {
	// AutoClosed: the leaf-frontier cascade closed it, not a person.
	AutoClosed bool
	// CascadeClosed: the descendants this close closed along with it.
	CascadeClosed []string
	// ClosedByParent: the explicit target whose cascade closed this task.
	ClosedByParent string
}

// lastClose reads the task's most recent done or canceled event.
func lastClose(tx dbtx, taskID int64) (closeRecord, error) {
	var typ, detail string
	err := tx.QueryRow(`
		SELECT event_type, COALESCE(detail, '') FROM events
		WHERE task_id = ? AND event_type IN (?, ?)
		ORDER BY ts DESC, rep DESC, seq DESC LIMIT 1`,
		taskID, string(EventDone), string(EventCanceled),
	).Scan(&typ, &detail)
	if err == sql.ErrNoRows || (err == nil && detail == "") {
		return closeRecord{}, nil
	}
	if err != nil {
		return closeRecord{}, err
	}
	if EventType(typ) == EventDone {
		var p DonePayload
		if json.Unmarshal([]byte(detail), &p) != nil {
			return closeRecord{}, nil
		}
		return closeRecord{AutoClosed: p.AutoClosed, CascadeClosed: p.CascadeClosed, ClosedByParent: p.CascadeClosedByParent}, nil
	}
	var p CanceledPayload
	if json.Unmarshal([]byte(detail), &p) != nil {
		return closeRecord{}, nil
	}
	return closeRecord{AutoClosed: p.AutoClosed, CascadeClosed: p.CascadeClosed, ClosedByParent: p.CascadeClosedByParent}, nil
}

// cascadeClosedDescendants returns, in the order the close recorded them, the
// descendants target's most recent close cascaded to that are still closed by
// it, and separately the ones a person has reopened or closed again since.
func cascadeClosedDescendants(tx dbtx, target *Task) (recorded []string, still []*Task, leftAlone []string, err error) {
	rec, err := lastClose(tx, target.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, sid := range rec.CascadeClosed {
		d, err := GetTaskByShortID(tx, sid)
		if err != nil {
			return nil, nil, nil, err
		}
		if d == nil {
			// Purged since: there is nothing left to reopen or report.
			continue
		}
		if d.Status == "done" || d.Status == "canceled" {
			own, err := lastClose(tx, d.ID)
			if err != nil {
				return nil, nil, nil, err
			}
			if own.ClosedByParent == target.ShortID {
				still = append(still, d)
				continue
			}
		}
		leftAlone = append(leftAlone, sid)
	}
	return rec.CascadeClosed, still, leftAlone, nil
}

// restoreDroppedBlocks re-adds the edges blocker held that were dropped
// because it closed, and that nothing has touched since.
func restoreDroppedBlocks(tx dbtx, b *eventBatch, blocker *Task, actor string) ([]RestoredBlock, error) {
	dependents, err := autoUnblockedDependents(tx, blocker.ShortID)
	if err != nil {
		return nil, err
	}
	var out []RestoredBlock
	for _, sid := range dependents {
		dep, err := GetTaskByShortID(tx, sid)
		if err != nil {
			return nil, err
		}
		// Purged, or finished since: a closed task waits on nothing.
		if dep == nil || dep.Status == "done" || dep.Status == "canceled" {
			continue
		}
		dropped, err := edgeLastDroppedByClose(tx, dep.ID, blocker.ShortID)
		if err != nil {
			return nil, err
		}
		if !dropped {
			continue
		}
		var exists bool
		if err := tx.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker_id = ? AND blocked_id = ?)",
			blocker.ID, dep.ID,
		).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		_, circular, err := newWaitGraph(tx).blockCycle(dep.ShortID, blocker.ShortID)
		if err != nil {
			return nil, err
		}
		if circular {
			continue
		}
		if err := b.emit(tx, EventBlocked, dep.ShortID, actor, BlockedPayload{
			BlockedID: dep.ShortID,
			BlockerID: blocker.ShortID,
		}); err != nil {
			return nil, err
		}
		out = append(out, RestoredBlock{BlockedID: dep.ShortID, BlockedTitle: dep.Title, BlockerID: blocker.ShortID})
	}
	return out, nil
}

// autoUnblockedDependents returns, sorted, every task that ever lost an edge
// to blocker because blocker closed.
func autoUnblockedDependents(tx dbtx, blockerShortID string) ([]string, error) {
	// The LIKE is only a prefilter so the scan decodes a handful of rows;
	// the decoded payload is the test.
	rows, err := tx.Query(`
		SELECT detail FROM events
		WHERE event_type = ? AND detail LIKE '%' || ? || '%'`,
		string(EventUnblocked), blockerShortID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			return nil, err
		}
		var p UnblockedPayload
		if json.Unmarshal([]byte(detail), &p) != nil {
			continue
		}
		if p.BlockerID != blockerShortID || !p.Reason.droppedByClose() || seen[p.BlockedID] {
			continue
		}
		seen[p.BlockedID] = true
		out = append(out, p.BlockedID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// edgeLastDroppedByClose reports whether the most recent block or unblock of
// the edge (blocked, blocker) was the automatic unblock a close emits — that
// is, no person has blocked or unblocked it since.
func edgeLastDroppedByClose(tx dbtx, blockedID int64, blockerShortID string) (bool, error) {
	rows, err := tx.Query(`
		SELECT event_type, COALESCE(detail, '') FROM events
		WHERE task_id = ? AND event_type IN (?, ?)
		ORDER BY ts DESC, rep DESC, seq DESC`,
		blockedID, string(EventBlocked), string(EventUnblocked),
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ, detail string
		if err := rows.Scan(&typ, &detail); err != nil {
			return false, err
		}
		if EventType(typ) == EventBlocked {
			var p BlockedPayload
			if json.Unmarshal([]byte(detail), &p) == nil && p.BlockerID == blockerShortID {
				return false, nil
			}
			continue
		}
		var p UnblockedPayload
		if json.Unmarshal([]byte(detail), &p) == nil && p.BlockerID == blockerShortID {
			return p.Reason.droppedByClose(), nil
		}
	}
	return false, rows.Err()
}

// droppedByClose reports whether the edge went away because its blocker
// closed, as opposed to a person removing it.
func (r UnblockReason) droppedByClose() bool {
	return r == UnblockBlockerDone || r == UnblockBlockerCanceled
}
