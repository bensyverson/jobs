package job

import (
	"bytes"
	"strings"
	"testing"
)

func TestIssueOpenCount_NoIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "Plan")

	has, open, err := IssueOpenCount(db)
	if err != nil {
		t.Fatalf("IssueOpenCount: %v", err)
	}
	if has {
		t.Error("has = true, want false with no issue roots")
	}
	if open != 0 {
		t.Errorf("open = %d, want 0", open)
	}
}

func TestIssueOpenCount_CountsRootAndOpenDescendants(t *testing.T) {
	db := SetupTestDB(t)
	_, bugs, _ := seedMixedRoots(t, db)
	// seedMixedRoots leaves `bugs` (root) and `issueLeaf` (child) both open.

	has, open, err := IssueOpenCount(db)
	if err != nil {
		t.Fatalf("IssueOpenCount: %v", err)
	}
	if !has {
		t.Fatal("has = false, want true")
	}
	if open != 2 {
		t.Errorf("open = %d, want 2 (root %s + its open leaf)", open, bugs)
	}
}

func TestIssueOpenCount_ExcludesDoneAndCanceled(t *testing.T) {
	db := SetupTestDB(t)
	_, bugs, issueLeaf := seedMixedRoots(t, db)
	// A second open sibling keeps the root open despite closing the first —
	// closing an only child cascade-closes its parent (see gotchas), which
	// would otherwise leave nothing open to distinguish from "all closed".
	MustAdd(t, db, bugs, "still open issue")
	MustDone(t, db, issueLeaf)

	has, open, err := IssueOpenCount(db)
	if err != nil {
		t.Fatalf("IssueOpenCount: %v", err)
	}
	if !has {
		t.Fatal("has = false, want true")
	}
	// The done leaf doesn't count; the root and its open sibling do.
	if open != 2 {
		t.Errorf("open = %d, want 2 (root + the still-open sibling)", open)
	}
}

func TestIssueOpenCount_MultipleIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	_, _, _ = seedMixedRoots(t, db)
	second := MustAdd(t, db, "", "More bugs")
	mustSetKind(t, db, second, KindIssue)

	has, open, err := IssueOpenCount(db)
	if err != nil {
		t.Fatalf("IssueOpenCount: %v", err)
	}
	if !has {
		t.Fatal("has = false, want true")
	}
	if open != 3 {
		t.Errorf("open = %d, want 3 (2 from first issue tree + 1 for the new root)", open)
	}
}

func TestScopeListResult_OpenForestKeepsOnlyRequestedKind(t *testing.T) {
	db := SetupTestDB(t)
	seedMixedRoots(t, db)

	result, err := RunListWithTail(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("RunListWithTail: %v", err)
	}
	if err := ScopeListResult(db, result, false, 0); err != nil {
		t.Fatalf("ScopeListResult: %v", err)
	}
	for _, n := range result.Open {
		if n.Task.Kind.IsIssue() {
			t.Errorf("task-scoped result kept an issue root %s", n.Task.ShortID)
		}
	}
	if len(result.Open) == 0 {
		t.Fatal("expected at least the task-tree root to remain")
	}
}

func TestScopeListResult_IssuesScopeKeepsOnlyIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	_, bugs, _ := seedMixedRoots(t, db)

	result, err := RunListWithTail(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("RunListWithTail: %v", err)
	}
	if err := ScopeListResult(db, result, true, 0); err != nil {
		t.Fatalf("ScopeListResult: %v", err)
	}
	if len(result.Open) != 1 || result.Open[0].Task.ShortID != bugs {
		t.Fatalf("issues-scoped Open = %v, want just %s", result.Open, bugs)
	}
}

func TestScopeListResult_ClosedTailScopedAndRecapped(t *testing.T) {
	db := SetupTestDB(t)
	// Bare closed roots (no children) land directly in the flat closed-tail
	// footer with no cascade or inline-merge complications, one per kind.
	taskRoot := MustAdd(t, db, "", "closed task root")
	MustDone(t, db, taskRoot)

	issueRoot := MustAdd(t, db, "", "closed issue root")
	mustSetKind(t, db, issueRoot, KindIssue)
	MustDone(t, db, issueRoot)

	result, err := RunListWithTail(db, ListFilter{ShowAll: true, ClosedTailCap: -1})
	if err != nil {
		t.Fatalf("RunListWithTail: %v", err)
	}
	if len(result.ClosedTail) != 2 {
		t.Fatalf("setup: want 2 closed-tail rows before scoping, got %d", len(result.ClosedTail))
	}

	if err := ScopeListResult(db, result, false, 0); err != nil {
		t.Fatalf("ScopeListResult: %v", err)
	}
	if len(result.ClosedTail) != 1 || result.ClosedTail[0].Task.ShortID != taskRoot {
		t.Fatalf("task-scoped ClosedTail = %v, want just %s", result.ClosedTail, taskRoot)
	}
	if result.ClosedTotal != 1 {
		t.Errorf("ClosedTotal = %d, want 1 (recomputed for the task-tree subset)", result.ClosedTotal)
	}
}

func TestScopeListResult_ClosedTailRespectsRequestedCap(t *testing.T) {
	db := SetupTestDB(t)
	for range 3 {
		r := MustAdd(t, db, "", "closed issue root")
		mustSetKind(t, db, r, KindIssue)
		MustDone(t, db, r)
	}

	result, err := RunListWithTail(db, ListFilter{ShowAll: true, ClosedTailCap: -1})
	if err != nil {
		t.Fatalf("RunListWithTail: %v", err)
	}
	if err := ScopeListResult(db, result, true, 2); err != nil {
		t.Fatalf("ScopeListResult: %v", err)
	}
	if len(result.ClosedTail) != 2 {
		t.Errorf("len(ClosedTail) = %d, want cap of 2", len(result.ClosedTail))
	}
	if result.ClosedTotal != 3 {
		t.Errorf("ClosedTotal = %d, want 3 (uncapped count for the scoped subset)", result.ClosedTotal)
	}
}

func TestRenderIssueRootList_OmitsIssueTreeTag(t *testing.T) {
	db := SetupTestDB(t)
	_, bugs, _ := seedMixedRoots(t, db)

	nodes, err := RunListFiltered(db, ListFilter{ShowAll: true})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	var issueRoots []*TaskNode
	for _, n := range nodes {
		if n.Task.Kind.IsIssue() {
			issueRoots = append(issueRoots, n)
		}
	}
	if len(issueRoots) == 0 {
		t.Fatal("setup: expected at least one issue root")
	}

	var buf bytes.Buffer
	RenderIssueRootList(&buf, issueRoots, nil, nil)
	out := buf.String()
	if strings.Contains(out, "issue-tree") {
		t.Errorf("RenderIssueRootList should not tag rows with issue-tree:\n%s", out)
	}
	if !strings.Contains(out, bugs) {
		t.Errorf("expected issue root %s in output:\n%s", bugs, out)
	}
}
