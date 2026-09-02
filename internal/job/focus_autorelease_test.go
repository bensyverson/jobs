package job

import (
	"database/sql"
	"testing"
)

// iarXd — Focus auto-releases when its root closes, for every actor focused
// on it. Focus is machine-local state resolved through the tasks table, so
// the release is derived rather than recorded: a done or canceled root reads
// as no focus, and no event is written for it.

// focusOf is GetFocusKind's task slot as a short id, "" for no live focus.
func focusOf(t *testing.T, db *sql.DB, actor string) string {
	t.Helper()
	got, err := GetFocus(db, actor)
	if err != nil {
		t.Fatalf("GetFocus(%s): %v", actor, err)
	}
	if got == nil {
		return ""
	}
	return got.ShortID
}

// wjS — Cascade-closing a focused root releases focus for every actor
// focused on it.
func TestCascadeCloseRoot_ReleasesFocusForAllActors(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf1 := MustAdd(t, db, root, "Leaf 1")
	leaf2 := MustAdd(t, db, root, "Leaf 2")

	if err := RunClaim(db, leaf1, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	if err := RunClaim(db, leaf2, "1h", "", "bob", false); err != nil {
		t.Fatalf("bob claim: %v", err)
	}

	if _, _, err := RunDone(db, []string{leaf1}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("alice done: %v", err)
	}
	if focusOf(t, db, "alice") != root || focusOf(t, db, "bob") != root {
		t.Fatalf("both actors must still be focused on %s while it is open", root)
	}

	// Closing the last open leaf cascade-closes the root.
	if _, _, err := RunDone(db, []string{leaf2}, false, "", nil, "bob", false, ""); err != nil {
		t.Fatalf("bob done: %v", err)
	}

	for _, actor := range []string{"alice", "bob"} {
		if got := focusOf(t, db, actor); got != "" {
			t.Errorf("GetFocus(%s) after root cascade-close: got %s, want none", actor, got)
		}
		if n := focusEventCount(t, db, actor); n != 0 {
			t.Errorf("focus events for %s: got %d, want 0", actor, n)
		}
	}
}

// A direct `done` on a childless focused root releases it too.
func TestDirectDoneRoot_ReleasesFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Childless root")
	MustClaim(t, db, root, "1h")

	if _, _, err := RunDone(db, []string{root}, false, "wrapped", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if got := focusOf(t, db, TestActor); got != "" {
		t.Errorf("GetFocus after closing the focused root: got %s, want none", got)
	}
	if n := focusEventCount(t, db, TestActor); n != 0 {
		t.Errorf("focus events: got %d, want 0", n)
	}
}

// BHd — Canceling a focused root releases focus.
func TestCancelRoot_ReleasesFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	MustClaim(t, db, leaf, "1h")
	mustRelease(t, db, leaf)

	if _, _, _, err := RunCancel(db, []string{root}, "abandoned", true, false, true, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	if got := focusOf(t, db, TestActor); got != "" {
		t.Errorf("GetFocus after root cancel: got %s, want none", got)
	}
	if n := focusEventCount(t, db, TestActor); n != 0 {
		t.Errorf("focus events: got %d, want 0", n)
	}
}

// Closing a root nobody is focused on leaves every focus alone.
func TestCloseUnfocusedRoot_NoReleaseEvents(t *testing.T) {
	db := SetupTestDB(t)
	focusRoot := MustAdd(t, db, "", "Focused root")
	focusLeaf := MustAdd(t, db, focusRoot, "Focused leaf")
	otherRoot := MustAdd(t, db, "", "Other root")

	MustClaim(t, db, focusLeaf, "1h")
	if _, _, err := RunDone(db, []string{otherRoot}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done other root: %v", err)
	}

	if got := focusOf(t, db, TestActor); got != focusRoot {
		t.Errorf("focus must survive closing an unrelated root: got %q, want %s", got, focusRoot)
	}
}

// lXi9K — An issue root never auto-closes (it is open-ended by design), so
// closing the last open bug in an issue tree must not release anyone's issue
// focus.
func TestCascadeCloseIssueRoot_DoesNotReleaseFocus(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	bug := MustAdd(t, db, root.ShortID, "Bug")
	MustClaim(t, db, bug, "1h")

	got, err := GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind before done: %v", err)
	}
	if got == nil || got.ShortID != root.ShortID {
		t.Fatalf("issue focus before done = %v, want %s", got, root.ShortID)
	}

	MustDone(t, db, bug)

	got, err = GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind after done: %v", err)
	}
	if got == nil || got.ShortID != root.ShortID {
		t.Errorf("issue focus after cascade should survive (root stays open): got %v, want %s", got, root.ShortID)
	}
}
