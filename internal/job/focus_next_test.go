package job

import (
	"strings"
	"testing"
)

// dH2iP — Bare next / claim --next scope to the actor's focus. With focus
// set, the no-argument frontier walk stays inside the focused root; an
// exhausted focused root fails loudly (naming the root and both escapes)
// instead of silently crossing trees; explicit parent arguments bypass
// focus; no focus means today's global behavior.

// JuS — With focus set, bare next and claim --next only yield leaves inside
// the focused root.
func TestNext_FocusSet_StaysInFocusedRoot(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf 1")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	leafB2 := MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // sets focus to rootB

	next, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if next.ShortID != leafB2 {
		t.Errorf("bare next with focus on B: got %s, want %s (not root A's leaf)", next.ShortID, leafB2)
	}

	claimed, err := RunClaimNext(db, "", "1h", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if claimed.ShortID != leafB2 {
		t.Errorf("bare claim --next with focus on B: got %s, want %s", claimed.ShortID, leafB2)
	}
}

// 6jK — With focus set and the root exhausted, the error names the root and
// both escapes.
func TestNext_FocusedRootExhausted_FailsLoudly(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB := MustAdd(t, db, rootB, "Only B leaf")

	MustClaim(t, db, leafB, "1h") // sets focus to rootB; B now has no available leaf

	_, err := RunNext(db, "", TestActor)
	if err == nil {
		t.Fatalf("RunNext with exhausted focused root: want loud error, got a task")
	}
	msg := err.Error()
	if !strings.Contains(msg, rootB) {
		t.Errorf("error must name the focused root %s: %q", rootB, msg)
	}
	if !strings.Contains(msg, "claim --next") {
		t.Errorf("error must name the claim-elsewhere escape: %q", msg)
	}
	if !strings.Contains(msg, "focus --release") {
		t.Errorf("error must name the focus --release escape: %q", msg)
	}
}

// OW9 — An explicit parent argument overrides focus.
func TestNext_ExplicitParent_BypassesFocus(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	leafB1 := MustAdd(t, db, rootB, "B leaf 1")
	MustAdd(t, db, rootB, "B leaf 2")

	MustClaim(t, db, leafB1, "1h") // focus on rootB

	next, err := RunNext(db, rootA, TestActor)
	if err != nil {
		t.Fatalf("RunNext(rootA): %v", err)
	}
	if next.ShortID != leafA {
		t.Errorf("explicit-parent next: got %s, want %s regardless of focus", next.ShortID, leafA)
	}
}

// 5J6 — With no focus set, behavior is unchanged: the globally first
// available leaf wins.
func TestNext_NoFocus_GlobalBehaviorUnchanged(t *testing.T) {
	db := SetupTestDB(t)
	rootA := MustAdd(t, db, "", "Root A")
	leafA := MustAdd(t, db, rootA, "A leaf")
	rootB := MustAdd(t, db, "", "Root B")
	MustAdd(t, db, rootB, "B leaf")

	next, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if next.ShortID != leafA {
		t.Errorf("no-focus next: got %s, want globally first leaf %s", next.ShortID, leafA)
	}
}
