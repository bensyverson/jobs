package job

import (
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// applySnapshot overwrites the state tables from a snapshot's payload.
//
// It is the one event that does not describe a change: everything the tables
// hold at this position in the order is replaced by everything the payload
// carries. Adoption writes one to carry a legacy database's state across, and
// `job compact` would write one to summarize archived files (backlog), so this
// stays self-contained — it reads nothing but the payload and the short ids
// already in the cache.
//
// The `events` table is deliberately untouched. Events are history, the log is
// the record of them, and a snapshot describes state; erasing history here
// would delete the very rows adoption exists to preserve.
//
// Tasks are reconciled rather than truncated. `events.task_id` references
// `tasks(id)` with no cascade, so emptying `tasks` outright fails the foreign
// key for every event already recorded against a task — and re-inserting could
// not restore the link, because an event row does not carry the short id it was
// resolved from. Upserting by short id keeps every existing row id, and so
// keeps every event pointing where it pointed.
func applySnapshot(tx dbtx, e eventlog.Envelope) error {
	var p SnapshotPayload
	if err := decodeEventPayload(e, &p); err != nil {
		return err
	}
	if err := upsertSnapshotTasks(tx, p.Tasks); err != nil {
		return err
	}
	if err := pruneSnapshotTasks(tx, p.Tasks); err != nil {
		return err
	}
	if err := linkSnapshotParents(tx, p.Tasks); err != nil {
		return err
	}
	return replaceSnapshotRelations(tx, p)
}

// upsertSnapshotTasks writes every task with a NULL parent, so no insert can
// depend on the order the payload happens to be in; linkSnapshotParents sets
// the parents once every row exists.
func upsertSnapshotTasks(tx dbtx, tasks []SnapshotTask) error {
	for _, t := range tasks {
		if t.ShortID == "" {
			return fmt.Errorf("snapshot task %q has no short id", t.Title)
		}
		_, err := tx.Exec(`
			INSERT INTO tasks (short_id, parent_id, title, description, status, sort_key,
			                   claimed_by, claim_expires_at, completion_note,
			                   created_at, updated_at, deleted_at, kind)
			VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(short_id) DO UPDATE SET
				parent_id = NULL, title = excluded.title, description = excluded.description,
				status = excluded.status, sort_key = excluded.sort_key,
				claimed_by = excluded.claimed_by, claim_expires_at = excluded.claim_expires_at,
				completion_note = excluded.completion_note, created_at = excluded.created_at,
				updated_at = excluded.updated_at, deleted_at = excluded.deleted_at,
				kind = excluded.kind`,
			t.ShortID, t.Title, t.Description, t.Status, t.SortKey,
			t.ClaimedBy, t.ClaimExpiresAt, t.CompletionNote,
			t.CreatedAt, t.UpdatedAt, t.DeletedAt, t.Kind,
		)
		if err != nil {
			return fmt.Errorf("snapshot task %s: %w", t.ShortID, err)
		}
	}
	return nil
}

// pruneSnapshotTasks removes every task the snapshot does not name. A task that
// is not in the state is a purged task, so its events go with it — that is what
// purge means, and applyPurged does the same.
func pruneSnapshotTasks(tx dbtx, tasks []SnapshotTask) error {
	keep := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		keep[t.ShortID] = true
	}
	rows, err := tx.Query("SELECT id, short_id FROM tasks")
	if err != nil {
		return err
	}
	var doomed []int64
	for rows.Next() {
		var id int64
		var short string
		if err := rows.Scan(&id, &short); err != nil {
			rows.Close()
			return err
		}
		if !keep[short] {
			doomed = append(doomed, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range doomed {
		if _, err := tx.Exec("DELETE FROM events WHERE task_id = ?", id); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM tasks WHERE id = ?", id); err != nil {
			return err
		}
	}
	return nil
}

func linkSnapshotParents(tx dbtx, tasks []SnapshotTask) error {
	for _, t := range tasks {
		if t.ParentID == "" {
			continue
		}
		parent, ok, err := taskRowID(tx, t.ParentID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("snapshot task %s names a parent %s the snapshot does not carry", t.ShortID, t.ParentID)
		}
		if _, err := tx.Exec("UPDATE tasks SET parent_id = ? WHERE short_id = ?", parent, t.ShortID); err != nil {
			return err
		}
	}
	return nil
}

// replaceSnapshotRelations empties and refills every table that hangs off
// tasks. Nothing references these rows, so a truncate is safe where the tasks
// table needs a reconcile.
func replaceSnapshotRelations(tx dbtx, p SnapshotPayload) error {
	for _, table := range []string{"found_in", "task_criteria", "task_labels", "blocks", "users"} {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("snapshot: clear %s: %w", table, err)
		}
	}

	for _, b := range p.Blocks {
		blocker, blocked, err := snapshotPair(tx, b.BlockerID, b.BlockedID)
		if err != nil {
			return fmt.Errorf("snapshot block %s->%s: %w", b.BlockerID, b.BlockedID, err)
		}
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)",
			blocker, blocked, b.CreatedAt,
		); err != nil {
			return err
		}
	}
	for _, l := range p.Labels {
		id, err := snapshotTaskID(tx, l.TaskID)
		if err != nil {
			return fmt.Errorf("snapshot label %s on %s: %w", l.Name, l.TaskID, err)
		}
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO task_labels (task_id, name, created_at) VALUES (?, ?, ?)",
			id, l.Name, l.CreatedAt,
		); err != nil {
			return err
		}
	}
	for _, c := range p.Criteria {
		id, err := snapshotTaskID(tx, c.TaskID)
		if err != nil {
			return fmt.Errorf("snapshot criterion %q on %s: %w", c.Label, c.TaskID, err)
		}
		if _, err := tx.Exec(`
			INSERT INTO task_criteria (task_id, short_id, label, state, sort_key, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, c.ShortID, c.Label, c.State, c.SortKey, c.CreatedAt, c.UpdatedAt,
		); err != nil {
			return err
		}
	}
	for _, f := range p.FoundIn {
		task, source, err := snapshotPair(tx, f.TaskID, f.SourceID)
		if err != nil {
			return fmt.Errorf("snapshot found_in %s in %s: %w", f.TaskID, f.SourceID, err)
		}
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO found_in (task_id, source_id, created_at) VALUES (?, ?, ?)",
			task, source, f.CreatedAt,
		); err != nil {
			return err
		}
	}
	for _, u := range p.Users {
		if _, err := tx.Exec(
			"INSERT OR IGNORE INTO users (name, created_at) VALUES (?, ?)", u.Name, u.CreatedAt,
		); err != nil {
			return err
		}
	}
	return nil
}

func snapshotTaskID(tx dbtx, short string) (int64, error) {
	id, ok, err := taskRowID(tx, short)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("the snapshot does not carry task %s", short)
	}
	return id, nil
}

func snapshotPair(tx dbtx, a, b string) (int64, int64, error) {
	x, err := snapshotTaskID(tx, a)
	if err != nil {
		return 0, 0, err
	}
	y, err := snapshotTaskID(tx, b)
	if err != nil {
		return 0, 0, err
	}
	return x, y, nil
}
