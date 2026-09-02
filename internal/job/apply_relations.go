package job

import (
	"github.com/bensyverson/jobs/internal/eventlog"
)

// The relations, provenance and kind family's state writes: blocked,
// unblocked, labeled, unlabeled, found_in_set, found_in_cleared and
// kind_changed. The criteria pair lives in apply_criteria.go.
//
// Everything these need arrives in the payload. The only lookup any of them
// performs is short id to row id, because row ids are minted by the local
// cache and never travel in an event.
//
// The set-membership types are idempotent in both directions, which is what
// the merge rule for them requires (project/2026-09-01-git-native-event-log.md,
// "Merge rule per event type"): applying `blocked` for an edge that already
// exists writes nothing, and applying `unblocked` for an edge that is gone is
// not an error. An event naming a task this cache does not hold is a no-op
// too — that is a tombstoned id, or an event that overtook its `created`.

func applyBlocked(tx dbtx, e eventlog.Envelope) error {
	var p BlockedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	blockedID, blockerID, ok, err := blockEndpoints(tx, p.BlockedID, p.BlockerID)
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec(
		"INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)",
		blockerID, blockedID, eventSeconds(e),
	)
	return err
}

func applyUnblocked(tx dbtx, e eventlog.Envelope) error {
	var p UnblockedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	blockedID, blockerID, ok, err := blockEndpoints(tx, p.BlockedID, p.BlockerID)
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec(
		"DELETE FROM blocks WHERE blocker_id = ? AND blocked_id = ?", blockerID, blockedID,
	)
	return err
}

// blockEndpoints resolves both ends of an edge. ok is false when either end
// is unknown to this cache, which makes the event a no-op rather than an
// error.
func blockEndpoints(tx dbtx, blockedShort, blockerShort string) (blockedID, blockerID int64, ok bool, err error) {
	blockedID, ok, err = taskRowID(tx, blockedShort)
	if err != nil || !ok {
		return 0, 0, false, err
	}
	blockerID, ok, err = taskRowID(tx, blockerShort)
	if err != nil || !ok {
		return 0, 0, false, err
	}
	return blockedID, blockerID, true, nil
}

// applyLabeled attaches every name in the payload. Names is the whole set the
// command asked for, not just the ones that were new — Existing records which
// were already there for the reader, and the write is INSERT OR IGNORE, so
// the two produce the same rows.
func applyLabeled(tx dbtx, e eventlog.Envelope) error {
	var p LabeledPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, e.Task)
	if err != nil || !ok {
		return err
	}
	for _, name := range p.Names {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO task_labels (task_id, name, created_at) VALUES (?, ?, ?)",
			taskID, name, eventSeconds(e),
		); err != nil {
			return err
		}
	}
	return nil
}

func applyUnlabeled(tx dbtx, e eventlog.Envelope) error {
	var p UnlabeledPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, e.Task)
	if err != nil || !ok {
		return err
	}
	for _, name := range p.Names {
		if _, err := tx.Exec(
			"DELETE FROM task_labels WHERE task_id = ? AND name = ?", taskID, name,
		); err != nil {
			return err
		}
	}
	return nil
}

// applyFoundInSet writes the provenance edge. One source per task, so the
// upsert replaces whatever was there; PreviousSourceID in the payload is for
// the reader and the scrubber's rewind, not for this write.
func applyFoundInSet(tx dbtx, e eventlog.Envelope) error {
	var p FoundInSetPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, p.TaskID)
	if err != nil || !ok {
		return err
	}
	sourceID, ok, err := taskRowID(tx, p.SourceID)
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO found_in (task_id, source_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET source_id = excluded.source_id, created_at = excluded.created_at
	`, taskID, sourceID, eventSeconds(e))
	return err
}

func applyFoundInCleared(tx dbtx, e eventlog.Envelope) error {
	var p FoundInClearedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, p.TaskID)
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec("DELETE FROM found_in WHERE task_id = ?", taskID)
	return err
}

// applyKindChanged writes the kind the payload names. It is unconditional
// rather than guarded on the current value, for the same reason
// applyReleased is: a guard would make the write a no-op on a replay where
// the earlier transition has not been applied, and updated_at would then
// differ between the original and the rebuild.
func applyKindChanged(tx dbtx, e eventlog.Envelope) error {
	var p KindChangedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	_, err := tx.Exec(
		"UPDATE tasks SET kind = ?, updated_at = ? WHERE short_id = ?",
		p.To, eventSeconds(e), e.Task,
	)
	return err
}
