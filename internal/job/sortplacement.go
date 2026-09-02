package job

import "fmt"

// Placing a row among its siblings.
//
// Every writer computes a sort key at command time and carries it in its
// event; applying that event is one column write. Nothing here ever updates
// a sibling — that is the whole point of fractional keys, and the reason two
// machines can insert under one parent without colliding.
//
// The neighbour queries deliberately ignore deleted_at: a soft-deleted row
// still holds its key, and stepping over it would let a new key duplicate
// one that is still on disk.

// noSortKeyExclusion is passed as excludeID when the row being placed is new
// and therefore cannot be its own neighbour. Row ids are positive.
const noSortKeyExclusion = int64(-1)

// appendSortKey returns a key that sorts after every existing child of
// parentID (or after every root when it is nil).
func appendSortKey(tx dbtx, parentID *int64, excludeID int64) (string, error) {
	var last string
	q := "SELECT COALESCE(MAX(sort_key), '') FROM tasks WHERE " + parentFilterSQL(parentID) + " AND id != ?"
	if err := tx.QueryRow(q, parentFilterArgs(parentID, excludeID)...).Scan(&last); err != nil {
		return "", err
	}
	return KeyBetween(last, "")
}

// sortKeyBeforeSibling returns a key that places a row immediately before
// relative among relative's siblings.
func sortKeyBeforeSibling(tx dbtx, parentID *int64, relative *Task, excludeID int64) (string, error) {
	if relative.SortKey == "" {
		return "", fmt.Errorf("task %s has no sort key", relative.ShortID)
	}
	var prev string
	q := "SELECT COALESCE(MAX(sort_key), '') FROM tasks WHERE " + parentFilterSQL(parentID) +
		" AND sort_key < ? AND id != ?"
	args := append(parentFilterArgs(parentID), relative.SortKey, excludeID)
	if err := tx.QueryRow(q, args...).Scan(&prev); err != nil {
		return "", err
	}
	return KeyBetween(prev, relative.SortKey)
}

// sortKeyAfterSibling returns a key that places a row immediately after
// relative among relative's siblings.
func sortKeyAfterSibling(tx dbtx, parentID *int64, relative *Task, excludeID int64) (string, error) {
	if relative.SortKey == "" {
		return "", fmt.Errorf("task %s has no sort key", relative.ShortID)
	}
	var next string
	q := "SELECT COALESCE(MIN(sort_key), '') FROM tasks WHERE " + parentFilterSQL(parentID) +
		" AND sort_key > ? AND id != ?"
	args := append(parentFilterArgs(parentID), relative.SortKey, excludeID)
	if err := tx.QueryRow(q, args...).Scan(&next); err != nil {
		return "", err
	}
	return KeyBetween(relative.SortKey, next)
}

func parentFilterSQL(parentID *int64) string {
	if parentID == nil {
		return "parent_id IS NULL"
	}
	return "parent_id = ?"
}

func parentFilterArgs(parentID *int64, rest ...any) []any {
	var args []any
	if parentID != nil {
		args = append(args, *parentID)
	}
	return append(args, rest...)
}
