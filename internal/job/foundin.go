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
	return commit(db, func(tx dbtx, b *eventBatch) error {
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
		return emitFoundInSet(tx, b, task, source, actor)
	})
}

// emitFoundInSet records the edge inside an existing batch. The previous
// source is read here rather than in apply, because it is a fact about the
// moment the command ran and the scrubber rewinds the set with it.
func emitFoundInSet(tx dbtx, b *eventBatch, task, source *Task, actor string) error {
	previous, err := foundInSourceByID(tx, task.ID)
	if err != nil {
		return err
	}
	payload := FoundInSetPayload{
		TaskID:   task.ShortID,
		SourceID: source.ShortID,
	}
	if previous != nil && previous.ID != source.ID {
		payload.PreviousSourceID = previous.ShortID
	}
	return b.emit(tx, EventFoundInSet, task.ShortID, actor, payload)
}

// RunClearFoundIn removes a task's found-in reference. Clearing a task that
// has none is an error: the caller named an edge that does not exist, and
// silently succeeding would hide a mistyped id.
func RunClearFoundIn(db *sql.DB, taskShortID, actor string) error {
	return commit(db, func(tx dbtx, b *eventBatch) error {
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
		return b.emit(tx, EventFoundInCleared, taskShortID, actor, FoundInClearedPayload{
			TaskID:   taskShortID,
			SourceID: previous.ShortID,
		})
	})
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
