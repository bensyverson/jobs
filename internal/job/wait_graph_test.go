package job

import (
	"sort"
	"strings"
	"testing"
)

// block add and import both refuse an edge that would close a loop in the
// wait graph (wait_cycle_test.go), so a healthy store can never reach one
// through the CLI. A store written before that check landed may still hold
// one, and FindDeadlocks is how it gets found after the fact. Since the
// guarded path is unreachable here, every test seeds its loop below the
// check with SeedBlockEdge (testhelpers.go).

func TestFindDeadlocks_EmptyStoreHasNone(t *testing.T) {
	db := SetupTestDB(t)
	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 0 {
		t.Errorf("deadlocks = %v, want none", deadlocks)
	}
}

func TestFindDeadlocks_NonCyclicBlocksAndContainmentAreClean(t *testing.T) {
	db := SetupTestDB(t)
	top := MustAdd(t, db, "", "Top")
	mid := MustAdd(t, db, top, "Mid")
	leaf := MustAdd(t, db, mid, "Leaf")
	other := MustAdd(t, db, "", "Other")
	if err := RunBlock(db, leaf, other, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 0 {
		t.Errorf("deadlocks = %v, want none", deadlocks)
	}
}

func TestFindDeadlocks_TwoTaskCycle(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	// Seeded below the check: a healthy store never holds this edge pair
	// because RunBlockMany would refuse the second one.
	SeedBlockEdge(t, db, a, b)
	SeedBlockEdge(t, db, b, a)

	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 1 {
		t.Fatalf("deadlocks = %d, want 1: %+v", len(deadlocks), deadlocks)
	}

	ids := []string{a, b}
	sort.Strings(ids)
	min, max := ids[0], ids[1]
	want := min + " blocked by " + max + ", " + max + " blocked by " + min
	if deadlocks[0].Chain != want {
		t.Errorf("Chain = %q, want %q", deadlocks[0].Chain, want)
	}
	if deadlocks[0].Blocked != min || deadlocks[0].Blocker != max {
		t.Errorf("Blocked/Blocker = %s/%s, want %s/%s", deadlocks[0].Blocked, deadlocks[0].Blocker, min, max)
	}
	wantFix := "job block remove " + min + " by " + max
	if deadlocks[0].Fix() != wantFix {
		t.Errorf("Fix() = %q, want %q", deadlocks[0].Fix(), wantFix)
	}
}

// A leaf blocked on its own ancestor: the ancestor waits on the leaf by
// containment (a parent closes only when its open children do), and the
// leaf now waits on the ancestor by an explicit block — a loop with one
// "blocked by" edge and two "parent of" edges, exactly what block add and
// import refuse to create (wait_cycle_test.go TestRunBlock_RefusesBlockOnOwnAncestor).
func TestFindDeadlocks_LeafBlockedByAncestor(t *testing.T) {
	db := SetupTestDB(t)
	top := MustAdd(t, db, "", "Top")
	mid := MustAdd(t, db, top, "Mid")
	leaf := MustAdd(t, db, mid, "Leaf")
	SeedBlockEdge(t, db, leaf, top)

	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 1 {
		t.Fatalf("deadlocks = %d, want 1: %+v", len(deadlocks), deadlocks)
	}
	d := deadlocks[0]
	for _, frag := range []string{
		leaf + " blocked by " + top,
		top + " parent of " + mid,
		mid + " parent of " + leaf,
	} {
		if !strings.Contains(d.Chain, frag) {
			t.Errorf("Chain %q missing fragment %q", d.Chain, frag)
		}
	}
	if got := strings.Count(d.Chain, ", ") + 1; got != 3 {
		t.Errorf("Chain has %d steps, want 3: %q", got, d.Chain)
	}
	if d.Blocked != leaf || d.Blocker != top {
		t.Errorf("Blocked/Blocker = %s/%s, want %s/%s", d.Blocked, d.Blocker, leaf, top)
	}
	wantFix := "job block remove " + leaf + " by " + top
	if d.Fix() != wantFix {
		t.Errorf("Fix() = %q, want %q", d.Fix(), wantFix)
	}
}

func TestFindDeadlocks_ReportsEachDistinctCycleOnce(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	c := MustAdd(t, db, "", "C")
	d := MustAdd(t, db, "", "D")
	SeedBlockEdge(t, db, a, b)
	SeedBlockEdge(t, db, b, a)
	SeedBlockEdge(t, db, c, d)
	SeedBlockEdge(t, db, d, c)

	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 2 {
		t.Fatalf("deadlocks = %d, want 2: %+v", len(deadlocks), deadlocks)
	}
	seen := map[string]bool{}
	for _, dl := range deadlocks {
		if seen[dl.Chain] {
			t.Errorf("cycle reported more than once: %q", dl.Chain)
		}
		seen[dl.Chain] = true
	}
}

// A cycle entirely among done tasks isn't a deadlock: closing a task drops
// every edge it was blocking (emitBlocksUnblockedOn), so a done task is
// never a live wait graph node, and FindDeadlocks only starts its scan from
// open tasks in the first place.
func TestFindDeadlocks_DoneTaskIsNotScanned(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	SeedBlockEdge(t, db, a, b)
	SeedBlockEdge(t, db, b, a)
	MustDone(t, db, a)

	deadlocks, err := FindDeadlocks(db)
	if err != nil {
		t.Fatalf("FindDeadlocks: %v", err)
	}
	if len(deadlocks) != 0 {
		t.Errorf("deadlocks = %v, want none once a is done", deadlocks)
	}
}
