package job

import (
	"database/sql"
	"fmt"
)

// Focus is a per-actor pointer at the root tree the actor is working in. It
// exists so the no-argument defaults of next/claim/status/orient stay inside
// the active tree instead of resolving against the global frontier.
//
// There is one focus slot per tree kind: a task focus and an issue focus,
// held independently, so triaging a bug never loses your place in the plan.
// The event store is the source of truth: focus_set / focus_released events
// carry the kind they apply to in their detail (`kind`), and resolution asks
// per kind — the latest focus_set of that kind not followed by a
// focus_released for it. The kind is recorded at set time because roots
// convert (`job kind`), and the event has to say what was true when it was
// written. A focus whose root is done, canceled, or deleted reads as released
// without needing a tombstone event — which is also how auto-release on root
// completion falls out for free.

const (
	eventFocusSet      = "focus_set"
	eventFocusReleased = "focus_released"
)

// focusKindExpr reads the kind slot a focus event belongs to. Events written
// before focus became per-kind carry no kind and belong to the task slot,
// which is what the single focus meant.
const focusKindExpr = `COALESCE(json_extract(detail, '$.kind'), 'task')`

// FocusKinds is every slot an actor can hold, in the order they are printed
// and released.
var FocusKinds = []TreeKind{KindTask, KindIssue}

// focusKindOf is the slot a root belongs to. The kind column is NOT NULL
// with a 'task' default, but a zero-value Task (or a hand-built one) would
// otherwise land in no slot at all.
func focusKindOf(root *Task) TreeKind {
	if root.Kind.IsIssue() {
		return KindIssue
	}
	return KindTask
}

// SetFocus points actor's focus at the given task's root, emitting a
// focus_set event on it, and returns that root. Any task in the tree may be
// named — focus is a property of the root, so the root is resolved here, and
// the root's kind decides which slot moves.
func SetFocus(db *sql.DB, shortID, actor string) (*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	root, err := findTopAncestor(tx, task)
	if err != nil {
		return nil, err
	}
	if err := recordEvent(tx, root.ID, eventFocusSet, actor, map[string]any{
		"root": root.ShortID, "title": root.Title, "kind": string(focusKindOf(root)),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return root, nil
}

// ReleaseFocusKind clears one of actor's focus slots by emitting
// focus_released on the root it pointed at, and returns that root. Releasing
// a slot with no live focus is a quiet no-op returning nil, so callers don't
// have to pre-check.
func ReleaseFocusKind(db *sql.DB, actor string, kind TreeKind) (*Task, error) {
	current, err := GetFocusKind(db, actor, kind)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := recordEvent(tx, current.ID, eventFocusReleased, actor, map[string]any{
		"root": current.ShortID, "kind": string(kind),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return current, nil
}

// ReleaseFocus clears every one of actor's focus slots.
func ReleaseFocus(db *sql.DB, actor string) error {
	for _, kind := range FocusKinds {
		if _, err := ReleaseFocusKind(db, actor, kind); err != nil {
			return err
		}
	}
	return nil
}

// flipFocusOnClaim is the automatic focus setter: called inside every
// successful claim's transaction, it resolves the claimed task's root and
// emits focus_set when that root differs from the actor's current focus *of
// that root's kind*. Claiming a bug therefore moves the issue focus and
// leaves the plan's focus where it was. Same-root claims are event-silent
// (last-claim-wins needs no re-assertion).
func flipFocusOnClaim(tx dbtx, task *Task, actor string) error {
	root, err := findTopAncestor(tx, task)
	if err != nil {
		return err
	}
	kind := focusKindOf(root)
	current, err := GetFocusKind(tx, actor, kind)
	if err != nil {
		return err
	}
	if current != nil && current.ID == root.ID {
		return nil
	}
	return recordEvent(tx, root.ID, eventFocusSet, actor, map[string]any{
		"root": root.ShortID, "title": root.Title, "kind": string(kind),
		"via": "claim", "claimed": task.ShortID,
	})
}

// releaseFocusOnRootClose emits focus_released for every actor whose live
// focus is the given root, in whichever slot holds it — closing an issue
// root never disturbs anyone's task focus. Called inside the done/cancel
// transaction that closes a root. GetFocusKind would already read the closed
// root as released (staleness check); the explicit events exist so the shift
// is visible in the event stream (`job tail`) rather than only inferable.
func releaseFocusOnRootClose(tx dbtx, root *Task) error {
	rows, err := tx.Query(`
		SELECT actor, `+focusKindExpr+`, event_type, task_id
		FROM events
		WHERE event_type IN (?, ?)
		ORDER BY created_at, id
	`, eventFocusSet, eventFocusReleased)
	if err != nil {
		return err
	}
	defer rows.Close()

	type slot struct {
		actor string
		kind  string
	}
	type focusState struct {
		eventType string
		taskID    sql.NullInt64
	}
	latest := map[slot]focusState{}
	var order []slot
	for rows.Next() {
		var s slot
		var eventType string
		var taskID sql.NullInt64
		if err := rows.Scan(&s.actor, &s.kind, &eventType, &taskID); err != nil {
			return err
		}
		if _, seen := latest[s]; !seen {
			order = append(order, s)
		}
		latest[s] = focusState{eventType: eventType, taskID: taskID}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, s := range order {
		state := latest[s]
		if state.eventType != eventFocusSet || !state.taskID.Valid || state.taskID.Int64 != root.ID {
			continue
		}
		if err := recordEvent(tx, root.ID, eventFocusReleased, s.actor, map[string]any{
			"root": root.ShortID, "kind": s.kind, "via": "root_closed",
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetFocus returns the actor's focused task-tree root, or nil when none is
// live. It is the task-kind accessor: `next`, `orient`, `claim --next` and
// `status` all answer "what is next in my plan", so the task slot is the one
// they read. Ask for the issue slot with GetFocusKind.
func GetFocus(db dbtx, actor string) (*Task, error) {
	return GetFocusKind(db, actor, KindTask)
}

// GetFocusKind returns the actor's currently focused root of one kind, or nil
// when no live focus exists in that slot. The latest focus event for the
// actor *in that slot* decides: a focus_released (or nothing) is no focus; a
// focus_set resolves through the tasks table and reads as released when the
// root is gone, deleted, or no longer open work (done/canceled).
func GetFocusKind(db dbtx, actor string, kind TreeKind) (*Task, error) {
	row := db.QueryRow(`
		SELECT event_type, task_id
		FROM events
		WHERE actor = ? AND event_type IN (?, ?) AND `+focusKindExpr+` = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, actor, eventFocusSet, eventFocusReleased, string(kind))
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
