package job

import "testing"

// lXi9K — An issue-tree root is open-ended by design (see
// docs/content/docs/concepts/tree-kinds.md): its lifetime is not bounded by
// the plan that surfaced it. The leaf-frontier auto-close cascade must never
// close an issue root just because its last open child closed — only an
// explicit `job done <root>` closes it. Intermediate parents inside an issue
// tree (an issue with its own task children) still auto-close as before;
// only the root itself is exempt.

// Closing the only child of an issue root must not auto-close the root.
func TestCascadeAutoClose_SkipsIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	bug := MustAdd(t, db, root.ShortID, "Flaky test")

	MustDone(t, db, bug)

	task := MustGet(t, db, root.ShortID)
	if task.Status == "done" {
		t.Errorf("issue root auto-closed when its last child closed; want it to stay open")
	}
}

// The cascade must not merely skip closing the root — it must stop there,
// not report it as auto-closed, and not report it as auto-closed via a
// cancel cascade either (the two trigger kinds share the same cascade).
func TestCascadeAutoClose_SkipsIssueRoot_NotInResult(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	bug := MustAdd(t, db, root.ShortID, "Flaky test")

	closed, _, err := RunDone(db, []string{bug}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("len(closed) = %d, want 1", len(closed))
	}
	for _, a := range closed[0].AutoClosedAncestors {
		if a.ShortID == root.ShortID {
			t.Errorf("issue root %s listed as auto-closed ancestor; want it exempt", root.ShortID)
		}
	}
}

// Intermediate parents inside an issue tree — an issue that owns its own
// task children — still auto-close as today. Only the issue-tree root is
// exempt from the cascade.
func TestCascadeAutoClose_IssueTree_ClosesIntermediateButNotRoot(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	issue := MustAdd(t, db, root.ShortID, "Crash on startup")
	leaf := MustAdd(t, db, issue, "Write regression test")

	MustDone(t, db, leaf)

	if got := MustGet(t, db, issue).Status; got != "done" {
		t.Errorf("intermediate parent status = %q, want done", got)
	}
	if got := MustGet(t, db, root.ShortID).Status; got == "done" {
		t.Errorf("issue root auto-closed via nested cascade; want it to stay open")
	}
}

// Canceling the last open child of an issue root must not auto-close the
// root either — cascadeAutoCloseAncestors backs both done and cancel.
func TestCascadeAutoClose_CancelSkipsIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	bug := MustAdd(t, db, root.ShortID, "Won't fix")

	if _, _, _, err := RunCancel(db, []string{bug}, "not reproducible", true, false, true, TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	task := MustGet(t, db, root.ShortID)
	if task.Status == "canceled" || task.Status == "done" {
		t.Errorf("issue root auto-closed via cancel cascade (status=%q); want it to stay open", task.Status)
	}
}

// An explicit `job done <issue-root>` is unaffected: it still closes the
// root directly (only the auto-close *cascade* is exempted).
func TestDone_ExplicitOnIssueRoot_StillCloses(t *testing.T) {
	db := SetupTestDB(t)
	root, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	bug := MustAdd(t, db, root.ShortID, "Some bug")
	MustDone(t, db, bug)

	MustDone(t, db, root.ShortID)

	if got := MustGet(t, db, root.ShortID).Status; got != "done" {
		t.Errorf("explicit done on issue root: status = %q, want done", got)
	}
}

// An issue root never closes on its own, so once its children are all
// closed it is the only open node in its tree. It must not become the
// answer to `next --issues`: a root is a container, not a unit of work.
func TestNextIssues_SkipsAnExhaustedIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leaf := MustAdd(t, db, bugs, "Leaf")
	MustDone(t, db, leaf)

	if got, err := RunNextFiltered(db, "", TestActor, "", false, true); err == nil {
		t.Fatalf("next --issues = %s, want ErrNoAvailableTasks", got.ShortID)
	}
	if got, err := RunNextFiltered(db, "", TestActor, "", true, true); err == nil {
		t.Fatalf("next --issues --include-parents = %s, want ErrNoAvailableTasks", got.ShortID)
	}
}
