package job

import "testing"

// `done` on a task with found-in edges should surface the still-open issues
// it produced (Decision 5, project/2026-08-28-issues-ux.md): the close
// reports, it does not refuse. ComputeDoneContext.SurfacedOpen carries the
// list the `done` ack renders as a `Surfaced:` line.

func TestComputeDoneContext_SurfacedOpenListsOnlyOpenIssues(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "Wire the router")
	openBug := MustAdd(t, db, "", "Router drops trailing slash")
	closedBug := MustAdd(t, db, "", "Typo in error message")

	if err := RunSetFoundIn(db, openBug, leaf, TestActor); err != nil {
		t.Fatalf("RunSetFoundIn(openBug): %v", err)
	}
	if err := RunSetFoundIn(db, closedBug, leaf, TestActor); err != nil {
		t.Fatalf("RunSetFoundIn(closedBug): %v", err)
	}
	MustDone(t, db, closedBug)

	MustDone(t, db, leaf)
	ctx, err := ComputeDoneContext(db, leaf, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if len(ctx.SurfacedOpen) != 1 {
		t.Fatalf("SurfacedOpen = %v, want exactly the one open issue", ctx.SurfacedOpen)
	}
	if ctx.SurfacedOpen[0].ShortID != openBug {
		t.Fatalf("SurfacedOpen[0] = %s, want %s", ctx.SurfacedOpen[0].ShortID, openBug)
	}
}

func TestComputeDoneContext_SurfacedOpenEmptyWhenNoSurfacedIssues(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A plain leaf with no found-in edges")

	MustDone(t, db, leaf)
	ctx, err := ComputeDoneContext(db, leaf, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if len(ctx.SurfacedOpen) != 0 {
		t.Fatalf("SurfacedOpen = %v, want none", ctx.SurfacedOpen)
	}
}

func TestComputeDoneContext_SurfacedOpenExcludesCanceled(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "Wire the router")
	canceledBug := MustAdd(t, db, "", "Won't fix")

	if err := RunSetFoundIn(db, canceledBug, leaf, TestActor); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}
	if _, _, _, err := RunCancel(db, []string{canceledBug}, "not a real bug", false, false, false, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	MustDone(t, db, leaf)
	ctx, err := ComputeDoneContext(db, leaf, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if len(ctx.SurfacedOpen) != 0 {
		t.Fatalf("SurfacedOpen = %v, want none (the only surfaced issue was canceled)", ctx.SurfacedOpen)
	}
}
