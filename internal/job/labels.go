package job

import (
	"database/sql"
	"fmt"
	"strings"
)

type LabelResult struct {
	ShortID  string
	Added    []string
	Existing []string
}

type UnlabelResult struct {
	ShortID string
	Removed []string
	Absent  []string
}

// validateLabelName trims surrounding whitespace and rejects empty names
// or names containing the comma reserved for future multi-label flags.
func validateLabelName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("label name is empty")
	}
	if strings.Contains(name, ",") {
		return "", fmt.Errorf("label name %q may not contain ','", name)
	}
	return name, nil
}

// normalizeLabelNames validates and dedupes (case-sensitive) a list of names,
// preserving first-seen order.
func normalizeLabelNames(raw []string) ([]string, error) {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		name, err := validateLabelName(r)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// insertLabels inserts each name for taskID using INSERT OR IGNORE and
// returns the names that were actually inserted (added) versus already
// present (existing). Order matches the input order.
func insertLabels(tx dbtx, taskID int64, names []string) (added, existing []string, err error) {
	for _, name := range names {
		res, err := tx.Exec(
			"INSERT OR IGNORE INTO task_labels (task_id, name) VALUES (?, ?)",
			taskID, name,
		)
		if err != nil {
			return nil, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			added = append(added, name)
		} else {
			existing = append(existing, name)
		}
	}
	return added, existing, nil
}

// deleteLabels removes each name for taskID and returns the names that were
// actually present (removed) versus absent.
func deleteLabels(tx dbtx, taskID int64, names []string) (removed, absent []string, err error) {
	for _, name := range names {
		res, err := tx.Exec(
			"DELETE FROM task_labels WHERE task_id = ? AND name = ?",
			taskID, name,
		)
		if err != nil {
			return nil, nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, nil, err
		}
		if n > 0 {
			removed = append(removed, name)
		} else {
			absent = append(absent, name)
		}
	}
	return removed, absent, nil
}

// GetLabels returns the labels attached to taskID, sorted alphabetically
// for deterministic display.
func GetLabels(tx dbtx, taskID int64) ([]string, error) {
	rows, err := tx.Query(
		"SELECT name FROM task_labels WHERE task_id = ? ORDER BY name",
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// GetLabelsForTaskIDs returns a map task_id -> sorted labels for the given ids.
func GetLabelsForTaskIDs(db *sql.DB, taskIDs []int64) (map[int64][]string, error) {
	out := make(map[int64][]string, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(taskIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		args = append(args, id)
	}
	rows, err := db.Query(
		"SELECT task_id, name FROM task_labels WHERE task_id IN ("+placeholders+") ORDER BY task_id, name",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var name string
		if err := rows.Scan(&taskID, &name); err != nil {
			return nil, err
		}
		out[taskID] = append(out[taskID], name)
	}
	return out, rows.Err()
}

func RunLabelAdd(db *sql.DB, shortID string, names []string, actor string) (*LabelResult, error) {
	normalized, err := normalizeLabelNames(names)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("label name is empty")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	added, existing, err := insertLabels(tx, task.ID, normalized)
	if err != nil {
		return nil, err
	}

	if len(added) > 0 {
		if err := recordEvent(tx, task.ID, EventLabeled, actor, LabeledPayload{
			Names:    normalized,
			Existing: ensureStringSlice(existing),
		}); err != nil {
			return nil, err
		}
	}

	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &LabelResult{
		ShortID:  shortID,
		Added:    added,
		Existing: existing,
	}, nil
}

func RunLabelRemove(db *sql.DB, shortID string, names []string, actor string) (*UnlabelResult, error) {
	normalized, err := normalizeLabelNames(names)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("label name is empty")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	removed, absent, err := deleteLabels(tx, task.ID, normalized)
	if err != nil {
		return nil, err
	}

	if len(removed) > 0 {
		if err := recordEvent(tx, task.ID, EventUnlabeled, actor, UnlabeledPayload{
			Names:  normalized,
			Absent: ensureStringSlice(absent),
		}); err != nil {
			return nil, err
		}
	}

	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &UnlabelResult{
		ShortID: shortID,
		Removed: removed,
		Absent:  absent,
	}, nil
}

// ensureStringSlice returns a non-nil slice so the recorded JSON detail
// surfaces an empty array rather than a `null` field.
func ensureStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
