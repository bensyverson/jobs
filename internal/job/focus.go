package job

import (
	"database/sql"
	"fmt"
)

// Focus is a per-actor pointer at the root tree the actor is working in. It
// exists so the no-argument defaults of next/claim/status/orient stay inside
// the active tree instead of resolving against the global frontier. The event
// store is the source of truth: focus_set / focus_released events, latest
// per actor, materialized on read by GetFocus. A focus whose root is done,
// canceled, or deleted reads as released without needing a tombstone event —
// which is also how auto-release on root completion falls out for free.

const (
	eventFocusSet      = "focus_set"
	eventFocusReleased = "focus_released"
)

// SetFocus points actor's focus at the given root task, emitting a focus_set
// event on it. Callers pass a resolved root (claim paths derive it via
// findTopAncestor); SetFocus does not re-derive it.
func SetFocus(db *sql.DB, rootShortID, actor string) error {
	root, err := GetTaskByShortID(db, rootShortID)
	if err != nil {
		return err
	}
	if root == nil {
		return fmt.Errorf("task %q not found", rootShortID)
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordEvent(tx, root.ID, eventFocusSet, actor, map[string]any{
		"root": root.ShortID, "title": root.Title,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// ReleaseFocus clears actor's focus by emitting focus_released on the
// currently focused root. Releasing with no live focus is a quiet no-op so
// callers don't have to pre-check.
func ReleaseFocus(db *sql.DB, actor string) error {
	current, err := GetFocus(db, actor)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordEvent(tx, current.ID, eventFocusReleased, actor, map[string]any{
		"root": current.ShortID,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// flipFocusOnClaim is the automatic focus setter: called inside every
// successful claim's transaction, it resolves the claimed task's root and
// emits focus_set when that root differs from the actor's current focus.
// Same-root claims are event-silent (last-claim-wins needs no re-assertion).
func flipFocusOnClaim(tx dbtx, task *Task, actor string) error {
	root, err := findTopAncestor(tx, task)
	if err != nil {
		return err
	}
	current, err := GetFocus(tx, actor)
	if err != nil {
		return err
	}
	if current != nil && current.ID == root.ID {
		return nil
	}
	return recordEvent(tx, root.ID, eventFocusSet, actor, map[string]any{
		"root": root.ShortID, "title": root.Title, "via": "claim", "claimed": task.ShortID,
	})
}

// releaseFocusOnRootClose emits focus_released for every actor whose live
// focus is the given root. Called inside the done/cancel transaction that
// closes a root. GetFocus would already read the closed root as released
// (staleness check); the explicit events exist so the shift is visible in
// the event stream (`job tail`) rather than only inferable.
func releaseFocusOnRootClose(tx dbtx, root *Task) error {
	rows, err := tx.Query(`
		SELECT actor, event_type, task_id
		FROM events
		WHERE event_type IN (?, ?)
		ORDER BY created_at, id
	`, eventFocusSet, eventFocusReleased)
	if err != nil {
		return err
	}
	defer rows.Close()

	type focusState struct {
		eventType string
		taskID    sql.NullInt64
	}
	latest := map[string]focusState{}
	for rows.Next() {
		var actor, eventType string
		var taskID sql.NullInt64
		if err := rows.Scan(&actor, &eventType, &taskID); err != nil {
			return err
		}
		latest[actor] = focusState{eventType: eventType, taskID: taskID}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for actor, state := range latest {
		if state.eventType != eventFocusSet || !state.taskID.Valid || state.taskID.Int64 != root.ID {
			continue
		}
		if err := recordEvent(tx, root.ID, eventFocusReleased, actor, map[string]any{
			"root": root.ShortID, "via": "root_closed",
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetFocus returns the actor's currently focused root task, or nil when no
// live focus exists. The latest focus event for the actor decides: a
// focus_released (or nothing) is no focus; a focus_set resolves through the
// tasks table and reads as released when the root is gone, deleted, or no
// longer open work (done/canceled).
func GetFocus(db dbtx, actor string) (*Task, error) {
	row := db.QueryRow(`
		SELECT event_type, task_id
		FROM events
		WHERE actor = ? AND event_type IN (?, ?)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, actor, eventFocusSet, eventFocusReleased)
	var eventType string
	var taskID sql.NullInt64
	if err := row.Scan(&eventType, &taskID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if eventType != eventFocusSet || !taskID.Valid {
		return nil, nil
	}

	root, err := getTaskByID(db, taskID.Int64)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Status == "done" || root.Status == "canceled" {
		return nil, nil
	}
	return root, nil
}
