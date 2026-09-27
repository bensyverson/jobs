package job

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// Reopen reverses the automatic consequences of the close it undoes: the
// ancestors the leaf-frontier cascade closed, and the block edges the close
// dropped from dependents. Anything a person did since is left alone.

func mustReopen(t *testing.T, db *sql.DB, id string, cascade bool) *ReopenResult {
	t.Helper()
	res, err := RunReopen(db, id, cascade, TestActor)
	if err != nil {
		t.Fatalf("RunReopen(%s): %v", id, err)
	}
	return res
}

func mustBlock(t *testing.T, db *sql.DB, blocked, blocker string) {
	t.Helper()
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("block %s by %s: %v", blocked, blocker, err)
	}
}

func mustCancel(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, _, _, err := RunCancel(db, []string{id}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel %s: %v", id, err)
	}
}

func hasEdge(t *testing.T, db *sql.DB, blocked, blocker string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM blocks b
		JOIN tasks d ON d.id = b.blocked_id
		JOIN tasks x ON x.id = b.blocker_id
		WHERE d.short_id = ? AND x.short_id = ?`, blocked, blocker).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func countTaskEvents(t *testing.T, db *sql.DB, typ EventType, task string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE e.event_type = ? AND t.short_id = ?`, string(typ), task).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantStatus(t *testing.T, db *sql.DB, id, want string) {
	t.Helper()
	if got := MustGet(t, db, id).Status; got != want {
		t.Errorf("%s: status %q, want %q", id, got, want)
	}
}

func ancestorIDs(res *ReopenResult) []string {
	var ids []string
	for _, a := range res.ReopenedAncestors {
		ids = append(ids, a.ShortID)
	}
	return ids
}

func TestReopen_ReopensTheAncestorsItsCloseAutoClosed(t *testing.T) {
	db := SetupTestDB(t)
	g := MustAdd(t, db, "", "Grandparent")
	p := MustAdd(t, db, g, "Parent")
	leaf := MustAdd(t, db, p, "Leaf")
	MustDone(t, db, leaf)
	wantStatus(t, db, p, "done")
	wantStatus(t, db, g, "done")

	res := mustReopen(t, db, leaf, false)

	wantStatus(t, db, leaf, "available")
	wantStatus(t, db, p, "available")
	wantStatus(t, db, g, "available")
	if got, want := ancestorIDs(res), []string{p, g}; !slices.Equal(got, want) {
		t.Errorf("reopened ancestors: got %v, want %v (nearest first)", got, want)
	}
	if len(res.ReopenedAncestors) > 0 && res.ReopenedAncestors[0].Title != "Parent" {
		t.Errorf("ancestor title: got %q", res.ReopenedAncestors[0].Title)
	}
}

func TestReopen_ReopensAnAncestorAutoCanceledByACancel(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	leaf := MustAdd(t, db, p, "Leaf")
	mustCancel(t, db, leaf)
	wantStatus(t, db, p, "canceled")

	res := mustReopen(t, db, leaf, false)

	wantStatus(t, db, p, "available")
	if got := ancestorIDs(res); !slices.Equal(got, []string{p}) {
		t.Errorf("reopened ancestors: got %v, want [%s]", got, p)
	}
}

func TestReopen_LeavesAnExplicitlyClosedParentDone(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	leaf := MustAdd(t, db, p, "Leaf")
	d := MustAdd(t, db, "", "Waits on the leaf")
	mustBlock(t, db, d, leaf)
	if _, _, err := RunDone(db, []string{p}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done --cascade: %v", err)
	}

	res := mustReopen(t, db, leaf, false)

	// The parent's close was a decision; the leaf's own consequences are
	// still reversed.
	wantStatus(t, db, p, "done")
	if len(res.ReopenedAncestors) != 0 {
		t.Errorf("reopened ancestors: got %v, want none", ancestorIDs(res))
	}
	if !hasEdge(t, db, d, leaf) {
		t.Error("the leaf's own edge was not restored")
	}
}

func TestReopen_StopsAtTheFirstAncestorAPersonClosed(t *testing.T) {
	db := SetupTestDB(t)
	g := MustAdd(t, db, "", "Grandparent")
	p := MustAdd(t, db, g, "Parent")
	q := MustAdd(t, db, p, "Subgroup")
	leaf := MustAdd(t, db, q, "Leaf")
	MustDone(t, db, leaf) // auto-closes q, p and g
	// Someone reopens p and closes it again by hand. That close is a
	// decision, not a cascade, so reopening the leaf must not undo it — nor
	// reach past it to g, which the hand close auto-closed again.
	if _, err := RunReopen(db, p, false, TestActor); err != nil {
		t.Fatalf("reopen parent: %v", err)
	}
	MustDone(t, db, p)
	wantStatus(t, db, g, "done")

	res := mustReopen(t, db, leaf, false)

	wantStatus(t, db, q, "available")
	wantStatus(t, db, p, "done")
	wantStatus(t, db, g, "done")
	if got := ancestorIDs(res); !slices.Equal(got, []string{q}) {
		t.Errorf("reopened ancestors: got %v, want [%s]", got, q)
	}
}

func TestReopen_RestoresTheBlocksItsCloseRemoved(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d1 := MustAdd(t, db, "", "Dependent one")
	d2 := MustAdd(t, db, "", "Dependent two")
	mustBlock(t, db, d1, x)
	mustBlock(t, db, d2, x)
	MustDone(t, db, x)
	if hasEdge(t, db, d1, x) || hasEdge(t, db, d2, x) {
		t.Fatal("setup: done should have removed the edges")
	}

	res := mustReopen(t, db, x, false)

	if !hasEdge(t, db, d1, x) || !hasEdge(t, db, d2, x) {
		t.Errorf("edges not restored: %s=%v %s=%v", d1, hasEdge(t, db, d1, x), d2, hasEdge(t, db, d2, x))
	}
	want := []RestoredBlock{
		{BlockedID: d1, BlockedTitle: "Dependent one", BlockerID: x},
		{BlockedID: d2, BlockedTitle: "Dependent two", BlockerID: x},
	}
	// Restored in short-id order, the order the close dropped them in.
	slices.SortFunc(want, func(a, b RestoredBlock) int { return strings.Compare(a.BlockedID, b.BlockedID) })
	if !slices.Equal(res.RestoredBlocks, want) {
		t.Errorf("restored blocks: got %+v, want %+v", res.RestoredBlocks, want)
	}
}

func TestReopen_RestoresTheBlocksACancelRemoved(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d := MustAdd(t, db, "", "Dependent")
	mustBlock(t, db, d, x)
	mustCancel(t, db, x)

	mustReopen(t, db, x, false)

	if !hasEdge(t, db, d, x) {
		t.Error("edge dropped by cancel was not restored")
	}
}

func TestReopen_RestoresTheBlocksOfAReopenedAncestor(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	leaf := MustAdd(t, db, p, "Leaf")
	d := MustAdd(t, db, "", "Waits on the parent")
	mustBlock(t, db, d, p)
	MustDone(t, db, leaf) // auto-closes p, which drops d's edge

	res := mustReopen(t, db, leaf, false)

	if !hasEdge(t, db, d, p) {
		t.Error("the auto-closed parent's edge was not restored")
	}
	if len(res.RestoredBlocks) != 1 || res.RestoredBlocks[0].BlockerID != p {
		t.Errorf("restored blocks: got %+v", res.RestoredBlocks)
	}
}

// wantRestored checks that exactly the named dependents were re-blocked.
func wantRestored(t *testing.T, res *ReopenResult, blocked ...string) {
	t.Helper()
	var got []string
	for _, r := range res.RestoredBlocks {
		got = append(got, r.BlockedID)
	}
	if !slices.Equal(got, blocked) {
		t.Errorf("restored blocks: got %v, want %v", got, blocked)
	}
}

// Each guard test below keeps one untouched dependent as a control, so the
// test fails until restoring works at all rather than passing vacuously.

func TestReopen_DoesNotReblockAClosedDependent(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	done := MustAdd(t, db, "", "Finished since")
	canceled := MustAdd(t, db, "", "Canceled since")
	open := MustAdd(t, db, "", "Still open")
	mustBlock(t, db, done, x)
	mustBlock(t, db, canceled, x)
	mustBlock(t, db, open, x)
	MustDone(t, db, x)
	MustDone(t, db, done)
	mustCancel(t, db, canceled)

	res := mustReopen(t, db, x, false)

	if hasEdge(t, db, done, x) || hasEdge(t, db, canceled, x) {
		t.Error("a closed dependent was re-blocked")
	}
	wantRestored(t, res, open)
}

func TestReopen_DoesNotDuplicateAnEdgeReaddedByHand(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d := MustAdd(t, db, "", "Dependent")
	open := MustAdd(t, db, "", "Still open")
	mustBlock(t, db, d, x)
	mustBlock(t, db, open, x)
	MustDone(t, db, x)
	mustBlock(t, db, d, x) // re-added by hand while x was closed

	res := mustReopen(t, db, x, false)

	if got := countTaskEvents(t, db, EventBlocked, d); got != 2 {
		t.Errorf("blocked events on %s: got %d, want 2 (no restore event)", d, got)
	}
	wantRestored(t, res, open)
}

func TestReopen_DoesNotRestoreAnEdgeAPersonRemovedSince(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d := MustAdd(t, db, "", "Dependent")
	open := MustAdd(t, db, "", "Still open")
	mustBlock(t, db, d, x)
	mustBlock(t, db, open, x)
	MustDone(t, db, x)
	// Re-added and then removed by hand: the last word on the edge is a
	// person's, so reopen leaves it gone.
	mustBlock(t, db, d, x)
	if err := RunUnblock(db, d, x, TestActor); err != nil {
		t.Fatalf("unblock: %v", err)
	}

	res := mustReopen(t, db, x, false)

	if hasEdge(t, db, d, x) {
		t.Error("an edge a person removed was restored")
	}
	wantRestored(t, res, open)
}

func TestReopen_SkipsAPurgedDependent(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d := MustAdd(t, db, "", "Dependent")
	open := MustAdd(t, db, "", "Still open")
	mustBlock(t, db, d, x)
	mustBlock(t, db, open, x)
	MustDone(t, db, x)
	if _, _, _, err := RunCancel(db, []string{d}, "mistake", false, true, false, TestActor); err != nil {
		t.Fatalf("purge: %v", err)
	}

	res := mustReopen(t, db, x, false)

	wantRestored(t, res, open)
}

func TestReopen_SkipsAnEdgeThatWouldNowFormACycle(t *testing.T) {
	db := SetupTestDB(t)
	x := MustAdd(t, db, "", "Blocker")
	d := MustAdd(t, db, "", "Dependent")
	open := MustAdd(t, db, "", "Still open")
	mustBlock(t, db, d, x)
	mustBlock(t, db, open, x)
	MustDone(t, db, x)
	mustBlock(t, db, x, d) // while x was closed, x came to wait on d

	res := mustReopen(t, db, x, false)

	if hasEdge(t, db, d, x) {
		t.Error("restored an edge that closes a cycle")
	}
	wantRestored(t, res, open)
}

func TestReopen_CascadeRestoresTheBlocksOfReopenedDescendants(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	c := MustAdd(t, db, p, "Child")
	d := MustAdd(t, db, "", "Waits on the child")
	mustBlock(t, db, d, c)
	if _, _, err := RunDone(db, []string{p}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done --cascade: %v", err)
	}

	res := mustReopen(t, db, p, true)

	if !hasEdge(t, db, d, c) {
		t.Error("the reopened descendant's edge was not restored")
	}
	if !slices.Equal(res.ReopenedChildren, []string{c}) {
		t.Errorf("reopened children: got %v, want [%s]", res.ReopenedChildren, c)
	}
}

func TestReopen_AnAutoClosedTargetReopensTheAncestorsAboveIt(t *testing.T) {
	// Reopening a parent that closed only because its last leaf did: the
	// grandparent closed in the same cascade and has an open child now.
	db := SetupTestDB(t)
	g := MustAdd(t, db, "", "Grandparent")
	p := MustAdd(t, db, g, "Parent")
	leaf := MustAdd(t, db, p, "Leaf")
	MustDone(t, db, leaf)

	res := mustReopen(t, db, p, false)

	wantStatus(t, db, g, "available")
	wantStatus(t, db, leaf, "done")
	if got := ancestorIDs(res); !slices.Equal(got, []string{g}) {
		t.Errorf("reopened ancestors: got %v, want [%s]", got, g)
	}
}
