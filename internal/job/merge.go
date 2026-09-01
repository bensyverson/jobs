package job

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Merging two databases that diverged.
//
// A `.jobs.db` that was copied and then written on both machines has two
// histories sharing one prefix. `job merge` folds the other file's tail into
// this one. It works on *state* with events as evidence: the shared prefix
// only proves the two files are the same database, and the merge itself is
// keyed on short ids, which are the sole stable identity across two SQLite
// files (row ids are per-file and are never copied).
//
// The other file is opened from a staged copy, so a merge — dry run or not —
// cannot write to it even indirectly through a migration.

// ---------------------------------------------------------------------------
// apply
// ---------------------------------------------------------------------------

func applyMergePlan(db *sql.DB, plan *mergePlan) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// New tasks land parentless so a parent that is itself new can be
	// linked afterwards; row ids are per-database and are never copied.
	for _, t := range plan.insertTasks {
		if _, err := tx.Exec(`
			INSERT INTO tasks (short_id, parent_id, title, description, status, sort_order,
			                   claimed_by, claim_expires_at, completion_note,
			                   created_at, updated_at, deleted_at, kind)
			VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.shortID, t.title, t.description, t.status, t.sortOrder,
			t.claimedBy, t.claimExpiresAt, t.completionNote,
			t.createdAt, t.updatedAt, t.deletedAt, t.kind); err != nil {
			return fmt.Errorf("insert task %s: %w", t.shortID, err)
		}
	}
	for _, t := range append(append([]*mergeTaskRow{}, plan.insertTasks...), plan.updateTasks...) {
		if _, err := tx.Exec(`
			UPDATE tasks SET parent_id = (SELECT id FROM tasks WHERE short_id = ?),
			                 title = ?, description = ?, status = ?, sort_order = ?,
			                 claimed_by = ?, claim_expires_at = ?, completion_note = ?,
			                 created_at = ?, updated_at = ?, deleted_at = ?, kind = ?
			WHERE short_id = ?`,
			nullIfEmpty(t.parentShortID), t.title, t.description, t.status, t.sortOrder,
			t.claimedBy, t.claimExpiresAt, t.completionNote,
			t.createdAt, t.updatedAt, t.deletedAt, t.kind, t.shortID); err != nil {
			return fmt.Errorf("update task %s: %w", t.shortID, err)
		}
	}

	for _, l := range plan.labels {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO task_labels (task_id, name, created_at)
			SELECT id, ?, ? FROM tasks WHERE short_id = ?`, l.name, l.createdAt, l.taskShortID); err != nil {
			return fmt.Errorf("insert label %s on %s: %w", l.name, l.taskShortID, err)
		}
	}

	for _, b := range plan.blocks {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at)
			SELECT br.id, bd.id, ? FROM tasks br, tasks bd
			WHERE br.short_id = ? AND bd.short_id = ?`,
			b.createdAt, b.key.blocker, b.key.blocked); err != nil {
			return fmt.Errorf("insert block %s->%s: %w", b.key.blocker, b.key.blocked, err)
		}
	}

	for _, c := range plan.insertCriteria {
		if _, err := tx.Exec(`
			INSERT INTO task_criteria (task_id, short_id, label, state, sort_order, created_at, updated_at)
			SELECT id, ?, ?, ?, ?, ?, ? FROM tasks WHERE short_id = ?`,
			c.shortID, c.label, c.state, c.sortOrder, c.createdAt, c.updatedAt, c.taskShortID); err != nil {
			return fmt.Errorf("insert criterion on %s: %w", c.taskShortID, err)
		}
	}
	for _, c := range plan.updateCriteria {
		// Criteria are addressed by short id where they have one; a row old
		// enough to have none is addressed by its task and label, which is
		// the same fallback its cross-database key uses.
		var err error
		if c.shortID.Valid && c.shortID.String != "" {
			_, err = tx.Exec(`
				UPDATE task_criteria SET label = ?, state = ?, sort_order = ?, updated_at = ?
				WHERE short_id = ? AND task_id = (SELECT id FROM tasks WHERE short_id = ?)`,
				c.label, c.state, c.sortOrder, c.updatedAt, c.shortID.String, c.taskShortID)
		} else {
			_, err = tx.Exec(`
				UPDATE task_criteria SET state = ?, sort_order = ?, updated_at = ?
				WHERE label = ? AND task_id = (SELECT id FROM tasks WHERE short_id = ?)`,
				c.state, c.sortOrder, c.updatedAt, c.label, c.taskShortID)
		}
		if err != nil {
			return fmt.Errorf("update criterion on %s: %w", c.taskShortID, err)
		}
	}

	for _, f := range plan.foundIn {
		if _, err := tx.Exec(`
			INSERT INTO found_in (task_id, source_id, created_at)
			SELECT t.id, s.id, ? FROM tasks t, tasks s WHERE t.short_id = ? AND s.short_id = ?
			ON CONFLICT(task_id) DO UPDATE SET source_id = excluded.source_id, created_at = excluded.created_at`,
			f.createdAt, f.taskShortID, f.sourceShortID); err != nil {
			return fmt.Errorf("insert found_in for %s: %w", f.taskShortID, err)
		}
	}

	for _, u := range plan.users {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users (name, created_at) VALUES (?, ?)`,
			u.name, u.createdAt); err != nil {
			return fmt.Errorf("insert user %s: %w", u.name, err)
		}
	}

	// Events are copied verbatim, not re-recorded: merge transcribes
	// history rather than making it, so no actor is invented for them and
	// the merge itself leaves no event behind.
	for _, e := range plan.events {
		if _, err := tx.Exec(`
			INSERT INTO events (task_id, event_type, actor, detail, created_at)
			VALUES ((SELECT id FROM tasks WHERE short_id = ?), ?, ?, ?, ?)`,
			nullIfEmpty(e.taskShortID), e.eventType, e.actor, e.detail, e.createdAt); err != nil {
			return fmt.Errorf("insert event %s: %w", e.eventType, err)
		}
	}

	return tx.Commit()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------------------
// entry point
// ---------------------------------------------------------------------------

// RunMerge folds the database at otherPath into db. The other file is never
// written: it is staged to a temporary copy first, so even the migration that
// opening it may run lands on the copy. With dryRun the report is computed
// and returned but nothing is written anywhere.
func RunMerge(db *sql.DB, otherPath string, dryRun bool) (*MergeReport, error) {
	info, err := os.Stat(otherPath)
	if err != nil {
		return nil, fmt.Errorf("merge: cannot read %s: %w", otherPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("merge: %s is a directory, not a job database", otherPath)
	}

	staged, cleanup, err := stageDatabaseCopy(otherPath)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	otherDB, err := OpenDB(staged)
	if err != nil {
		return nil, fmt.Errorf("merge: open %s: %w", otherPath, err)
	}
	defer otherDB.Close()

	localSnap, err := readMergeSnapshot(db)
	if err != nil {
		return nil, err
	}
	otherSnap, err := readMergeSnapshot(otherDB)
	if err != nil {
		return nil, err
	}

	prefix, err := commonEventPrefix(localSnap.events, otherSnap.events)
	if err != nil {
		return nil, err
	}

	report := &MergeReport{
		OtherPath:       otherPath,
		DryRun:          dryRun,
		SharedEvents:    prefix,
		LocalTailEvents: len(localSnap.events) - prefix,
		OtherTailEvents: len(otherSnap.events) - prefix,
	}
	plan := planMerge(localSnap, otherSnap, CurrentNowFunc().Unix(), report)

	if dryRun || plan.empty() {
		return report, nil
	}
	if err := applyMergePlan(db, plan); err != nil {
		return nil, fmt.Errorf("merge: %w", err)
	}
	return report, nil
}

// stageDatabaseCopy copies a database and its WAL sidecars somewhere
// disposable. Opening a database runs migrations, so reading the other file
// in place could write to it; a merge must be able to promise it did not.
func stageDatabaseCopy(path string) (staged string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "job-merge-")
	if err != nil {
		return "", func() {}, fmt.Errorf("merge: stage %s: %w", path, err)
	}
	cleanup = func() { os.RemoveAll(dir) }
	staged = filepath.Join(dir, "other.jobs.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFileIfPresent(path+suffix, staged+suffix); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("merge: stage %s: %w", path, err)
		}
	}
	return staged, cleanup, nil
}

func copyFileIfPresent(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
