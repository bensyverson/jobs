package job

import (
	"strings"
	"testing"
)

// 0DAPk — Orient's default target respects focus. Regression pins, not
// red/green: resolveOrientTarget's no-arg path goes through RunNext, whose
// focus scoping shipped with dH2iP, so orient inherited this behavior the
// moment that landed. These tests pin the contract at the orient level so a
// future refactor of target resolution can't silently lose it.

// VD3 — No-arg orient targets a leaf inside the focused root when focus is
// set.
func TestRunOrient_NoArg_TargetsWithinFocusedRoot(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	leafB2 := MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // sets focus to rootB

	view, err := RunOrient(db, "", "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Header.Target != leafB2 {
		t.Errorf("no-arg orient target with focus on B: got %s, want %s", view.Header.Target, leafB2)
	}
	if view.Header.Root != rootB {
		t.Errorf("no-arg orient root: got %s, want focused root %s", view.Header.Root, rootB)
	}
}

// The exhausted-focus loud failure surfaces through orient too.
func TestRunOrient_NoArg_FocusedRootExhausted_FailsLoudly(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB := MustAdd(t, db, rootB, "Only B leaf")

	MustClaim(t, db, leafB, "1h") // focus rootB; nothing else available there

	_, err := RunOrient(db, "", "", TestActor)
	if err == nil {
		t.Fatalf("no-arg orient with exhausted focused root: want loud error, got a view")
	}
	if !strings.Contains(err.Error(), rootB) || !strings.Contains(err.Error(), "focus --clear") {
		t.Errorf("orient error must carry the focused-root escape hint: %q", err.Error())
	}
}

// o6E — Orient with an explicit id ignores focus.
func TestRunOrient_ExplicitID_IgnoresFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // focus rootB

	view, err := RunOrient(db, leafA, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient(leafA): %v", err)
	}
	if view.Header.Target != leafA {
		t.Errorf("explicit-id orient target: got %s, want %s regardless of focus", view.Header.Target, leafA)
	}
	if view.Header.Root != rootA {
		t.Errorf("explicit-id orient root: got %s, want %s", view.Header.Root, rootA)
	}
}
