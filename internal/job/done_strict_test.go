package job

import (
	"strings"
	"testing"
)

// TestRunDone_StrictRefusesPendingCriteria covers the headline criterion of
// the strict-default close path: a plain `job done <id>` (cascade=false,
// forceCloseWithPending=false) refuses to close a task whose criteria list
// contains any pending entries.
func TestRunDone_StrictRefusesPendingCriteria(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "alpha"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	_, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected strict-default refusal, got nil error")
	}

	task := MustGet(t, db, id)
	if task.Status == "done" {
		t.Errorf("task should not have closed; status=%q", task.Status)
	}
}

// TestRunDone_StrictErrorPrefixIsGreppable pins the leading line of the
// refusal so retry-with-override automation can pattern-match it.
func TestRunDone_StrictErrorPrefixIsGreppable(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	_, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected strict-default refusal")
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	if !strings.HasPrefix(first, "cannot close: 3 pending criteria") {
		t.Errorf("first line should start with 'cannot close: 3 pending criteria'; got: %q", first)
	}
}

// TestRunDone_StrictErrorListsUnmarkedLabels verifies the refusal lists every
// pending criterion's label so the operator can see what was unmarked
// without re-running `job show`.
func TestRunDone_StrictErrorListsUnmarkedLabels(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	labels := []string{"alpha", "beta gamma", "delta-epsilon"}
	items := make([]Criterion, len(labels))
	for i, l := range labels {
		items[i] = Criterion{Label: l}
	}
	if _, err := RunAddCriteria(db, id, items, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	_, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected strict-default refusal")
	}
	for _, l := range labels {
		if !strings.Contains(err.Error(), l) {
			t.Errorf("error should list label %q; got:\n%s", l, err.Error())
		}
	}
}

// TestRunDone_StrictPendingCountAgreesWithListedLabels guards the off-by-one
// regression noted in the experience report ("would be embarrassing for the
// refusal to say '6 pending' and list 5"). The leading-line count must equal
// the number of label rows the error renders.
func TestRunDone_StrictPendingCountAgreesWithListedLabels(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{
		{Label: "p1"}, {Label: "p2"}, {Label: "p3"}, {Label: "p4"},
	}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	// Mark two as passed so the pending set is exactly {p1, p4}.
	if _, err := RunSetCriterion(db, id, "p2", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	if _, err := RunSetCriterion(db, id, "p3", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	_, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected strict-default refusal")
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	if !strings.HasPrefix(first, "cannot close: 2 pending criteria") {
		t.Errorf("count must be 2; got line: %q", first)
	}
	listedRows := 0
	for line := range strings.SplitSeq(err.Error(), "\n") {
		// The criterion-list rows are indented and start with "[ ]".
		if strings.Contains(line, "[ ]") {
			listedRows++
		}
	}
	if listedRows != 2 {
		t.Errorf("count(%d) should equal listed rows(%d):\n%s", 2, listedRows, err.Error())
	}
}

// TestRunDone_StrictFullyMarkedClosesCleanly confirms the strict path imposes
// no friction once every criterion has been resolved (passed/skipped/failed
// — anything but pending).
func TestRunDone_StrictFullyMarkedClosesCleanly(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{
		{Label: "x"}, {Label: "y"}, {Label: "z"},
	}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunSetCriterion(db, id, "x", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion x: %v", err)
	}
	if _, err := RunSetCriterion(db, id, "y", CriterionSkipped, TestActor); err != nil {
		t.Fatalf("RunSetCriterion y: %v", err)
	}
	if _, err := RunSetCriterion(db, id, "z", CriterionFailed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion z: %v", err)
	}

	closed, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("close should succeed when no criteria are pending: %v", err)
	}
	if len(closed) != 1 {
		t.Errorf("closed: got %d, want 1", len(closed))
	}
}

// TestRunDone_ForceCloseWithPendingClosesAndRecordsWaiver covers both halves
// of the override path: the close goes through, and the unmarked labels are
// preserved on the done event under "criteria_waived" so a reviewer can see
// what was deferred.
func TestRunDone_ForceCloseWithPendingClosesAndRecordsWaiver(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{
		{Label: "first"}, {Label: "second"},
	}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	closed, _, err := RunDone(db, []string{id}, false, "deferred", nil, TestActor, true, "")
	if err != nil {
		t.Fatalf("force-close should succeed: %v", err)
	}
	if len(closed) != 1 {
		t.Fatalf("closed: got %d, want 1", len(closed))
	}

	task := MustGet(t, db, id)
	if task.Status != "done" {
		t.Errorf("status: got %q, want done", task.Status)
	}
	detail, err := GetLatestEventDetail(db, task.ID, "done")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	waived, ok := detail["criteria_waived"].([]any)
	if !ok {
		t.Fatalf("done event detail should carry a criteria_waived array; got %T (detail=%v)", detail["criteria_waived"], detail)
	}
	got := make(map[string]bool, len(waived))
	for _, w := range waived {
		s, ok := w.(string)
		if !ok {
			t.Fatalf("criteria_waived entries should be strings; got %T", w)
		}
		got[s] = true
	}
	for _, want := range []string{"first", "second"} {
		if !got[want] {
			t.Errorf("criteria_waived missing %q; got %v", want, waived)
		}
	}
}

// TestRunDone_CascadeBypassesStrict pins the criterion that --cascade closes
// remain unaffected by the strict check ("children own the criteria"): a
// cascade close succeeds without per-row marking and without needing the
// override flag, even when the explicit target has pending criteria.
func TestRunDone_CascadeBypassesStrict(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	child := MustAdd(t, db, parent, "Child")
	if _, err := RunAddCriteria(db, parent, []Criterion{{Label: "still-pending"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria parent: %v", err)
	}
	if _, err := RunAddCriteria(db, child, []Criterion{{Label: "child-pending"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria child: %v", err)
	}

	closed, _, err := RunDone(db, []string{parent}, true, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("--cascade should bypass strict: %v", err)
	}
	if len(closed) != 1 || len(closed[0].CascadeClosed) != 1 {
		t.Errorf("closed: got %+v, want 1 target cascading 1 child", closed)
	}
}

// TestRunDone_NoCriteriaUnaffected guards against the strict-default rollout
// breaking the common case (most tasks carry no criteria; closing them must
// remain frictionless).
func TestRunDone_NoCriteriaUnaffected(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if _, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("close without criteria should remain unaffected: %v", err)
	}
	task := MustGet(t, db, id)
	if task.Status != "done" {
		t.Errorf("status: got %q, want done", task.Status)
	}
}

// TestRunCancel_PendingCriteriaUnaffected pins the cancel-side criterion:
// canceled work's criteria are moot, so RunCancel never invokes the
// strict-close gate.
func TestRunCancel_PendingCriteriaUnaffected(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "won't matter"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	if _, _, _, err := RunCancel(db, []string{id}, "won't ship", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel should succeed regardless of criteria: %v", err)
	}
	task := MustGet(t, db, id)
	if task.Status != "canceled" {
		t.Errorf("status: got %q, want canceled", task.Status)
	}
}
