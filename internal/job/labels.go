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

// splitLabelsByPresence reports which of names are already attached to
// taskID and which are not, preserving input order. Handlers need this
// before they emit: applyLabeled and applyUnlabeled are idempotent, so the
// write cannot be what tells the caller what changed.
func splitLabelsByPresence(tx dbtx, taskID int64, names []string) (present, absent []string, err error) {
	for _, name := range names {
		var exists bool
		if err := tx.QueryRow(
			"SELECT EXISTS(SELECT 1 FROM task_labels WHERE task_id = ? AND name = ?)",
			taskID, name,
		).Scan(&exists); err != nil {
			return nil, nil, err
		}
		if exists {
			present = append(present, name)
		} else {
			absent = append(absent, name)
		}
	}
	return present, absent, nil
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

	result := &LabelResult{ShortID: shortID}
	err = commit(db, func(tx dbtx, b *eventBatch) error {
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		task, err := GetTaskByShortID(tx, shortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", shortID)
		}

		existing, added, err := splitLabelsByPresence(tx, task.ID, normalized)
		if err != nil {
			return err
		}
		result.Added, result.Existing = added, existing

		if len(added) > 0 {
			if err := b.emit(tx, EventLabeled, shortID, actor, LabeledPayload{
				Names:    normalized,
				Existing: ensureStringSlice(existing),
			}); err != nil {
				return err
			}
		}
		// The claims family owns maybeExtendClaim; it still writes the claim
		// column directly (leaf for agent-claims).
		return maybeExtendClaim(tx, b, task.ShortID, actor)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func RunLabelRemove(db *sql.DB, shortID string, names []string, actor string) (*UnlabelResult, error) {
	normalized, err := normalizeLabelNames(names)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("label name is empty")
	}

	result := &UnlabelResult{ShortID: shortID}
	err = commit(db, func(tx dbtx, b *eventBatch) error {
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		task, err := GetTaskByShortID(tx, shortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", shortID)
		}

		removed, absent, err := splitLabelsByPresence(tx, task.ID, normalized)
		if err != nil {
			return err
		}
		result.Removed, result.Absent = removed, absent

		if len(removed) > 0 {
			if err := b.emit(tx, EventUnlabeled, shortID, actor, UnlabeledPayload{
				Names:  normalized,
				Absent: ensureStringSlice(absent),
			}); err != nil {
				return err
			}
		}
		return maybeExtendClaim(tx, b, task.ShortID, actor)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ensureStringSlice returns a non-nil slice so the recorded JSON detail
// surfaces an empty array rather than a `null` field.
func ensureStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
