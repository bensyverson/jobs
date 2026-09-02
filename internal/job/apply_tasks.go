package job

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The task family's state writes: created, edited, noted, done, reopened,
// canceled, purged, moved, reparented. The auto-release the add and reparent
// handlers emit is the claims family's applyReleased, in apply_claims.go.
//
// Every function here is reached only through apply, takes everything it
// needs from the envelope, and stamps every timestamp from the event's ts.
// None of them reads the clock, mints an id, or decides anything: a cascade
// arrives as its own event, already decided by the handler.

func applyCreated(tx dbtx, e eventlog.Envelope) error {
	var p CreatedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	if p.ShortID == "" {
		return fmt.Errorf("created event carries no short id")
	}
	var parentID any
	if p.ParentID != "" {
		id, ok, err := taskRowID(tx, p.ParentID)
		if err != nil {
			return err
		}
		if ok {
			parentID = id
		}
	}
	kind := p.Kind
	if kind == "" {
		kind = string(KindTask)
	}
	at := eventSeconds(e)
	// Idempotent by short id: the same created event replayed, or arriving
	// from two log files, must not make a second row.
	_, err := tx.Exec(`
		INSERT INTO tasks (short_id, parent_id, title, description, status, sort_key, created_at, updated_at, kind)
		VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?)
		ON CONFLICT(short_id) DO NOTHING`,
		p.ShortID, parentID, p.Title, p.Description, p.SortKey, at, at, kind,
	)
	return err
}

func applyEdited(tx dbtx, e eventlog.Envelope) error {
	var p EditedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	sets := []string{"updated_at = ?"}
	args := []any{eventSeconds(e)}
	if p.NewTitle != nil {
		sets = append(sets, "title = ?")
		args = append(args, *p.NewTitle)
	}
	if p.NewDesc != nil {
		sets = append(sets, "description = ?")
		args = append(args, *p.NewDesc)
	}
	args = append(args, e.Task)
	_, err := tx.Exec("UPDATE tasks SET "+strings.Join(sets, ", ")+" WHERE short_id = ?", args...)
	return err
}

// noted touches only updated_at: the note itself lives in the event, which is
// where `show` and the dashboard read it from.
func applyNoted(tx dbtx, e eventlog.Envelope) error {
	_, err := tx.Exec("UPDATE tasks SET updated_at = ? WHERE short_id = ?", eventSeconds(e), e.Task)
	return err
}

// applyDone closes a task. A done event has three shapes and they write
// different columns, which the payload already distinguishes: a cascaded
// descendant carries the parent that closed it, an auto-closed ancestor
// carries auto_closed, and the explicit target carries neither — it is the
// only one that owns a completion note.
func applyDone(tx dbtx, e eventlog.Envelope) error {
	var p DonePayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	if !isExplicitCloseTarget(p.AutoClosed, p.CascadeClosedByParent) {
		_, err := tx.Exec(
			"UPDATE tasks SET status = 'done', updated_at = ? WHERE short_id = ?",
			eventSeconds(e), e.Task,
		)
		return err
	}
	var note any
	if p.Note != "" {
		note = p.Note
	}
	_, err := tx.Exec(
		"UPDATE tasks SET status = 'done', completion_note = ?, updated_at = ? WHERE short_id = ?",
		note, eventSeconds(e), e.Task,
	)
	return err
}

// applyCanceled mirrors applyDone. Canceling releases a live claim — the work
// is not going to happen — except on an auto-closed ancestor, which never
// holds one to release: adding an open child auto-releases a claimed parent.
func applyCanceled(tx dbtx, e eventlog.Envelope) error {
	var p CanceledPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	if p.AutoClosed {
		_, err := tx.Exec(
			"UPDATE tasks SET status = 'canceled', updated_at = ? WHERE short_id = ?",
			eventSeconds(e), e.Task,
		)
		return err
	}
	_, err := tx.Exec(`
		UPDATE tasks SET status = 'canceled', claimed_by = NULL, claim_expires_at = NULL, updated_at = ?
		WHERE short_id = ?`, eventSeconds(e), e.Task)
	return err
}

func applyReopened(tx dbtx, e eventlog.Envelope) error {
	_, err := tx.Exec(`
		UPDATE tasks SET status = 'available', completion_note = NULL, updated_at = ?
		WHERE short_id = ?`, eventSeconds(e), e.Task)
	return err
}

func applyMoved(tx dbtx, e eventlog.Envelope) error {
	var p MovedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	// The event carries the key; placing a row is one column write and no
	// sibling is touched. That is the whole point of fractional keys.
	_, err := tx.Exec(
		"UPDATE tasks SET sort_key = ?, updated_at = ? WHERE short_id = ?",
		p.SortKey, eventSeconds(e), e.Task,
	)
	return err
}

func applyReparented(tx dbtx, e eventlog.Envelope) error {
	var p ReparentedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	var parentID any
	if p.NewParentID != "" {
		id, ok, err := taskRowID(tx, p.NewParentID)
		if err != nil {
			return err
		}
		if ok {
			parentID = id
		}
	}
	_, err := tx.Exec(
		"UPDATE tasks SET parent_id = ?, sort_key = ?, updated_at = ? WHERE short_id = ?",
		parentID, p.SortKey, eventSeconds(e), e.Task,
	)
	return err
}

// applyPurged erases a subtree. The envelope's task is where the tombstone is
// recorded — the purged task's parent, or nothing at all for a root — and the
// payload names what to erase.
//
// A purge that finds nothing is not an error: it is a tombstone arriving at a
// cache that never held the task, which is exactly what it should do there.
func applyPurged(tx dbtx, e eventlog.Envelope) error {
	var p PurgedPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	if p.PurgedID == "" {
		return fmt.Errorf("purged event names no task")
	}

	// Descendants are listed in pre-order; erase leaves first so the
	// parent_id chain is never dangling mid-delete.
	var rowIDs []int64
	for _, sid := range slices.Backward(p.CascadePurged) {
		if id, ok, err := taskRowID(tx, sid); err != nil {
			return err
		} else if ok {
			rowIDs = append(rowIDs, id)
		}
	}
	targetID, targetFound, err := taskRowID(tx, p.PurgedID)
	if err != nil {
		return err
	}
	if targetFound {
		rowIDs = append(rowIDs, targetID)
	}

	for _, id := range rowIDs {
		// found_in and blocks are deleted explicitly rather than left to ON
		// DELETE CASCADE, because PRAGMA foreign_keys=ON is set on one pooled
		// connection and may not be in force on this one.
		if _, err := tx.Exec("DELETE FROM events WHERE task_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM blocks WHERE blocker_id = ? OR blocked_id = ?", id, id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM found_in WHERE task_id = ? OR source_id = ?", id, id); err != nil {
			return err
		}
	}
	for _, id := range rowIDs {
		if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

// isExplicitCloseTarget reports whether a done or canceled event is the one
// the operator asked for, rather than a descendant the cascade swept or an
// ancestor the leaf frontier closed. Only the explicit target owns the
// completion note.
func isExplicitCloseTarget(autoClosed bool, cascadeClosedByParent string) bool {
	return !autoClosed && cascadeClosedByParent == ""
}
