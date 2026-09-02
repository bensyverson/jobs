package job

import (
	"testing"
)

// eyd54 — Claiming is the focus setter (last-claim-wins). Every successful
// claim resolves the claimed task's root and emits focus_set for the actor
// when it differs from their current focus; claims inside the focused root
// are event-silent. Plain done/note/release never touch focus.

// j7N — Claiming a task in a different root flips the actor's focus to that
// root.
func TestClaim_DifferentRoot_FlipsFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "Leaf A")
	rootB := MustAdd(t, db, "", "Root B")
	leafB := MustAdd(t, db, rootB, "Leaf B")

	MustClaim(t, db, leafA, "1h")
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != rootA {
		t.Fatalf("focus after first claim: got %v, want %s", got, rootA)
	}

	MustClaim(t, db, leafB, "1h")
	got, err = GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != rootB {
		t.Errorf("focus after cross-root claim: got %v, want %s (last claim wins)", got, rootB)
	}
}

// wOb — Claiming within the focused root leaves the focus where it is.
func TestClaim_SameRoot_LeavesTheFocusAlone(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf1 := MustAdd(t, db, root, "Leaf 1")
	leaf2 := MustAdd(t, db, root, "Leaf 2")

	MustClaim(t, db, leaf1, "1h")
	MustClaim(t, db, leaf2, "1h")

	if slot := localFocusSlot(t, db, TestActor, KindTask); slot != root {
		t.Errorf("task slot after two same-root claims: got %q, want %q", slot, root)
	}
	if n := focusEventCount(t, db, TestActor); n != 0 {
		t.Errorf("focus events recorded: got %d, want 0", n)
	}
}

// bfS — A second actor's claim does not move the first actor's focus.
func TestClaim_SecondActor_DoesNotMoveFirstActorsFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "Leaf A")
	rootB := MustAdd(t, db, "", "Root B")
	leafB := MustAdd(t, db, rootB, "Leaf B")

	if err := RunClaim(db, leafA, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	if err := RunClaim(db, leafB, "1h", "", "bob", false); err != nil {
		t.Fatalf("bob claim: %v", err)
	}

	alice, err := GetFocus(db, "alice")
	if err != nil {
		t.Fatalf("GetFocus(alice): %v", err)
	}
	if alice == nil || alice.ShortID != rootA {
		t.Errorf("alice's focus after bob's claim: got %v, want %s", alice, rootA)
	}
	bob, err := GetFocus(db, "bob")
	if err != nil {
		t.Fatalf("GetFocus(bob): %v", err)
	}
	if bob == nil || bob.ShortID != rootB {
		t.Errorf("bob's focus: got %v, want %s", bob, rootB)
	}
}

// The claim inside claim-next flips focus the same way an explicit claim
// does (it funnels through the same path — this pins the contract).
func TestClaimNext_FlipsFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")

	task, err := RunClaimNext(db, "", "1h", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if task.ShortID != leaf {
		t.Fatalf("precondition: claimed %s, want %s", task.ShortID, leaf)
	}
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != root {
		t.Errorf("focus after claim-next: got %v, want %s", got, root)
	}
}

// Plain done, note, and release never touch focus.
func TestDoneNoteRelease_NeverTouchFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf1 := MustAdd(t, db, root, "Leaf 1")
	MustAdd(t, db, root, "Leaf 2")

	MustClaim(t, db, leaf1, "1h")

	if err := RunNote(db, leaf1, "progress", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	if err := RunRelease(db, leaf1, "stepping away", TestActor); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, _, err := RunDone(db, []string{leaf1}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	if n := focusEventCount(t, db, TestActor); n != 0 {
		t.Errorf("focus events after note/release/done: got %d, want 0", n)
	}
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != root {
		t.Errorf("focus after note/release/done: got %v, want still %s", got, root)
	}
}
