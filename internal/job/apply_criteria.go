package job

import (
	"database/sql"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The criteria family's state writes: criteria_added and criterion_state.
//
// criteria_added is idempotent by (task, criterion short id), which is the
// merge rule for it. That is only possible because the id travels in the
// event: apply inserts the row with the id and the fractional sort key the
// handler minted rather than minting either itself, so the same event applied
// twice — a replay, or the same line arriving from two log files — writes one
// row, and a shuffled log lands the same order.

func applyCriteriaAdded(tx dbtx, e eventlog.Envelope) error {
	var p CriteriaAddedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, e.Task)
	if err != nil || !ok {
		return err
	}
	at := eventSeconds(e)
	for _, c := range p.Criteria {
		if c.ShortID == "" {
			return fmt.Errorf("criteria_added entry %q carries no short id", c.Label)
		}
		var exists bool
		if err := tx.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM task_criteria WHERE task_id = ? AND short_id = ?)",
			taskID, c.ShortID,
		).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		state := c.State
		if state == "" {
			state = string(CriterionPending)
		}
		if _, err := tx.Exec(`
			INSERT INTO task_criteria (task_id, short_id, label, state, sort_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			taskID, c.ShortID, c.Label, state, c.SortKey, at, at,
		); err != nil {
			return err
		}
	}
	return nil
}

// applyCriterionState writes one criterion's state. The payload's short id is
// the address; the label is a fallback for events recorded before criteria
// had ids, and is never written back — a criterion's label is edited by other
// means and a state change must not revert it.
func applyCriterionState(tx dbtx, e eventlog.Envelope) error {
	var p CriterionStatePayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	taskID, ok, err := taskRowID(tx, e.Task)
	if err != nil || !ok {
		return err
	}
	id, ok, err := criterionRowID(tx, taskID, p.ShortID, p.Label)
	if err != nil || !ok {
		return err
	}
	_, err = tx.Exec(
		"UPDATE task_criteria SET state = ?, updated_at = ? WHERE id = ?",
		p.State, eventSeconds(e), id,
	)
	return err
}

// criterionRowID resolves a criterion to this cache's row id, by short id
// first and by verbatim label second. ok is false when neither matches, which
// makes the event a no-op.
func criterionRowID(tx dbtx, taskID int64, shortID, label string) (int64, bool, error) {
	if shortID != "" {
		var id int64
		err := tx.QueryRow(
			"SELECT id FROM task_criteria WHERE task_id = ? AND short_id = ?", taskID, shortID,
		).Scan(&id)
		if err == nil {
			return id, true, nil
		}
		if err != sql.ErrNoRows {
			return 0, false, err
		}
	}
	if label == "" {
		return 0, false, nil
	}
	var id int64
	err := tx.QueryRow(
		"SELECT id FROM task_criteria WHERE task_id = ? AND label = ?", taskID, label,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}
