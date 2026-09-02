package job

import (
	"testing"
)

// gplfH — Focus domain. Focus is a per-actor pointer at a root task, held in
// .jobs/local.json beside the cache — machine-local state, never an event. A
// focus whose root is done, canceled, or deleted reads as released without
// needing a tombstone.

// 4BA — SetFocus records the root in local.json and GetFocus returns it for
// the same actor only.
func TestSetFocus_GetFocus_PerActor(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")

	if _, err := SetFocus(db, rootA, "alice"); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}

	got, err := GetFocus(db, "alice")
	if err != nil {
		t.Fatalf("GetFocus(alice): %v", err)
	}
	if got == nil || got.ShortID != rootA {
		t.Errorf("GetFocus(alice): got %v, want root %s", got, rootA)
	}

	other, err := GetFocus(db, "bob")
	if err != nil {
		t.Fatalf("GetFocus(bob): %v", err)
	}
	if other != nil {
		t.Errorf("GetFocus(bob): got %s, want nil (focus is per-actor)", other.ShortID)
	}

	if slot := localFocusSlot(t, db, "alice", KindTask); slot != rootA {
		t.Errorf("local.json task slot for alice: got %q, want %q", slot, rootA)
	}
	if n := focusEventCount(t, db, "alice"); n != 0 {
		t.Errorf("focus events recorded: got %d, want 0", n)
	}
}

// Last set wins: focusing a second root moves the pointer.
func TestSetFocus_LastSetWins(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	rootB := MustAdd(t, db, "", "Root B")

	if _, err := SetFocus(db, rootA, TestActor); err != nil {
		t.Fatalf("SetFocus A: %v", err)
	}
	if _, err := SetFocus(db, rootB, TestActor); err != nil {
		t.Fatalf("SetFocus B: %v", err)
	}
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != rootB {
		t.Errorf("GetFocus after re-set: got %v, want %s", got, rootB)
	}
}

// 5hI — ReleaseFocus empties the slot and GetFocus returns nil afterward.
func TestReleaseFocus(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")

	if _, err := SetFocus(db, root, TestActor); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	if err := ReleaseFocus(db, TestActor); err != nil {
		t.Fatalf("ReleaseFocus: %v", err)
	}
	got, err := GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got != nil {
		t.Errorf("GetFocus after release: got %s, want nil", got.ShortID)
	}

	if slot := localFocusSlot(t, db, TestActor, KindTask); slot != "" {
		t.Errorf("local.json task slot after release: got %q, want empty", slot)
	}
}

// Releasing with no focus set is a quiet no-op, not an error or a stray slot.
func TestReleaseFocus_NoFocus_NoOp(t *testing.T) {
	db := SetupTestDB(t)
	if err := ReleaseFocus(db, TestActor); err != nil {
		t.Fatalf("ReleaseFocus with no focus: %v", err)
	}
	if slot := localFocusSlot(t, db, TestActor, KindTask); slot != "" {
		t.Errorf("local.json task slot after a no-op release: got %q, want empty", slot)
	}
}

// OVF — GetFocus returns nil when the focused root is done, canceled, or
// deleted; no tombstone event is required.
func TestGetFocus_StaleRoots_ReadAsReleased(t *testing.T) {
	db := SetupTestDB(t)

	doneRoot := MustAdd(t, db, "", "Done root")
	if _, err := SetFocus(db, doneRoot, "done-actor"); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	MustDone(t, db, doneRoot)
	if got, err := GetFocus(db, "done-actor"); err != nil || got != nil {
		t.Errorf("GetFocus on done root: got %v, err %v; want nil, nil", got, err)
	}

	canceledRoot := MustAdd(t, db, "", "Canceled root")
	if _, err := SetFocus(db, canceledRoot, "cancel-actor"); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	if _, _, _, err := RunCancel(db, []string{canceledRoot}, "abandoned", false, false, true, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}
	if got, err := GetFocus(db, "cancel-actor"); err != nil || got != nil {
		t.Errorf("GetFocus on canceled root: got %v, err %v; want nil, nil", got, err)
	}

	purgedRoot := MustAdd(t, db, "", "Purged root")
	if _, err := SetFocus(db, purgedRoot, "purge-actor"); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	if _, _, _, err := RunCancel(db, []string{purgedRoot}, "gone", false, true, true, TestActor); err != nil {
		t.Fatalf("RunCancel --purge: %v", err)
	}
	if got, err := GetFocus(db, "purge-actor"); err != nil || got != nil {
		t.Errorf("GetFocus on purged root: got %v, err %v; want nil, nil", got, err)
	}
}
