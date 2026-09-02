package job

import (
	"database/sql"
	"fmt"
)

type HeartbeatResult struct {
	ShortID   string
	ExpiresAt int64
}

// RunHeartbeat extends the caller's live claims on each id to at least
// DefaultClaimTTLSeconds from now, never shortening a claim that already
// runs longer. Validates all ids first; commits atomically.
// Errors strictly if the caller does not currently hold the claim
// (including expired-was-mine).
func RunHeartbeat(db *sql.DB, ids []string, actor string) ([]*HeartbeatResult, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("heartbeat requires at least one task id")
	}

	var results []*HeartbeatResult
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		results = nil
		return heartbeatInTx(tx, b, ids, actor, &results)
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// heartbeatInTx validates every id and emits one heartbeat per live claim the
// caller holds. Split out so RunHeartbeat's body reads as one batch.
func heartbeatInTx(tx dbtx, b *eventBatch, ids []string, actor string, out *[]*HeartbeatResult) error {
	// Snapshot pre-expire holders so we can distinguish "my claim expired
	// and was flipped to available" from "never claimed" after
	// expireStaleClaimsInTx runs.
	priorHolder := make(map[string]string)
	for _, id := range ids {
		t, err := GetTaskByShortID(tx, id)
		if err != nil {
			return err
		}
		if t == nil {
			continue
		}
		if t.Status == "claimed" && t.ClaimedBy != nil {
			priorHolder[id] = *t.ClaimedBy
		}
	}

	if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
		return err
	}

	type target struct {
		shortID string
		task    *Task
	}
	var targets []target
	for _, id := range ids {
		t, err := GetTaskByShortID(tx, id)
		if err != nil {
			return err
		}
		if t == nil {
			return fmt.Errorf("task %q not found", id)
		}
		switch t.Status {
		case "done", "canceled":
			return fmt.Errorf("task %s is %s; heartbeat refreshes only live claims.", id, t.Status)
		case "available":
			if priorHolder[id] == actor {
				return fmt.Errorf("your claim on %s expired; reclaim with 'job claim %s'", id, id)
			}
			return fmt.Errorf("task %s is not claimed (status: %s); heartbeat refreshes a live claim.", id, t.Status)
		case "claimed":
			if t.ClaimedBy != nil && *t.ClaimedBy == actor {
				targets = append(targets, target{shortID: id, task: t})
				continue
			}
			// Claim is held by someone else. If the caller previously held
			// it (stolen claim), reuse checkClaimOwnership's "now held by"
			// wording. Otherwise emit our own "not you" message mirroring
			// release.
			var callerOnceHeld bool
			if err := tx.QueryRow(
				`SELECT EXISTS(
					SELECT 1 FROM events
					WHERE task_id = ? AND event_type = 'claimed' AND actor = ?
				)`, t.ID, actor,
			).Scan(&callerOnceHeld); err != nil {
				return err
			}
			if callerOnceHeld {
				if err := checkClaimOwnership(tx, id, actor); err != nil {
					return err
				}
			}
			holder := ""
			if t.ClaimedBy != nil {
				holder = *t.ClaimedBy
			}
			return fmt.Errorf("task %s is claimed by %s, not you. 'heartbeat' refreshes only your own claims.",
				id, holder)
		default:
			return fmt.Errorf("task %s is %s; heartbeat refreshes only live claims.", id, t.Status)
		}
	}

	// Each deadline is resolved here and carried absolute in the payload —
	// apply never turns a TTL into a time. It is per task rather than one
	// for the batch because extendedClaimExpiry reads the claim's current
	// deadline, and a batch can mix a two-hour claim with a short one.
	for _, tg := range targets {
		var current int64
		if tg.task.ClaimExpiresAt != nil {
			current = *tg.task.ClaimExpiresAt
		}
		newExpiresAt := extendedClaimExpiry(current)
		if err := b.emit(tx, EventHeartbeat, tg.shortID, actor, HeartbeatPayload{
			NewExpiresAt: newExpiresAt,
		}); err != nil {
			return err
		}
		*out = append(*out, &HeartbeatResult{
			ShortID:   tg.shortID,
			ExpiresAt: newExpiresAt,
		})
	}
	return nil
}
