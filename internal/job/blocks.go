package job

import (
	"database/sql"
	"fmt"
	"strings"
)

func RunBlock(db *sql.DB, blockedShortID, blockerShortID, actor string) error {
	return RunBlockMany(db, blockedShortID, []string{blockerShortID}, actor)
}

// RunBlockMany applies N block edges atomically. Duplicates in the input
// collapse to a single edge (and a single event). Cycle detection runs
// against the combined post-state. On any failure the transaction rolls
// back and nothing persists.
func RunBlockMany(db *sql.DB, blockedShortID string, blockerShortIDs []string, actor string) error {
	if len(blockerShortIDs) == 0 {
		return fmt.Errorf("no blockers provided")
	}

	return commit(db, func(tx dbtx, b *eventBatch) error {
		blocked, err := GetTaskByShortID(tx, blockedShortID)
		if err != nil {
			return err
		}
		if blocked == nil {
			return fmt.Errorf("task %q not found", blockedShortID)
		}

		// Dedup while preserving caller order so events land in a predictable
		// sequence and the per-edge ack lines match the order the user typed.
		seen := make(map[string]bool, len(blockerShortIDs))
		uniqueShortIDs := make([]string, 0, len(blockerShortIDs))
		for _, id := range blockerShortIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			uniqueShortIDs = append(uniqueShortIDs, id)
		}

		// Resolve all blockers up-front so a missing one fails fast before any
		// edge is written. Track them in input order alongside their short IDs
		// for per-edge cycle reporting and event recording.
		type blockerEntry struct {
			shortID string
			id      int64
		}
		blockers := make([]blockerEntry, 0, len(uniqueShortIDs))
		for _, sid := range uniqueShortIDs {
			t, err := GetTaskByShortID(tx, sid)
			if err != nil {
				return err
			}
			if t == nil {
				return fmt.Errorf("task %q not found", sid)
			}
			if t.ID == blocked.ID {
				return fmt.Errorf("a task cannot block itself")
			}
			blockers = append(blockers, blockerEntry{shortID: sid, id: t.ID})
		}

		// Cycle check has to consider edges added earlier in this same call,
		// not just the persisted graph. Each iteration emits its event before
		// the next check runs, and apply writes as it goes, so a later check
		// sees the earlier edge.
		for _, be := range blockers {
			circular, err := wouldCreateCycle(tx, blocked.ID, be.id)
			if err != nil {
				return err
			}
			if circular {
				return fmt.Errorf("cannot block %s by %s: would create a circular dependency", blockedShortID, be.shortID)
			}
			if err := b.emit(tx, EventBlocked, blockedShortID, actor, BlockedPayload{
				BlockedID: blockedShortID,
				BlockerID: be.shortID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func wouldCreateCycle(tx dbtx, blockedID, blockerID int64) (bool, error) {
	visited := make(map[int64]bool)
	return walkBlockerChain(tx, blockerID, blockedID, visited)
}

func walkBlockerChain(tx dbtx, startID, targetID int64, visited map[int64]bool) (bool, error) {
	if startID == targetID {
		return true, nil
	}
	if visited[startID] {
		return false, nil
	}
	visited[startID] = true

	rows, err := tx.Query("SELECT blocker_id FROM blocks WHERE blocked_id = ?", startID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var blockerID int64
		if err := rows.Scan(&blockerID); err != nil {
			return false, err
		}
		found, err := walkBlockerChain(tx, blockerID, targetID, visited)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, rows.Err()
}

func RunUnblock(db *sql.DB, blockedShortID, blockerShortID, actor string) error {
	return RunUnblockMany(db, blockedShortID, []string{blockerShortID}, actor)
}

// RunUnblockMany removes N block edges atomically. If any named edge does
// not exist, the whole transaction rolls back.
func RunUnblockMany(db *sql.DB, blockedShortID string, blockerShortIDs []string, actor string) error {
	if len(blockerShortIDs) == 0 {
		return fmt.Errorf("no blockers provided")
	}

	return commit(db, func(tx dbtx, b *eventBatch) error {
		blocked, err := GetTaskByShortID(tx, blockedShortID)
		if err != nil {
			return err
		}
		if blocked == nil {
			return fmt.Errorf("task %q not found", blockedShortID)
		}

		seen := make(map[string]bool, len(blockerShortIDs))
		for _, sid := range blockerShortIDs {
			if seen[sid] {
				continue
			}
			seen[sid] = true

			blocker, err := GetTaskByShortID(tx, sid)
			if err != nil {
				return err
			}
			if blocker == nil {
				return fmt.Errorf("task %q not found", sid)
			}

			// The edge has to exist before the event is emitted: apply's
			// delete is idempotent by design, so it cannot be the check.
			var exists bool
			if err := tx.QueryRow(
				"SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker_id = ? AND blocked_id = ?)",
				blocker.ID, blocked.ID,
			).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%s is not blocked by %s", blockedShortID, sid)
			}

			if err := b.emit(tx, EventUnblocked, blockedShortID, actor, UnblockedPayload{
				BlockedID: blockedShortID,
				BlockerID: sid,
				Reason:    UnblockManual,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// emitBlocksUnblockedOn drops every edge a closing task was blocking, as one
// `unblocked` event per edge. The delete lives in applyUnblocked, so a
// rebuild reproduces it; reason names which close did it.
func emitBlocksUnblockedOn(tx dbtx, b *eventBatch, blockerID int64, blockerShortID string, reason UnblockReason, actor string) error {
	rows, err := tx.Query(`
		SELECT t.short_id FROM blocks
		JOIN tasks t ON t.id = blocks.blocked_id
		WHERE blocks.blocker_id = ?
		ORDER BY t.short_id`, blockerID)
	if err != nil {
		return err
	}
	var blockedShortIDs []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			rows.Close()
			return err
		}
		blockedShortIDs = append(blockedShortIDs, sid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, sid := range blockedShortIDs {
		if err := b.emit(tx, EventUnblocked, sid, actor, UnblockedPayload{
			BlockedID: sid,
			BlockerID: blockerShortID,
			Reason:    reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetBlockersForTaskIDs returns a map of blocked-task-id to the short IDs of
// its open blockers. One query covers the whole set, replacing N+1 walks.
func GetBlockersForTaskIDs(db *sql.DB, taskIDs []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(taskIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		args = append(args, id)
	}
	rows, err := db.Query(`
		SELECT b.blocked_id, blocker.short_id
		FROM blocks b
		JOIN tasks blocker ON blocker.id = b.blocker_id
		WHERE b.blocked_id IN (`+placeholders+`)
		  AND blocker.status != 'done'
		  AND blocker.deleted_at IS NULL
		ORDER BY b.blocked_id, blocker.short_id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var blockedID int64
		var blockerShort string
		if err := rows.Scan(&blockedID, &blockerShort); err != nil {
			return nil, err
		}
		out[blockedID] = append(out[blockedID], blockerShort)
	}
	return out, rows.Err()
}

func GetBlockers(db *sql.DB, shortID string) ([]*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	rows, err := db.Query(`
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_key,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM blocks b
		JOIN tasks t ON t.id = b.blocker_id
		WHERE b.blocked_id = ? AND t.status != 'done' AND t.deleted_at IS NULL
	`, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var blockers []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		blockers = append(blockers, t)
	}
	return blockers, rows.Err()
}

// GetBlocked returns the tasks that this task is blocking — i.e. the tasks
// that depend on this one. Filters out closed (done/canceled) blocked tasks
// since they're no longer waiting on anything.
func GetBlocked(db *sql.DB, shortID string) ([]*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	rows, err := db.Query(`
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_key,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM blocks b
		JOIN tasks t ON t.id = b.blocked_id
		WHERE b.blocker_id = ? AND t.status NOT IN ('done', 'canceled') AND t.deleted_at IS NULL
	`, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
