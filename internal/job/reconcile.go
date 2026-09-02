package job

import (
	"database/sql"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Reconcile: restoring the invariants a single replica keeps, after a rebuild
// that ingested another replica's events.
//
// Apply never derives. A cascade close is an explicit event the handler emits,
// not something the applier works out, which is what makes replay
// deterministic — and the price is that a trigger split across two machines
// leaves the invariant broken. Neither machine saw the other's half, so
// neither emitted the cascade.
//
// This is the one place a read can write. Every repair is an ordinary event
// appended to this replica's file through the normal commit path, so it
// propagates like any other, and every repair is printed
// (project/2026-09-01-git-native-event-log.md, "Rebuild, and when it runs").

// reconcileActor is the actor every repair is attributed to, so a reader can
// tell a merge repair from a human's decision. The one exception is the claim
// re-establish below, which must name the holder: the claim's owner is the
// event's actor.
const reconcileActor = "reconcile"

// reconcile evaluates every invariant and appends the repairing events,
// returning one line per repair.
func reconcile(db *sql.DB) ([]string, error) {
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	// The claim rule cannot be seen in the tables — claimed_by is one column,
	// so the second claim simply overwrote the first — so it is read from the
	// log before the batch opens.
	lost, err := findLostClaims(path)
	if err != nil {
		return nil, err
	}

	var repairs []string
	err = commit(db, func(tx dbtx, b *eventBatch) error {
		repairs = nil
		purges, err := purgeOrphanedChildren(tx, b, path)
		if err != nil {
			return err
		}
		repairs = append(repairs, purges...)

		claims, err := repairLostClaims(tx, b, lost)
		if err != nil {
			return err
		}
		repairs = append(repairs, claims...)

		closes, err := closeParentsWhoseChildrenAllClosed(tx, b)
		if err != nil {
			return err
		}
		repairs = append(repairs, closes...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return repairs, nil
}

// closeParentsWhoseChildrenAllClosed closes every parent whose children are
// all closed but which is still open — the cascade that neither machine could
// emit because neither saw the last child close.
//
// It runs to a fixpoint: closing a parent can be the event that closes its own
// parent.
func closeParentsWhoseChildrenAllClosed(tx dbtx, b *eventBatch) ([]string, error) {
	var repairs []string
	for {
		short, status, trigger, destination, found, err := nextParentToClose(tx)
		if err != nil {
			return nil, err
		}
		if !found {
			return repairs, nil
		}
		if destination == "done" {
			if err := b.emit(tx, EventDone, short, reconcileActor, DonePayload{
				AutoClosed:    true,
				TriggerKind:   reconcileActor,
				TriggeredBy:   trigger,
				CascadeStatus: destination,
				WasStatus:     status,
			}); err != nil {
				return nil, err
			}
		} else {
			if err := b.emit(tx, EventCanceled, short, reconcileActor, CanceledPayload{
				AutoClosed:    true,
				TriggerKind:   reconcileActor,
				TriggeredBy:   trigger,
				CascadeStatus: destination,
				WasStatus:     status,
			}); err != nil {
				return nil, err
			}
		}
		id, ok, err := taskRowID(tx, short)
		if err != nil {
			return nil, err
		}
		if ok {
			reason := UnblockBlockerDone
			if destination == "canceled" {
				reason = UnblockBlockerCanceled
			}
			if err := emitBlocksUnblockedOn(tx, b, id, short, reason, reconcileActor); err != nil {
				return nil, err
			}
		}
		repairs = append(repairs, fmt.Sprintf(
			"reconcile: closed %s as %s — every child closed, split across replicas", short, destination))
	}
}

// nextParentToClose finds one open parent whose children have all closed. An
// issue-tree root is exempt: its lifetime is not bounded by the plan that
// surfaced it, exactly as in the ordinary cascade.
func nextParentToClose(tx dbtx) (short, status, trigger, destination string, found bool, err error) {
	row := tx.QueryRow(`
		SELECT t.short_id, t.status,
		       (SELECT COUNT(*) FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL AND c.status = 'done'),
		       (SELECT c.short_id FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL
		        ORDER BY c.updated_at DESC, c.short_id LIMIT 1)
		FROM tasks t
		WHERE t.deleted_at IS NULL
		  AND t.status NOT IN ('done','canceled')
		  AND NOT (t.parent_id IS NULL AND t.kind = 'issue')
		  AND EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL)
		  AND NOT EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL
		                  AND c.status NOT IN ('done','canceled'))
		ORDER BY t.short_id LIMIT 1`)
	var doneChildren int
	var lastChild sql.NullString
	switch err := row.Scan(&short, &status, &doneChildren, &lastChild); err {
	case sql.ErrNoRows:
		return "", "", "", "", false, nil
	case nil:
	default:
		return "", "", "", "", false, err
	}
	destination = "canceled"
	if doneChildren > 0 {
		destination = "done"
	}
	return short, status, lastChild.String, destination, true, nil
}

// purgeOrphanedChildren purges every live task whose `created` event named a
// parent that another replica purged. A purge is a tombstone: later events for
// that id apply to nothing, and its children go with it.
func purgeOrphanedChildren(tx dbtx, b *eventBatch, path string) ([]string, error) {
	events, err := eventlog.ReadAll(eventlog.StoreDir(path))
	if err != nil {
		return nil, err
	}
	applyRekeys(events)

	purged := map[string]bool{}
	parentOf := map[string]string{}
	for _, e := range events {
		switch EventType(e.Type) {
		case EventPurged:
			var p PurgedPayload
			if err := decodeEventPayload(e, &p); err != nil {
				return nil, err
			}
			purged[p.PurgedID] = true
			for _, sid := range p.CascadePurged {
				purged[sid] = true
			}
		case EventCreated:
			var p CreatedPayload
			if err := decodeEventPayload(e, &p); err != nil {
				return nil, err
			}
			if p.ShortID != "" && p.ParentID != "" {
				parentOf[p.ShortID] = p.ParentID
			}
		}
	}

	var repairs []string
	for child, parent := range parentOf {
		if !purged[parent] || purged[child] {
			continue
		}
		task, err := GetTaskByShortID(tx, child)
		if err != nil {
			return nil, err
		}
		if task == nil {
			continue
		}
		descendants, err := findAllDescendants(tx, task.ID)
		if err != nil {
			return nil, err
		}
		shorts := make([]string, 0, len(descendants))
		for _, d := range descendants {
			shorts = append(shorts, d.ShortID)
		}
		if err := b.emit(tx, EventPurged, "", reconcileActor, PurgedPayload{
			Reason:        "parent " + parent + " was purged on another replica",
			PurgedID:      child,
			PurgedTitle:   task.Title,
			Cascade:       len(shorts) > 0,
			CascadePurged: shorts,
		}); err != nil {
			return nil, err
		}
		repairs = append(repairs, fmt.Sprintf(
			"reconcile: purged %s %q — its parent %s was purged on another replica", child, task.Title, parent))
	}
	return repairs, nil
}

// lostClaim is one claim that lost a merge: two replicas held the same task at
// the same time, and the earlier (ts, rep) keeps it.
type lostClaim struct {
	Task        string
	WinnerActor string
	WinnerUntil int64
	LoserActor  string
	LoserUntil  int64
	LoserPos    string
}

// findLostClaims reads the raw log for two `claimed` events on one task from
// two replicas whose windows overlap with no release between them.
//
// It cannot be read from the tables: claimed_by is one column, so the later
// claim simply overwrote the earlier and the conflict left no trace.
func findLostClaims(path string) ([]lostClaim, error) {
	events, err := eventlog.ReadAll(eventlog.StoreDir(path))
	if err != nil {
		return nil, err
	}
	applyRekeys(events)

	type held struct {
		actor string
		until int64
		rep   string
	}
	open := map[string]held{}
	repaired := map[string]bool{}
	var out []lostClaim

	for _, e := range events {
		switch EventType(e.Type) {
		case EventClaimed:
			var p ClaimedPayload
			if err := decodeEventPayload(e, &p); err != nil {
				return nil, err
			}
			cur, ok := open[e.Task]
			// Two claims from ONE replica are an ordinary --force takeover,
			// decided by a person who could see both. Only claims minted while
			// the replicas were apart are a merge conflict.
			if ok && cur.rep != e.Rep && cur.actor != e.Actor && e.TS < cur.until*1000 {
				out = append(out, lostClaim{
					Task:        e.Task,
					WinnerActor: cur.actor,
					WinnerUntil: cur.until,
					LoserActor:  e.Actor,
					LoserUntil:  p.ExpiresAt,
					LoserPos:    e.Position().String(),
				})
				continue
			}
			open[e.Task] = held{actor: e.Actor, until: p.ExpiresAt, rep: e.Rep}
		case EventReleased:
			var p ReleasedPayload
			if err := decodeEventPayload(e, &p); err != nil {
				return nil, err
			}
			if p.Reason == ReleaseLostMerge && p.LostClaim != "" {
				repaired[p.LostClaim] = true
			}
			delete(open, e.Task)
		case EventClaimExpired, EventDone, EventCanceled:
			delete(open, e.Task)
		}
	}

	kept := out[:0]
	for _, c := range out {
		if !repaired[c.LoserPos] {
			kept = append(kept, c)
		}
	}
	return kept, nil
}

// repairLostClaims releases the later claim and re-establishes the earlier one.
//
// Two events, not one: releasing the loser alone would leave the task
// unclaimed, and the winner never gave it up. The re-establishing `claimed`
// names the winner as its actor because applyClaimed reads the holder from
// there — a claim is by definition made by whoever emitted it — while the
// release is attributed to reconcile, which is what actually decided it.
func repairLostClaims(tx dbtx, b *eventBatch, lost []lostClaim) ([]string, error) {
	var repairs []string
	for _, c := range lost {
		task, err := GetTaskByShortID(tx, c.Task)
		if err != nil {
			return nil, err
		}
		if task == nil || task.Status == "done" || task.Status == "canceled" {
			continue
		}
		if err := b.emit(tx, EventReleased, c.Task, reconcileActor, ReleasedPayload{
			Reason:       ReleaseLostMerge,
			LostClaim:    c.LoserPos,
			WasClaimedBy: c.LoserActor,
			WasExpiresAt: c.LoserUntil,
		}); err != nil {
			return nil, err
		}
		if err := b.emit(tx, EventClaimed, c.Task, c.WinnerActor, ClaimedPayload{
			ExpiresAt: c.WinnerUntil,
		}); err != nil {
			return nil, err
		}
		repairs = append(repairs, fmt.Sprintf(
			"reconcile: released %s from %s (lost-merge) — %s claimed it first", c.Task, c.LoserActor, c.WinnerActor))
	}
	return repairs, nil
}
