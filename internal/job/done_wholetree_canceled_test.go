package job

import "testing"

// subtreeCompleteness (via ComputeDoneContext.WholeTreeComplete) must treat a
// canceled task as settled, the same as a done one: a root whose remaining
// child is canceled, with every other task done, is a complete tree — not an
// incomplete one just because "canceled" != "done". project/agents/jobs.md
// leaf Vxhv0d.
func TestComputeDoneContext_WholeTreeCompleteWithCanceledSibling(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	a := MustAdd(t, db, pid, "Won't happen")
	b := MustAdd(t, db, pid, "Last real work")

	if _, _, _, err := RunCancel(db, []string{a}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	// Closing b leaves the parent with zero open children (a is canceled,
	// not open), so the leaf-frontier cascade auto-closes the parent too —
	// the whole tree (parent, a, b) is now settled.
	closed, _, err := RunDone(db, []string{b}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("closed: got %+v, want 1", closed)
	}
	autoClosedSet := map[string]bool{}
	for _, anc := range closed[0].AutoClosedAncestors {
		autoClosedSet[anc.ShortID] = true
	}
	if !autoClosedSet[pid] {
		t.Fatalf("expected parent %s to auto-close alongside b, got AutoClosedAncestors=%+v", pid, closed[0].AutoClosedAncestors)
	}

	ctx, err := ComputeDoneContext(db, b, autoClosedSet)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if !ctx.WholeTreeComplete {
		t.Errorf("WholeTreeComplete = false, want true (canceled sibling is settled, not open)")
	}
	if ctx.WholeTreeRootID != pid {
		t.Errorf("WholeTreeRootID = %q, want %q", ctx.WholeTreeRootID, pid)
	}
	// doneCount counts literal "done" statuses only (parent + b); the
	// canceled task a is settled but not counted as done.
	if ctx.WholeTreeDoneCount != 2 {
		t.Errorf("WholeTreeDoneCount = %d, want 2 (parent + b; a is canceled, not done)", ctx.WholeTreeDoneCount)
	}
}
