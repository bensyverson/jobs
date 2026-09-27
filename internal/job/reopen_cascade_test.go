package job

import (
	"database/sql"
	"slices"
	"testing"
)

// `reopen --cascade` undoes the target's own cascading close and nothing
// else: it reopens the descendants that close recorded in cascade_closed, and
// only those still closed by it.

func mustDoneCascade(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, _, err := RunDone(db, []string{id}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done --cascade %s: %v", id, err)
	}
}

func mustCancelCascade(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, _, _, err := RunCancel(db, []string{id}, "not needed", true, false, false, TestActor); err != nil {
		t.Fatalf("cancel --cascade %s: %v", id, err)
	}
}

func TestReopenCascade_LeavesADescendantClosedBeforeTheCascadeClosed(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	early := MustAdd(t, db, p, "Finished on its own")
	late := MustAdd(t, db, p, "Closed by the cascade")
	MustDone(t, db, early)
	mustDoneCascade(t, db, p)

	res := mustReopen(t, db, p, true)

	wantStatus(t, db, early, "done")
	wantStatus(t, db, late, "available")
	if !slices.Equal(res.ReopenedChildren, []string{late}) {
		t.Errorf("reopened children: got %v, want [%s]", res.ReopenedChildren, late)
	}
	if !slices.Equal(res.CascadeClosed, []string{late}) {
		t.Errorf("cascade closed: got %v, want [%s]", res.CascadeClosed, late)
	}
}

func TestReopenCascade_UndoesACancelCascadeOnly(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	early := MustAdd(t, db, p, "Canceled on its own")
	late := MustAdd(t, db, p, "Canceled by the cascade")
	mustCancel(t, db, early)
	mustCancelCascade(t, db, p)

	res := mustReopen(t, db, p, true)

	wantStatus(t, db, early, "canceled")
	wantStatus(t, db, late, "available")
	if !slices.Equal(res.ReopenedChildren, []string{late}) {
		t.Errorf("reopened children: got %v, want [%s]", res.ReopenedChildren, late)
	}
}

func TestReopenCascade_LeavesADescendantReclosedByHandSince(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	redone := MustAdd(t, db, p, "Reopened and finished again")
	other := MustAdd(t, db, p, "Untouched since the cascade")
	mustDoneCascade(t, db, p)
	mustReopen(t, db, redone, false)
	MustDone(t, db, redone)

	res := mustReopen(t, db, p, true)

	// The second close was a person's decision; later human action wins.
	wantStatus(t, db, redone, "done")
	wantStatus(t, db, other, "available")
	if !slices.Equal(res.ReopenedChildren, []string{other}) {
		t.Errorf("reopened children: got %v, want [%s]", res.ReopenedChildren, other)
	}
	if !slices.Equal(res.CascadeClosed, []string{redone, other}) {
		t.Errorf("cascade closed: got %v, want [%s %s]", res.CascadeClosed, redone, other)
	}
	if !slices.Equal(res.CascadeLeftAlone, []string{redone}) {
		t.Errorf("left alone: got %v, want [%s]", res.CascadeLeftAlone, redone)
	}
}

func TestReopenCascade_LeavesADescendantReopenedSince(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	c := MustAdd(t, db, p, "Reopened by hand")
	d := MustAdd(t, db, p, "Still closed by the cascade")
	mustDoneCascade(t, db, p)
	mustReopen(t, db, c, false)

	res := mustReopen(t, db, p, true)

	if n := countTaskEvents(t, db, EventReopened, c); n != 1 {
		t.Errorf("%s reopened %d times, want 1", c, n)
	}
	wantStatus(t, db, d, "available")
	if !slices.Equal(res.CascadeLeftAlone, []string{c}) {
		t.Errorf("left alone: got %v, want [%s]", res.CascadeLeftAlone, c)
	}
}

func TestReopenCascade_ReopensGrandchildrenTheSameCascadeClosed(t *testing.T) {
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	c := MustAdd(t, db, p, "Child")
	gcEarly := MustAdd(t, db, c, "Grandchild finished on its own")
	gc := MustAdd(t, db, c, "Grandchild closed by the cascade")
	MustDone(t, db, gcEarly)
	mustDoneCascade(t, db, p)

	res := mustReopen(t, db, p, true)

	wantStatus(t, db, c, "available")
	wantStatus(t, db, gc, "available")
	wantStatus(t, db, gcEarly, "done")
	if !slices.Equal(res.ReopenedChildren, []string{c, gc}) {
		t.Errorf("reopened children: got %v, want [%s %s]", res.ReopenedChildren, c, gc)
	}
}

func TestReopenCascade_ReopensNothingExtraAfterANonCascadingClose(t *testing.T) {
	// The children closed one by one and the parent auto-closed behind the
	// last of them: its close closed no descendant, so none comes back.
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	a := MustAdd(t, db, p, "First")
	b := MustAdd(t, db, p, "Last")
	MustDone(t, db, a)
	MustDone(t, db, b)
	wantStatus(t, db, p, "done")

	res := mustReopen(t, db, p, true)

	wantStatus(t, db, p, "available")
	wantStatus(t, db, a, "done")
	wantStatus(t, db, b, "done")
	if len(res.ReopenedChildren) != 0 || len(res.CascadeClosed) != 0 {
		t.Errorf("got reopened %v, cascade closed %v; want none", res.ReopenedChildren, res.CascadeClosed)
	}
}

func TestReopenCascade_UsesTheTargetsMostRecentClose(t *testing.T) {
	// An earlier cascade's descendants that were reopened and are open again
	// are not the most recent close's to undo — and a second cascade closed
	// only what was open at the time.
	db := SetupTestDB(t)
	p := MustAdd(t, db, "", "Parent")
	c1 := MustAdd(t, db, p, "Closed by the first cascade, left closed")
	c2 := MustAdd(t, db, p, "Closed by both cascades")
	mustDoneCascade(t, db, p)
	mustReopen(t, db, p, false)
	mustReopen(t, db, c2, false)
	mustDoneCascade(t, db, p)

	res := mustReopen(t, db, p, true)

	wantStatus(t, db, c1, "done")
	wantStatus(t, db, c2, "available")
	if !slices.Equal(res.ReopenedChildren, []string{c2}) {
		t.Errorf("reopened children: got %v, want [%s]", res.ReopenedChildren, c2)
	}
}
