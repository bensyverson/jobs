package job

import (
	"database/sql"
	"fmt"
	"strings"
)

// TreeKind classifies a root task. A task-tree is planned work: it is
// decomposed, it has a bottom, and it closes when its children close. An
// issue-tree is discovered work: open-ended, closing on evidence rather than
// on structure. The distinction exists so `next`, `orient` and the no-argument
// `claim` can answer "what is next in my plan" without surfacing bugs.
//
// Kind attaches to the root only. Children of an issue root are ordinary
// tasks — an issue owns task children directly, so it stays one object with
// one lifetime instead of an issue/PR pair.
type TreeKind string

const (
	// KindTask is the default for every new root.
	KindTask TreeKind = "task"
	// KindIssue marks a root as an issue-tree.
	KindIssue TreeKind = "issue"
)

// ParseTreeKind converts CLI input to a TreeKind, case- and space-insensitively.
func ParseTreeKind(s string) (TreeKind, error) {
	switch TreeKind(strings.ToLower(strings.TrimSpace(s))) {
	case KindTask:
		return KindTask, nil
	case KindIssue:
		return KindIssue, nil
	default:
		return "", fmt.Errorf("invalid kind %q (want task|issue)", s)
	}
}

// IsIssue reports whether k marks an issue-tree.
func (k TreeKind) IsIssue() bool { return k == KindIssue }

func (k TreeKind) String() string { return string(k) }

// Label renders k the way the CLI names it, article included: "a task-tree",
// "an issue-tree".
func (k TreeKind) Label() string {
	if k.IsIssue() {
		return "an issue-tree"
	}
	return "a task-tree"
}

// KindResult carries the outcome of RunSetKind. Changed is false when the
// root already had the requested kind — a quiet no-op that records no event,
// because nothing changed.
type KindResult struct {
	ShortID string
	Title   string
	From    TreeKind
	To      TreeKind
	Changed bool
}

// RunSetKind converts a root task between task-tree and issue-tree, recording
// a kind_changed event with the before/after. Nothing else about the tree is
// touched, so a conversion in either direction loses nothing.
//
// Setting a kind on a non-root is an error: only roots carry a meaningful
// kind, and silently accepting the write would leave a value that no reader
// consults.
func RunSetKind(db *sql.DB, shortID string, kind TreeKind, actor string) (*KindResult, error) {
	if kind != KindTask && kind != KindIssue {
		return nil, fmt.Errorf("invalid kind %q (want task|issue)", kind)
	}

	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	if task.ParentID != nil {
		return nil, fmt.Errorf(
			"tree kind is a property of the root only; %s is a child. Set the kind on its root instead.",
			shortID,
		)
	}

	res := &KindResult{ShortID: task.ShortID, Title: task.Title, From: task.Kind, To: kind}
	if task.Kind == kind {
		return res, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		"UPDATE tasks SET kind = ?, updated_at = ? WHERE id = ?",
		string(kind), CurrentNowFunc().Unix(), task.ID,
	); err != nil {
		return nil, err
	}
	if err := recordEvent(tx, task.ID, EventKindChanged, actor, KindChangedPayload{
		From: string(task.Kind),
		To:   string(kind),
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	res.Changed = true
	return res, nil
}
