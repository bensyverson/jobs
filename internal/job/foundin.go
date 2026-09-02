package job

import (
	"database/sql"
	"fmt"
)

// found-in is provenance, not sequence. It records the leaf that surfaced a
// task — typically a defect discovered while working a plan — so the defect
// can live in its own tree without the plan being held open by it. Unlike
// `blocks`, the edge gates nothing: neither end constrains the other's
// claimability, close, or cascade.
//
// One source per task, because work is found in one place. Setting a source
// over an existing one replaces it and records both ids on the event.

const taskSelectColumns = `id, short_id, parent_id, title, description, status, sort_key,
	       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind`

// Qualified form for the queries that join found_in, which carries its own
// created_at and would otherwise make the column reference ambiguous.
const qualifiedTaskSelectColumns = `tasks.id, tasks.short_id, tasks.parent_id, tasks.title,
	       tasks.description, tasks.status, tasks.sort_key, tasks.claimed_by,
	       tasks.claim_expires_at, tasks.completion_note, tasks.created_at,
	       tasks.updated_at, tasks.deleted_at, tasks.kind`

// RunSetFoundIn records that taskShortID was surfaced by sourceShortID,
// replacing any source already recorded. A task cannot be found in itself;
// longer loops are permitted, since nothing traverses this edge.
func RunSetFoundIn(db *sql.DB, taskShortID, sourceShortID, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	task, err := GetTaskByShortID(tx, taskShortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", taskShortID)
	}
	source, err := GetTaskByShortID(tx, sourceShortID)
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("task %q not found", sourceShortID)
	}
	if source.ID == task.ID {
		return fmt.Errorf("a task cannot be found in itself")
	}

	if err := setFoundInTx(tx, task, source, taskShortID, sourceShortID, actor); err != nil {
		return err
	}
	return tx.Commit()
}

// setFoundInTx writes the edge and its event inside an existing transaction.
func setFoundInTx(tx dbtx, task, source *Task, taskShortID, sourceShortID, actor string) error {
	previous, err := foundInSourceByID(tx, task.ID)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(`
		INSERT INTO found_in (task_id, source_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET source_id = excluded.source_id, created_at = excluded.created_at
	`, task.ID, source.ID, CurrentNowFunc().Unix()); err != nil {
		return err
	}

	payload := FoundInSetPayload{
		TaskID:   taskShortID,
		SourceID: sourceShortID,
	}
	if previous != nil && previous.ID != source.ID {
		payload.PreviousSourceID = previous.ShortID
	}
	return recordEvent(tx, task.ID, EventFoundInSet, actor, payload)
}

// RunClearFoundIn removes a task's found-in reference. Clearing a task that
// has none is an error: the caller named an edge that does not exist, and
// silently succeeding would hide a mistyped id.
func RunClearFoundIn(db *sql.DB, taskShortID, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	task, err := GetTaskByShortID(tx, taskShortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", taskShortID)
	}

	previous, err := foundInSourceByID(tx, task.ID)
	if err != nil {
		return err
	}
	if previous == nil {
		return fmt.Errorf("task %s has no found-in reference to clear", taskShortID)
	}

	if _, err := tx.Exec("DELETE FROM found_in WHERE task_id = ?", task.ID); err != nil {
		return err
	}
	if err := recordEvent(tx, task.ID, EventFoundInCleared, actor, FoundInClearedPayload{
		TaskID:   taskShortID,
		SourceID: previous.ShortID,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// GetFoundInSource returns the task that surfaced shortID, or nil when none
// is recorded. The source is returned whatever its status and whether or not
// it is soft-deleted — the whole point of the edge is that it outlives the
// work that produced it.
func GetFoundInSource(db *sql.DB, shortID string) (*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	return foundInSourceByID(db, task.ID)
}

func foundInSourceByID(tx dbtx, taskID int64) (*Task, error) {
	row := tx.QueryRow(`
		SELECT `+taskSelectColumns+`
		FROM tasks WHERE id = (SELECT source_id FROM found_in WHERE task_id = ?)
	`, taskID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// GetSurfaced returns the tasks recorded as found in shortID — the other end
// of the reference. Closed issues stay listed (provenance survives their
// close too); soft-deleted ones do not, matching every other reader.
func GetSurfaced(db *sql.DB, shortID string) ([]*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	return surfacedByID(db, task.ID)
}

func surfacedByID(tx dbtx, sourceID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT `+qualifiedTaskSelectColumns+`
		FROM tasks
		JOIN found_in ON found_in.task_id = tasks.id
		WHERE found_in.source_id = ? AND tasks.deleted_at IS NULL
		ORDER BY tasks.short_id
	`, sourceID)
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
