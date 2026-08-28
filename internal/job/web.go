package job

import (
	"database/sql"
	"fmt"
	"strings"
)

// GetEventsForTask returns events for a single task (not its
// descendants). Used by the web dashboard's task-detail view where
// only this task's history is shown. Returns an empty slice (not an
// error) when the task has no events; returns an error when the task
// itself is missing.
func GetEventsForTask(db *sql.DB, shortID string) ([]EventEntry, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	rows, err := db.Query(`
		SELECT e.id, e.task_id, t.short_id, e.event_type, e.actor, e.detail, e.created_at
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.task_id = ?
		ORDER BY e.created_at DESC, e.id DESC
	`, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventEntry
	for rows.Next() {
		var e EventEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ShortID, &e.EventType, &e.Actor, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetBlocking returns tasks that this task blocks — i.e. the reverse
// direction of [GetBlockers]. Excludes done/deleted tasks so the
// detail view doesn't show edges that no longer matter.
func GetBlocking(db *sql.DB, shortID string) ([]*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	rows, err := db.Query(`
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_order,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM blocks b
		JOIN tasks t ON t.id = b.blocked_id
		WHERE b.blocker_id = ? AND t.status != 'done' AND t.deleted_at IS NULL
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

// GetTaskParent returns the parent of shortID (its direct parent,
// not ancestors), or nil if shortID is a root task.
func GetTaskParent(db *sql.DB, shortID string) (*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	if task.ParentID == nil {
		return nil, nil
	}
	rows, err := db.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_order,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE id = ? AND deleted_at IS NULL
	`, *task.ParentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	return scanTask(rows)
}

// DistinctActors returns unique actor names from the events table,
// sorted alphabetically. Used by the web dashboard's log view to
// populate the Actor filter chips.
func DistinctActors(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT actor FROM events WHERE actor <> '' ORDER BY actor`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// OpenTaskLabelFreqs returns each label's count of open tasks
// (status not in done/canceled, not soft-deleted). The dashboard's
// filter strips use this to cap their chip rows to the most-used
// labels currently in active circulation, matching what's actually
// useful as a filter rather than every label ever recorded.
func OpenTaskLabelFreqs(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`
		SELECT tl.name, COUNT(DISTINCT tl.task_id)
		FROM task_labels tl
		JOIN tasks t ON t.id = tl.task_id
		WHERE t.status NOT IN ('done', 'canceled')
		  AND t.deleted_at IS NULL
		GROUP BY tl.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, rows.Err()
}

// AllTaskLabelFreqs returns each label's count across all
// non-soft-deleted tasks regardless of status. The Log view uses this
// to populate its Label filter strip — the log is a historical event
// stream, so filtering by a label whose tasks have all closed is a
// perfectly valid query (where it would be noise on the live
// Home/Plan strips, which want only labels currently in flight).
func AllTaskLabelFreqs(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`
		SELECT tl.name, COUNT(DISTINCT tl.task_id)
		FROM task_labels tl
		JOIN tasks t ON t.id = tl.task_id
		WHERE t.deleted_at IS NULL
		GROUP BY tl.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, rows.Err()
}

// DistinctLabels returns unique label names from task_labels, sorted
// alphabetically. Used by the web dashboard's log view to populate
// the Label filter chips.
func DistinctLabels(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT DISTINCT name FROM task_labels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// TaskTitlesByID returns a title lookup for the given task IDs. The
// web dashboard joins events to titles in the log view's renderer.
func TaskTitlesByID(db *sql.DB, ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	var placeholders strings.Builder
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders.WriteString(",")
		}
		placeholders.WriteString("?")
		args[i] = id
	}
	rows, err := db.Query(
		`SELECT id, title FROM tasks WHERE id IN (`+placeholders.String()+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string, len(ids))
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out[id] = title
	}
	return out, rows.Err()
}
