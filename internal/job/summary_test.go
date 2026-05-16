package job

import (
	"bytes"
	"strings"
	"testing"
)

// R2 — `job summary <parent>` fills the gap between `status` (whole DB,
// too broad) and `list … all` (full subtree, too wide).
//
// Two-level shape: rollup of the target plus one rollup line per direct
// child. Stops there — `list` covers deep trees.

func TestRunSummary_TargetWithMixedChildren(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Project")
	a := MustAdd(t, db, root, "Phase A")
	b := MustAdd(t, db, root, "Phase B")
	a1 := MustAdd(t, db, a, "A1")
	a2 := MustAdd(t, db, a, "A2")
	MustAdd(t, db, b, "B1")
	MustAdd(t, db, b, "B2")
	MustDone(t, db, a1)
	MustDone(t, db, a2)

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	if s.Target.Done != 3 {
		t.Errorf("Target.Done: got %d, want 3 (a1, a2, and Phase A auto-closed)", s.Target.Done)
	}
	if s.Target.Open != 3 {
		t.Errorf("Target.Open: got %d, want 3 (Phase B + B1 + B2)", s.Target.Open)
	}
	if len(s.DirectChildren) != 2 {
		t.Fatalf("DirectChildren: got %d, want 2", len(s.DirectChildren))
	}
	if s.DirectChildren[0].Status != "done" {
		t.Errorf("Phase A status: got %q, want done", s.DirectChildren[0].Status)
	}
	if s.DirectChildren[1].Done != 0 || s.DirectChildren[1].Open != 2 {
		t.Errorf("Phase B counts: got %d done %d open, want 0,2", s.DirectChildren[1].Done, s.DirectChildren[1].Open)
	}
}

func TestRunSummary_TargetWithBlockedAndClaimed(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	a := MustAdd(t, db, root, "A")
	b := MustAdd(t, db, root, "B")
	c := MustAdd(t, db, root, "C")
	if err := RunBlock(db, b, a, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := RunClaim(db, c, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	if s.Target.Blocked != 1 {
		t.Errorf("Blocked: got %d, want 1", s.Target.Blocked)
	}
	if s.Target.InFlight != 1 {
		t.Errorf("InFlight: got %d, want 1", s.Target.InFlight)
	}
	if s.Target.Available != 1 {
		t.Errorf("Available: got %d, want 1 (only A is claimable now)", s.Target.Available)
	}
	if s.Target.NextID != a {
		t.Errorf("NextID: got %q, want %q (A)", s.Target.NextID, a)
	}
}

func TestRunSummary_LeafTarget(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Lonely")

	s, err := RunSummary(db, id)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	if s.Target.Open != 0 || s.Target.Done != 0 {
		t.Errorf("leaf target should have 0 descendants: got %+v", s.Target)
	}
	if len(s.DirectChildren) != 0 {
		t.Errorf("leaf target should have no children: got %d", len(s.DirectChildren))
	}
}

func TestRunSummary_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunSummary(db, "noExs")
	if err == nil {
		t.Fatal("expected error for missing task")
	}
}

// Forest scope: BuildRollup(db, nil) aggregates the whole DB. Target
// is nil (there's no single anchor); Children are the top-level tasks,
// each carrying its own subtree counts.
// Forest scope must hide root tasks that are fully closed (done or canceled
// with no open descendants).
func TestBuildRollup_ForestScope_HidesClosedRoots(t *testing.T) {
	db := SetupTestDB(t)

	// open root — must appear
	open := MustAdd(t, db, "", "OpenRoot")
	MustAdd(t, db, open, "Child")

	// done root with no open children — must NOT appear
	finished := MustAdd(t, db, "", "DoneRoot")
	child := MustAdd(t, db, finished, "FinishedChild")
	MustDone(t, db, child)
	MustDone(t, db, finished)

	// canceled root — must NOT appear
	canceled := MustAdd(t, db, "", "CanceledRoot")
	if _, _, _, err := RunCancel(db, []string{canceled}, "obsolete", false, false, true, ""); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	rollup, err := BuildRollup(db, nil)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}

	ids := make(map[string]bool)
	for _, c := range rollup.DirectChildren {
		ids[c.ShortID] = true
	}

	if !ids[open] {
		t.Errorf("open root %q must appear in forest scope", open)
	}
	if ids[finished] {
		t.Errorf("done root with no open children %q must not appear in forest scope", finished)
	}
	if ids[canceled] {
		t.Errorf("canceled root %q must not appear in forest scope", canceled)
	}
}

func TestBuildRollup_ForestScope_ReturnsRootsAsChildren(t *testing.T) {
	db := SetupTestDB(t)
	r1 := MustAdd(t, db, "", "Root1")
	MustAdd(t, db, r1, "L1a")
	MustAdd(t, db, r1, "L1b")
	r2 := MustAdd(t, db, "", "Root2")
	MustAdd(t, db, r2, "L2a")

	rollup, err := BuildRollup(db, nil)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	if rollup.Target != nil {
		t.Errorf("forest scope must produce a nil Target, got %+v", rollup.Target)
	}
	if len(rollup.DirectChildren) != 2 {
		t.Fatalf("forest scope should list both roots, got %d", len(rollup.DirectChildren))
	}
	// Roots carry their own subtree counts.
	byID := map[string]*SubtreeRollup{}
	for _, c := range rollup.DirectChildren {
		byID[c.ShortID] = c
	}
	if byID[r1] == nil || byID[r1].Done+byID[r1].Open < 2 {
		t.Errorf("Root1's subtree count should include its leaves: %+v", byID[r1])
	}
	if byID[r2] == nil || byID[r2].Done+byID[r2].Open < 1 {
		t.Errorf("Root2's subtree count should include its leaf: %+v", byID[r2])
	}
}

// Forest scope renders with no headline — just the per-root rows.
func TestRenderSummary_ForestScope_NoHeadline(t *testing.T) {
	db := SetupTestDB(t)
	r1 := MustAdd(t, db, "", "Root1")
	MustAdd(t, db, r1, "L1")
	_ = MustAdd(t, db, "", "Root2")

	rollup, err := BuildRollup(db, nil)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, rollup)
	got := buf.String()

	if strings.Contains(got, " · ") && strings.HasPrefix(got, "Root") == false {
		// Forest scope must not start with a "<title> · <id>" target line.
		// The first row should be one of the roots, not a DB-wide heading.
	}
	if !strings.Contains(got, "Root1") || !strings.Contains(got, "Root2") {
		t.Errorf("forest scope must render every root:\n%s", got)
	}
}

// u2.1 — Zero-count status tokens are noise; suppress them so the eye
// lands on what matters. A fully-done subtree should show "N of N done"
// alone, without "0 blocked · 0 available · 0 in flight" padding.
func TestRenderSummary_SuppressesZeroCountsOnFullyDoneSubtree(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "P")
	a := MustAdd(t, db, root, "A")
	b := MustAdd(t, db, root, "B")
	MustDone(t, db, a)
	MustDone(t, db, b)

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if !strings.Contains(got, "2 of 2 done") {
		t.Errorf("expected headline scoreboard: %s", got)
	}
	for _, noise := range []string{"0 blocked", "0 available", "0 in flight", "0 canceled"} {
		if strings.Contains(got, noise) {
			t.Errorf("zero-count %q must not appear: %s", noise, got)
		}
	}
}

// u2.2 — When the scope is fully complete, append a closure timestamp
// sourced from the latest done/canceled event under scope.
func TestRenderSummary_AppendsClosureTimestampWhenFullyComplete(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "P")
	a := MustAdd(t, db, root, "A")
	MustDone(t, db, a)

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if !strings.Contains(got, "closed ") {
		t.Errorf("expected closure timestamp on fully-done scope:\n%s", got)
	}
}

// u2.3 — A leaf child whose status matches the scope's dominant status
// should not carry a trailing ": <status>" tail. Scope is mostly-open
// here, so available is dominant; a ": available" suffix on the leaf
// would be redundant noise duplicating the headline's "N available".
// Non-leaf child shown alongside so u3's later leaf-only collapse
// doesn't eliminate the per-child block this test depends on.
func TestRenderSummary_PerChildDropsAvailableWhenDominant(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "LeafChild")
	deep := MustAdd(t, db, root, "DeepChild")
	MustAdd(t, db, deep, "deeper") // deep now has descendants → not a leaf child
	_ = leaf

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if strings.Contains(got, ": available") {
		t.Errorf(`leaf child matching dominant "available" must not show ": available":\n%s`, got)
	}
	if !strings.Contains(got, "LeafChild") {
		t.Errorf("leaf child line should still render (just without the status suffix):\n%s", got)
	}
}

// u2.3 — A leaf child whose raw status is "available" but which has an
// open blocker is effectively blocked; surface ": blocked" so the agent
// sees why the task isn't claimable.
func TestRenderSummary_PerChildSurfacesBlockedAsException(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	blocker := MustAdd(t, db, root, "Blocker")
	blockedChild := MustAdd(t, db, root, "BlockedChild")
	if err := RunBlock(db, blockedChild, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	deep := MustAdd(t, db, root, "DeepChild")
	MustAdd(t, db, deep, "deeper")

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if !strings.Contains(got, "BlockedChild ("+blockedChild+"): blocked") {
		t.Errorf(`blocked leaf child must surface ": blocked":\n%s`, got)
	}
}

// u2.3 — Done children in a not-fully-done scope are still exceptions
// against dominant=available — BUT saying ": done" on one row while the
// headline says "N of M done" is redundant. Prefer dropping. (The
// dominant-status rule works both ways: when dominant is "available",
// ": done" is the exception that's informative only when its count
// disagrees with what the headline implies — which for per-child shape
// it already does via the "N of M done" counter. Drop it.)
func TestRenderSummary_PerChildDropsDoneInPartiallyCompleteScope(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	doneChild := MustAdd(t, db, root, "DoneChild")
	_ = MustAdd(t, db, root, "OpenChild")
	deep := MustAdd(t, db, root, "DeepChild")
	MustAdd(t, db, deep, "deeper")
	MustDone(t, db, doneChild)

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if strings.Contains(got, "DoneChild ("+doneChild+"): done") {
		t.Errorf(`done leaf child should not carry ": done" suffix:\n%s`, got)
	}
	if !strings.Contains(got, "DoneChild") {
		t.Errorf("DoneChild row should still render:\n%s", got)
	}
}

// u6 — Subtree scope ends with a Next: line naming the first
// claimable leaf inside the target subtree.
func TestRenderSummary_AppendsNextHint_SubtreeScope(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	phase := MustAdd(t, db, root, "Phase")
	leaf := MustAdd(t, db, phase, "leaf")

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	want := "Next: " + leaf
	if !strings.Contains(got, want) {
		t.Errorf("subtree scope should append %q:\n%s", want, got)
	}
}

// u6 — Forest scope ends with a Next: line naming the globally-first
// claimable leaf.
func TestRenderSummary_AppendsNextHint_ForestScope(t *testing.T) {
	db := SetupTestDB(t)
	r1 := MustAdd(t, db, "", "Root1")
	leaf := MustAdd(t, db, r1, "L1")
	_ = MustAdd(t, db, "", "Root2")

	rollup, err := BuildRollup(db, nil)
	if err != nil {
		t.Fatalf("BuildRollup: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, rollup)
	got := buf.String()

	want := "Next: " + leaf
	if !strings.Contains(got, want) {
		t.Errorf("forest scope should append %q:\n%s", want, got)
	}
}

// u3 — When the scope's direct children are all leaves (no descendants
// of their own), the per-child block is padding: the headline already
// says everything worth saying at this granularity. Skip it entirely
// when no child is in flight.
func TestRenderSummary_LeafOnlyScope_SkipsPerChildBlockWhenNothingClaimed(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "FlatUmbrella")
	done1 := MustAdd(t, db, root, "D1")
	done2 := MustAdd(t, db, root, "D2")
	_ = MustAdd(t, db, root, "O1")   // open/available
	_ = MustAdd(t, db, root, "Canc") // will cancel
	canceled := MustAdd(t, db, root, "Canc2")
	MustDone(t, db, done1)
	MustDone(t, db, done2)
	if _, _, _, err := RunCancel(db, []string{canceled}, "reason", false, false, false, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	// Per-child rows look like `  <title> (<id>)...` — absence of that
	// pattern is the u3 invariant. (The u6 Next: line may mention a
	// claimable leaf's title; that mention is not a per-child row.)
	for _, childTitle := range []string{"D1", "D2", "O1", "Canc", "Canc2"} {
		if strings.Contains(got, "  "+childTitle+" (") {
			t.Errorf("leaf-only scope should skip per-child row for %q:\n%s", childTitle, got)
		}
	}
	// Headline still rendered.
	if !strings.Contains(got, "FlatUmbrella") {
		t.Errorf("headline should still render:\n%s", got)
	}
}

// u3 — When the leaf-only scope has claimed children, list ONLY the
// claimed ones; the other leaves (done/available/canceled/blocked) stay
// rolled into the headline counts. Gives the "who's working on what"
// signal without the bulk.
func TestRenderSummary_LeafOnlyScope_ListsOnlyClaimedChildren(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "FlatUmbrella")
	inflight := MustAdd(t, db, root, "BusyTask")
	quiet := MustAdd(t, db, root, "QuietTask")
	MustClaim(t, db, inflight, "1h")
	_ = quiet

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if !strings.Contains(got, "  BusyTask (") {
		t.Errorf("claimed leaf per-child row must appear:\n%s", got)
	}
	if strings.Contains(got, "  QuietTask (") {
		t.Errorf("non-claimed leaves must not have their own per-child row:\n%s", got)
	}
}

func TestRenderSummary_FormatsRollup(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Project")
	a := MustAdd(t, db, root, "Phase A")
	b := MustAdd(t, db, root, "Phase B")
	a1 := MustAdd(t, db, a, "A1")
	MustAdd(t, db, b, "B1")
	MustDone(t, db, a1)

	s, err := RunSummary(db, root)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()

	if !strings.Contains(got, "Project") || !strings.Contains(got, root) {
		t.Errorf("title/id missing:\n%s", got)
	}
	if !strings.Contains(got, "of") || !strings.Contains(got, "done") {
		t.Errorf("rollup line missing:\n%s", got)
	}
	if !strings.Contains(got, "Phase A") || !strings.Contains(got, "Phase B") {
		t.Errorf("direct children missing:\n%s", got)
	}
	// Phase A is fully done — should display its terminal state, not "0 of 0".
	if !strings.Contains(got, "Phase A") {
		t.Errorf("Phase A line missing")
	}
	// "Direct child line for Phase B should advertise its first available
	// task" — B1 is the only candidate.
	if !strings.Contains(got, b) {
		t.Errorf("Phase B id missing:\n%s", got)
	}
}
