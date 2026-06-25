package job

import (
	"strings"
	"testing"
)

// `done --claim-next` must scope its follow-on claim to the root subtree of the
// just-closed task, not the whole repo. RunClaimNextUnderRootOf resolves the
// closed task's top ancestor and claims the next available leaf within it, so a
// focused session never gets handed an unrelated leaf in a different root.

// After closing a leaf in root A, the scoped claim returns A's next leaf, never
// a leaf in an unrelated root B — even when B's leaf sorts earlier globally.
func TestClaimNextUnderRootOf_StaysInClosedTaskRoot(t *testing.T) {
	db := SetupTestDB(t)

	rootB := MustAdd(t, db, "", "Root B") // created first, so b1 sorts before a*
	b1 := MustAdd(t, db, rootB, "B1")
	rootA := MustAdd(t, db, "", "Root A")
	a1 := MustAdd(t, db, rootA, "A1")
	a2 := MustAdd(t, db, rootA, "A2")

	MustClaim(t, db, a1, "")
	if _, _, err := RunDone(db, []string{a1}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone a1: %v", err)
	}

	got, err := RunClaimNextUnderRootOf(db, a1, "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNextUnderRootOf: %v", err)
	}
	if got.ShortID != a2 {
		t.Errorf("scoped claim-next: got %s, want %s (A's next leaf); must not pick %s in root B", got.ShortID, a2, b1)
	}
}

// When the closed task's root is fully done, the scoped claim reports no
// available work rather than falling through to an unrelated root.
func TestClaimNextUnderRootOf_EmptySubtree_NoFallthrough(t *testing.T) {
	db := SetupTestDB(t)

	rootB := MustAdd(t, db, "", "Root B")
	_ = MustAdd(t, db, rootB, "B1")
	rootA := MustAdd(t, db, "", "Root A")
	a1 := MustAdd(t, db, rootA, "A1") // A's only leaf

	MustClaim(t, db, a1, "")
	if _, _, err := RunDone(db, []string{a1}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone a1: %v", err)
	}

	_, err := RunClaimNextUnderRootOf(db, a1, "", TestActor, false)
	if err == nil {
		t.Fatal("expected an error (root A is fully done), got nil — must not fall through to root B")
	}
	if !strings.Contains(err.Error(), "No available tasks") {
		t.Errorf("error should report no available tasks; got %v", err)
	}
}

// A closed task that is itself a root scopes to its own subtree.
func TestClaimNextUnderRootOf_ClosedTaskIsRoot(t *testing.T) {
	db := SetupTestDB(t)

	rootA := MustAdd(t, db, "", "Root A")
	a1 := MustAdd(t, db, rootA, "A1")
	_ = MustAdd(t, db, rootA, "A2")
	rootB := MustAdd(t, db, "", "Root B")
	_ = MustAdd(t, db, rootB, "B1")

	// Resolve the root of a1 (= rootA) by passing a1, then verify the next leaf
	// is under rootA. Passing rootA directly must behave the same.
	MustClaim(t, db, a1, "")
	got, err := RunClaimNextUnderRootOf(db, rootA, "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNextUnderRootOf: %v", err)
	}
	gt := MustGet(t, db, got.ShortID)
	top, err := findTopAncestor(db, gt)
	if err != nil {
		t.Fatalf("findTopAncestor: %v", err)
	}
	if top.ShortID != rootA {
		t.Errorf("claimed leaf %s is under root %s, want it under %s", got.ShortID, top.ShortID, rootA)
	}
}
