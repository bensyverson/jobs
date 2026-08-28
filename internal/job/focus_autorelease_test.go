package job

import (
	"testing"
)

// iarXd — Focus auto-releases when its root closes. GetFocus already reads a
// done/canceled root as released (implicit release); these tests pin the
// explicit focus_released events emitted so the shift is visible in the
// event stream (`job tail`) for every actor focused on the closing root.

func focusReleasedCount(t *testing.T, db dbtx, actor string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type = 'focus_released' AND actor = ?", actor,
	).Scan(&n); err != nil {
		t.Fatalf("count focus_released for %s: %v", actor, err)
	}
	return n
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
	if focusReleasedCount(t, db, "alice") != 0 || focusReleasedCount(t, db, "bob") != 0 {
		t.Fatalf("no focus_released expected while the root is still open")
	}

	// Closing the last open leaf cascade-closes the root.
	if _, _, err := RunDone(db, []string{leaf2}, false, "", nil, "bob", false, ""); err != nil {
		t.Fatalf("bob done: %v", err)
	}

	for _, actor := range []string{"alice", "bob"} {
		got, err := GetFocus(db, actor)
		if err != nil {
			t.Fatalf("GetFocus(%s): %v", actor, err)
		}
		if got != nil {
			t.Errorf("GetFocus(%s) after root cascade-close: got %s, want nil", actor, got.ShortID)
		}
		if n := focusReleasedCount(t, db, actor); n != 1 {
			t.Errorf("focus_released events for %s: got %d, want 1", actor, n)
		}
	}
}

// A direct `done` on a childless focused root also emits the release.
func TestDirectDoneRoot_ReleasesFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Childless root")
	MustClaim(t, db, root, "1h")

	if _, _, err := RunDone(db, []string{root}, false, "wrapped", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if n := focusReleasedCount(t, db, TestActor); n != 1 {
		t.Errorf("focus_released events: got %d, want 1", n)
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

	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got != nil {
		t.Errorf("GetFocus after root cancel: got %s, want nil", got.ShortID)
	}
	if n := focusReleasedCount(t, db, TestActor); n != 1 {
		t.Errorf("focus_released events: got %d, want 1", n)
	}
}

// Closing a root nobody is focused on emits nothing.
func TestCloseUnfocusedRoot_NoReleaseEvents(t *testing.T) {
	db := SetupTestDB(t)
	focusRoot := MustAdd(t, db, "", "Focused root")
	focusLeaf := MustAdd(t, db, focusRoot, "Focused leaf")
	otherRoot := MustAdd(t, db, "", "Other root")

	MustClaim(t, db, focusLeaf, "1h")
	if _, _, err := RunDone(db, []string{otherRoot}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done other root: %v", err)
	}

	if n := focusReleasedCount(t, db, TestActor); n != 0 {
		t.Errorf("focus_released events after closing an unfocused root: got %d, want 0", n)
	}
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != focusRoot {
		t.Errorf("focus must survive closing an unrelated root: got %v, want %s", got, focusRoot)
	}
}

// lXi9K — An issue root never auto-closes (it is open-ended by design), so
// the cascade must never reach releaseFocusOnRootClose for it: closing the
// last open bug in an issue tree must not release anyone's issue focus.
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

	if n := focusReleasedCount(t, db, TestActor); n != 0 {
		t.Errorf("focus_released events after closing an issue root's last child: got %d, want 0", n)
	}
	got, err = GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind after done: %v", err)
	}
	if got == nil || got.ShortID != root.ShortID {
		t.Errorf("issue focus after cascade should survive (root stays open): got %v, want %s", got, root.ShortID)
	}
}
