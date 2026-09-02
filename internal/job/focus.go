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
// Focus is machine-local workflow state, so it lives in .jobs/local.json
// beside the cache (see local.go) — not in the events table, which is the
// shared record every other replica reads. The slot a focus occupies is
// decided at set time from the root's kind, because roots convert
// (`job kind`) and the stored pointer has to say what was true when it was
// written. A focus whose root is done, canceled, or deleted reads as released
// without needing a tombstone — which is also how auto-release on root
// completion falls out for free, with no event and no write.

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

// SetFocus points actor's focus at the given task's root and returns that
// root. Any task in the tree may be named — focus is a property of the root,
// so the root is resolved here, and the root's kind decides which slot moves.
func SetFocus(db *sql.DB, shortID, actor string) (*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	root, err := findTopAncestor(db, task)
	if err != nil {
		return nil, err
	}
	if err := updateLocal(db, func(s *LocalState) error {
		s.SetFocusRoot(actor, focusKindOf(root), root.ShortID)
		return nil
	}); err != nil {
		return nil, err
	}
	return root, nil
}

// ReleaseFocusKind clears one of actor's focus slots and returns the root it
// pointed at. Releasing a slot with no live focus is a quiet no-op returning
// nil, so callers don't have to pre-check.
func ReleaseFocusKind(db *sql.DB, actor string, kind TreeKind) (*Task, error) {
	current, err := GetFocusKind(db, actor, kind)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, nil
	}
	if err := updateLocal(db, func(s *LocalState) error {
		s.ClearFocusRoot(actor, kind)
		return nil
	}); err != nil {
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

// flipFocusOnClaim is the automatic focus setter: called after every
// successful claim commits, it resolves the claimed task's root and moves the
// actor's focus *of that root's kind* when it differs. Claiming a bug
// therefore moves the issue focus and leaves the plan's focus where it was.
// A same-root claim writes nothing (last-claim-wins needs no re-assertion).
//
// It runs outside the claim's transaction because it writes a file under the
// store lock: taking that lock while holding a SQLite write transaction would
// invert the lock order every other writer uses.
func flipFocusOnClaim(db *sql.DB, task *Task, actor string) error {
	root, err := findTopAncestor(db, task)
	if err != nil {
		return err
	}
	kind := focusKindOf(root)
	current, err := GetFocusKind(db, actor, kind)
	if err != nil {
		return err
	}
	if current != nil && current.ID == root.ID {
		return nil
	}
	return updateLocal(db, func(s *LocalState) error {
		s.SetFocusRoot(actor, kind, root.ShortID)
		return nil
	})
}

// GetFocus returns the actor's focused task-tree root, or nil when none is
// live. It is the task-kind accessor: `next`, `orient`, `claim --next` and
// `status` all answer "what is next in my plan", so the task slot is the one
// they read. Ask for the issue slot with GetFocusKind.
func GetFocus(db dbtx, actor string) (*Task, error) {
	return GetFocusKind(db, actor, KindTask)
}

// GetFocusKind returns the actor's currently focused root of one kind, or nil
// when no live focus exists in that slot. The slot in local.json holds a root
// short id; it resolves through the tasks table and reads as released when the
// root is gone, deleted, or no longer open work (done/canceled).
func GetFocusKind(db dbtx, actor string, kind TreeKind) (*Task, error) {
	state, err := loadLocal(db)
	if err != nil {
		return nil, err
	}
	shortID := state.FocusRoot(actor, kind)
	if shortID == "" {
		return nil, nil
	}
	root, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Status == "done" || root.Status == "canceled" {
		return nil, nil
	}
	return root, nil
}
