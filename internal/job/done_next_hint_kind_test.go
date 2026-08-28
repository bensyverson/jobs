package job

import "testing"

// The trailing "Next:" hint on a done ack crosses root trees when the closed
// task's own root is exhausted. `next`, `claim --next` and `orient` all skip
// issue-trees by default; the hint must agree, or closing planned work hands
// the operator a bug report as "what's next".

func TestDoneNextHintSkipsIssueTreeRoots(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	leaf := MustAdd(t, db, plan, "Only task leaf")
	bugs := MustAdd(t, db, "", "Bugs")
	issueLeaf := MustAdd(t, db, bugs, "Issue leaf")
	mustSetKind(t, db, bugs, KindIssue)

	MustDone(t, db, leaf)
	ctx, err := ComputeDoneContext(db, leaf, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if ctx.Next != nil {
		t.Errorf("Next = %s %q, want none (only an issue-tree leaf %s remains)",
			ctx.Next.ShortID, ctx.Next.Title, issueLeaf)
	}
}

func TestDoneNextHintPrefersTaskTreeOverIssueTree(t *testing.T) {
	db := SetupTestDB(t)
	planA := MustAdd(t, db, "", "Plan A")
	leafA := MustAdd(t, db, planA, "Leaf A")
	bugs := MustAdd(t, db, "", "Bugs")
	MustAdd(t, db, bugs, "Issue leaf")
	mustSetKind(t, db, bugs, KindIssue)
	planB := MustAdd(t, db, "", "Plan B")
	leafB := MustAdd(t, db, planB, "Leaf B")

	MustDone(t, db, leafA)
	ctx, err := ComputeDoneContext(db, leafA, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if ctx.Next == nil {
		t.Fatal("expected a Next hint naming the other task-tree leaf")
	}
	if ctx.Next.ShortID != leafB {
		t.Errorf("Next = %s, want the task-tree leaf %s (issue-trees are skipped)", ctx.Next.ShortID, leafB)
	}
}

// Guard against over-filtering: closing a leaf inside an issue-tree must
// still surface its own tree's remaining work. Kind is a root-level scope
// filter, not a ban on issue tasks appearing in a hint at all.
func TestDoneNextHintStaysInsideItsOwnIssueTree(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	first := MustAdd(t, db, bugs, "First issue")
	second := MustAdd(t, db, bugs, "Second issue")
	mustSetKind(t, db, bugs, KindIssue)

	MustDone(t, db, first)
	ctx, err := ComputeDoneContext(db, first, nil)
	if err != nil {
		t.Fatalf("ComputeDoneContext: %v", err)
	}
	if ctx.Next == nil || ctx.Next.ShortID != second {
		t.Errorf("Next = %v, want the sibling %s inside the same issue-tree", ctx.Next, second)
	}
}
