package job

import "testing"

// fwk0j — RunOrientNoTasks never learned about --issues, so `job orient
// --issues` on an exhausted issue forest rendered the task-focused tree
// instead of the issue one. These tests pin the fix at the internal level:
// issues=true must pick trees from the issue side (focused issue root, or
// every issue root when none is focused), never from the task side.

// TestRunOrientNoTasks_Issues_FocusedIssueRoot_RendersIssueRootNotTaskRoot
// covers a task root with an available leaf (so the task frontier is not
// exhausted) alongside an exhausted, focused issue root. issues=true must
// render only the focused issue root.
func TestRunOrientNoTasks_Issues_FocusedIssueRoot_RendersIssueRootNotTaskRoot(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot := MustAdd(t, db, "", "Plan")
	MustAdd(t, db, taskRoot, "Available task leaf")

	bugs := MustAdd(t, db, "", "Bugs")
	issueLeaf := MustAdd(t, db, bugs, "Only issue leaf")
	mustSetKind(t, db, bugs, KindIssue)
	MustClaim(t, db, issueLeaf, "1h") // exhausts the issue root, sets issue focus to it

	if _, err := RunNextFiltered(db, "", TestActor, "", false, true); err == nil {
		t.Fatalf("precondition: expected the issue frontier to be exhausted")
	}
	if focus, err := GetFocusKind(db, TestActor, KindIssue); err != nil || focus == nil || focus.ShortID != bugs {
		t.Fatalf("precondition: expected issue focus on %s, got %+v, err %v", bugs, focus, err)
	}

	view, err := RunOrientNoTasks(db, TestActor, "No available tasks in focused root", false, true)
	if err != nil {
		t.Fatalf("RunOrientNoTasks(issues=true): %v", err)
	}
	if len(view.Trees) != 1 || view.Trees[0].Task.ShortID != bugs {
		t.Fatalf("Trees: got %v, want exactly the focused issue root %s", treeShortIDs(view.Trees), bugs)
	}
}

// TestRunOrientNoTasks_Issues_NoFocus_RendersEveryIssueRoot covers the same
// mixed forest but with no issue focus set: issues=true must render every
// issue root, and no task root, even though the task root's frontier is
// still open.
func TestRunOrientNoTasks_Issues_NoFocus_RendersEveryIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot := MustAdd(t, db, "", "Plan")
	MustAdd(t, db, taskRoot, "Available task leaf")

	bugsA := MustAdd(t, db, "", "Bugs A")
	leafA := MustAdd(t, db, bugsA, "Issue leaf A")
	mustSetKind(t, db, bugsA, KindIssue)
	MustClaim(t, db, leafA, "1h")

	bugsB := MustAdd(t, db, "", "Bugs B")
	leafB := MustAdd(t, db, bugsB, "Issue leaf B")
	mustSetKind(t, db, bugsB, KindIssue)
	MustClaim(t, db, leafB, "1h")

	// Claiming leafB moved the issue focus onto bugsB; release it so this
	// test exercises the no-focus, whole-issue-forest path.
	if _, err := ReleaseFocusKind(db, TestActor, KindIssue); err != nil {
		t.Fatalf("ReleaseFocusKind: %v", err)
	}

	if _, err := RunNextFiltered(db, "", TestActor, "", false, true); err == nil {
		t.Fatalf("precondition: expected the issue frontier to be exhausted")
	}

	view, err := RunOrientNoTasks(db, TestActor, "No available tasks in any issue tree", false, true)
	if err != nil {
		t.Fatalf("RunOrientNoTasks(issues=true): %v", err)
	}
	got := treeShortIDs(view.Trees)
	if len(got) != 2 || !contains(got, bugsA) || !contains(got, bugsB) {
		t.Fatalf("Trees: got %v, want exactly the two issue roots [%s %s]", got, bugsA, bugsB)
	}
	if contains(got, taskRoot) {
		t.Fatalf("Trees: got %v, task root %s must not render in the issue forest view", got, taskRoot)
	}
}

func treeShortIDs(trees []*OrientNode) []string {
	out := make([]string, 0, len(trees))
	for _, tr := range trees {
		out = append(out, tr.Task.ShortID)
	}
	return out
}
