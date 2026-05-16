package main

import (
	"encoding/json"
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
)

func TestCancel_Single_Happy(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Write red tests")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "no longer needed")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	wantHead := `Canceled: ` + id + ` "Write red tests"`
	if !strings.Contains(stdout, wantHead) {
		t.Errorf("missing headline:\n%s", stdout)
	}
	if !strings.Contains(stdout, `  reason: 16 chars · "no longer needed"`) {
		t.Errorf("missing reason line:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "canceled" {
		t.Errorf("status: got %q, want canceled", task.Status)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "canceled")
	if detail["reason"] != "no longer needed" {
		t.Errorf("event reason: got %v", detail["reason"])
	}
}

func TestCancel_Variadic(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	c := job.MustAdd(t, db, "", "C")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, b, c, "--reason", "scope cut")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.HasPrefix(stdout, "Canceled 3 tasks:\n") {
		t.Errorf("multi headline:\n%s", stdout)
	}
	for _, id := range []string{a, b, c} {
		if !strings.Contains(stdout, "- Canceled: "+id) {
			t.Errorf("missing per-id line for %s:\n%s", id, stdout)
		}
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{a, b, c} {
		task := job.MustGet(t, db, id)
		if task.Status != "canceled" {
			t.Errorf("%s: status=%q, want canceled", id, task.Status)
		}
	}
}

func TestCancel_AllOrNothing(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, "noExs", b, "--reason", "x")
	if err == nil {
		t.Fatal("expected error with invalid id")
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{a, b} {
		task := job.MustGet(t, db, id)
		if task.Status == "canceled" {
			t.Errorf("%s was canceled despite failure", id)
		}
	}
}

func TestCancel_Cascade_ClosesOpenDescendants(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	gc := job.MustAdd(t, db, c1, "GC")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", parent, "--reason", "pivot", "--cascade")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.Contains(stdout, "and 2 subtasks") {
		t.Errorf("missing cascade count:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{parent, c1, gc} {
		task := job.MustGet(t, db, id)
		if task.Status != "canceled" {
			t.Errorf("%s: status=%q, want canceled", id, task.Status)
		}
	}
}

func TestCancel_Cascade_SkipsAlreadyDone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	job.MustDone(t, db, c1)
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", parent, "--reason", "x", "--cascade"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	if t1 := job.MustGet(t, db, c1); t1.Status != "done" {
		t.Errorf("done child should remain done, got %q", t1.Status)
	}
	if t2 := job.MustGet(t, db, c2); t2.Status != "canceled" {
		t.Errorf("open child should be canceled, got %q", t2.Status)
	}
}

func TestCancel_MissingReason_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `cancel requires --reason "<text>"`) {
		t.Errorf("err: %v", err)
	}
}

func TestCancel_OnDone_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	job.MustDone(t, db, id)
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "task " + id + " is already done; cancel only applies to open work"
	if err.Error() != want {
		t.Errorf("err:\n  got:  %q\n  want: %q", err.Error(), want)
	}
}

func TestCancel_Idempotent_Single(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "x"); err != nil {
		t.Fatalf("first cancel: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "y")
	if err != nil {
		t.Fatalf("second cancel: %v", err)
	}
	want := "Already canceled: " + id + "\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestCancel_Idempotent_InVariadic(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, "--reason", "first"); err != nil {
		t.Fatalf("seed cancel: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, b, "--reason", "second")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.Contains(stdout, "already canceled: "+a) {
		t.Errorf("missing already-canceled line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Canceled: "+b) {
		t.Errorf("missing per-id line for %s:\n%s", b, stdout)
	}
}

func TestCancel_UnblocksDependents(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	dep := job.MustAdd(t, db, "", "Dep")
	if err := job.RunBlock(db, dep, a, job.TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, "--reason", "drop"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	bls, err := job.GetBlockers(db, dep)
	if err != nil {
		t.Fatalf("job.GetBlockers: %v", err)
	}
	if len(bls) != 0 {
		t.Errorf("expected dep unblocked, got %d blockers", len(bls))
	}
	depTask := job.MustGet(t, db, dep)
	detail, _ := job.GetLatestEventDetail(db, depTask.ID, "unblocked")
	if detail["reason"] != "blocker_canceled" {
		t.Errorf("unblock reason: got %v, want blocker_canceled", detail["reason"])
	}
}

func TestCancel_ClaimedByOtherErrors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "bob", "cancel", id, "--reason", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "claimed by alice") {
		t.Errorf("err: %v", err)
	}
}

func TestCancel_ClaimedByCallerSucceeds_DropsClaim(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "x"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "canceled" {
		t.Errorf("status: got %q, want canceled", task.Status)
	}
	if task.ClaimedBy != nil {
		t.Errorf("claim should be dropped, got %v", *task.ClaimedBy)
	}
	if task.ClaimExpiresAt != nil {
		t.Errorf("claim_expires_at should be cleared, got %v", *task.ClaimExpiresAt)
	}
}

func TestCancel_Md_Single_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Write red tests")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "no longer needed")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	want := "Canceled: " + id + " \"Write red tests\" as=alice\n  reason: 16 chars · \"no longer needed\"\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestCancel_Md_Multi_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, b, "--reason", "scope cut")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.HasPrefix(stdout, "Canceled 2 tasks:\n") {
		t.Errorf("headline:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Canceled: "+a+" \"A\"\n") {
		t.Errorf("missing a line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Canceled: "+b+" \"B\"\n") {
		t.Errorf("missing b line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  reason: 9 chars · \"scope cut\"\n") {
		t.Errorf("missing reason line:\n%s", stdout)
	}
}

func TestCancel_Md_Reason_Long_Truncates(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "T")
	db.Close()

	// 120-rune reason — well past the 60-rune preview window; needs an ellipsis.
	reason := strings.Repeat("abcdefghij ", 11) + "xyz" // 124 runes, spaces allow clean break
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", reason)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.Contains(stdout, "…\"") {
		t.Errorf("expected ellipsis in preview:\n%s", stdout)
	}
	// Char count must reflect the full rune length, not the truncated preview.
	wantCount := "124 chars"
	if !strings.Contains(stdout, wantCount) {
		t.Errorf("expected %q in:\n%s", wantCount, stdout)
	}
}

func TestCancel_Json_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "x", "--format=json")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if got["reason"] != "x" {
		t.Errorf("reason: %v", got["reason"])
	}
	if got["purged"] != false {
		t.Errorf("purged: %v", got["purged"])
	}
	canceled, ok := got["canceled"].([]any)
	if !ok || len(canceled) != 1 {
		t.Fatalf("canceled: %v", got["canceled"])
	}
	first := canceled[0].(map[string]any)
	if first["id"] != id || first["title"] != "X" {
		t.Errorf("canceled[0]: %v", first)
	}
}

func TestCancel_Purge_EraseTaskAndEvents(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child to purge")
	parentTask := job.MustGet(t, db, parent)
	childTask := job.MustGet(t, db, child)
	childID := childTask.ID
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", child, "--reason", "garbage"); err != nil {
		t.Fatalf("cancel --purge: %v", err)
	}

	db = openTestDB(t, dbFile)
	row, _ := job.GetTaskByShortID(db, child)
	if row != nil {
		t.Errorf("child task row should be erased")
	}
	var nEvents int
	db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ?", childID).Scan(&nEvents)
	if nEvents != 0 {
		t.Errorf("child events should be erased, got %d", nEvents)
	}
	// Purged event should appear on the parent.
	detail, err := job.GetLatestEventDetail(db, parentTask.ID, "purged")
	if err != nil {
		t.Fatalf("job.GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected purged event on parent")
	}
	if detail["purged_id"] != child {
		t.Errorf("purged_id: %v", detail["purged_id"])
	}
}

func TestCancel_Purge_RootTask_EventOnNullTaskID(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Root")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", id, "--reason", "drop"); err != nil {
		t.Fatalf("cancel --purge: %v", err)
	}

	db = openTestDB(t, dbFile)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id IS NULL AND event_type = 'purged'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected 1 orphan purged event, got %d", n)
	}
}

func TestCancel_Purge_MissingReason_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", id)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `cancel --purge requires --reason "<text>"`) {
		t.Errorf("err: %v", err)
	}
}

func TestCancel_Purge_RequiresCascade_WhenChildrenPresent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, parent, "Child")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", parent, "--reason", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "task " + parent + " has subtasks; add --cascade --yes to purge the subtree"
	if err.Error() != want {
		t.Errorf("err:\n  got:  %q\n  want: %q", err.Error(), want)
	}
}

func TestCancel_Purge_Cascade_Yes_Erases(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	gc := job.MustAdd(t, db, c1, "GC")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", "--cascade", "--yes", parent, "--reason", "drop"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{parent, c1, gc} {
		task, _ := job.GetTaskByShortID(db, id)
		if task != nil {
			t.Errorf("%s should be erased", id)
		}
	}
}

func TestCancel_Purge_Cascade_WithoutYes_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, parent, "C1")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", "--cascade", parent, "--reason", "drop")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "requires --yes") {
		t.Errorf("err: %v", err)
	}
	if !strings.Contains(err.Error(), "irrecoverable erasure of 2 tasks") {
		t.Errorf("err should name count: %v", err)
	}
}

func TestCancel_Purge_RemovesBlocksCleanly(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	if err := job.RunBlock(db, b, a, job.TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", "--purge", a, "--reason", "x"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	bls, err := job.GetBlockers(db, b)
	if err != nil {
		t.Fatalf("job.GetBlockers: %v", err)
	}
	if len(bls) != 0 {
		t.Errorf("expected no blockers, got %d", len(bls))
	}
}

func TestRemove_Unknown(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "remove", id)
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err: %v", err)
	}
}

// --- P4: Cancel cascade with status-aware destination ---

func TestCancel_CascadeLastChild_SiblingsAllDone_ParentGoesDone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	a := job.MustAdd(t, db, parent, "A")
	b := job.MustAdd(t, db, parent, "B")
	c := job.MustAdd(t, db, parent, "C")
	job.MustDone(t, db, a)
	job.MustDone(t, db, b)
	db.Close()

	// Cancel the last open child; parent should auto-close to "done" since a
	// sibling closed as done.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", c, "--reason", "dropped")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	p := job.MustGet(t, db, parent)
	if p.Status != "done" {
		t.Errorf("parent status = %q, want done (any-done sibling rule)", p.Status)
	}
}

func TestCancel_CascadeLastChild_SiblingsAllCanceled_ParentGoesCanceled(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	a := job.MustAdd(t, db, parent, "A")
	b := job.MustAdd(t, db, parent, "B")
	db.Close()

	// Cancel a first, then b — when b is canceled there are no done siblings,
	// so parent cascade-cancels.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", a, "--reason", "x"); err != nil {
		t.Fatalf("cancel a: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", b, "--reason", "y"); err != nil {
		t.Fatalf("cancel b: %v", err)
	}

	db = openTestDB(t, dbFile)
	p := job.MustGet(t, db, parent)
	if p.Status != "canceled" {
		t.Errorf("parent status = %q, want canceled (no done siblings)", p.Status)
	}
}

func TestCancel_CascadeMixedSiblings_AnyDoneWins(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	a := job.MustAdd(t, db, parent, "A")
	b := job.MustAdd(t, db, parent, "B")
	c := job.MustAdd(t, db, parent, "C")
	db.Close()

	// One sibling done, one canceled — cancel the last open; any-done rule
	// wins.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", b, "--reason", "x"); err != nil {
		t.Fatalf("cancel b: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	job.MustDone(t, db2, a)
	db2.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", c, "--reason", "y"); err != nil {
		t.Fatalf("cancel c: %v", err)
	}

	db = openTestDB(t, dbFile)
	p := job.MustGet(t, db, parent)
	if p.Status != "done" {
		t.Errorf("parent status = %q, want done (any-done rule wins over canceled)", p.Status)
	}
}

func TestCancel_Cascade_MultiLevel(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	gp := job.MustAdd(t, db, "", "Grandparent")
	p := job.MustAdd(t, db, gp, "Parent")
	child := job.MustAdd(t, db, p, "Child")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", child, "--reason", "x"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	parentTask := job.MustGet(t, db, p)
	gpTask := job.MustGet(t, db, gp)
	if parentTask.Status != "canceled" {
		t.Errorf("parent status = %q, want canceled (all-canceled subtree)", parentTask.Status)
	}
	if gpTask.Status != "canceled" {
		t.Errorf("grandparent status = %q, want canceled (all-canceled subtree)", gpTask.Status)
	}
}

func TestCancel_CascadeEvent_RecordsCascadeStatus(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	a := job.MustAdd(t, db, parent, "A")
	b := job.MustAdd(t, db, parent, "B")
	job.MustDone(t, db, a)
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", b, "--reason", "dropped")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}

	db = openTestDB(t, dbFile)
	parentTask := job.MustGet(t, db, parent)
	// The auto-close event is recorded against the parent task. In this case
	// the parent cascaded to done, so the event type is "done" with
	// detail.cascade_status = "done" and detail.trigger_kind = "cancel".
	detail, err := job.GetLatestEventDetail(db, parentTask.ID, "done")
	if err != nil {
		t.Fatalf("get event detail: %v", err)
	}
	if detail == nil {
		t.Fatalf("no 'done' event on parent")
	}
	if detail["auto_closed"] != true {
		t.Errorf("auto_closed missing/false in event detail: %+v", detail)
	}
	if detail["cascade_status"] != "done" {
		t.Errorf("cascade_status = %v, want 'done'", detail["cascade_status"])
	}
	if detail["trigger_kind"] != "cancel" {
		t.Errorf("trigger_kind = %v, want 'cancel'", detail["trigger_kind"])
	}
	if detail["triggered_by"] != b {
		t.Errorf("triggered_by = %v, want %q", detail["triggered_by"], b)
	}
}

// Regression: existing done-cascade still works (trigger_kind = "done" and
// destination always "done"). Uses the simplest one-child scenario from
// existing done tests.
func TestDone_Cascade_TriggerKindStillDone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", child)
	if err != nil {
		t.Fatalf("done: %v", err)
	}

	db = openTestDB(t, dbFile)
	p := job.MustGet(t, db, parent)
	if p.Status != "done" {
		t.Fatalf("parent status = %q, want done", p.Status)
	}
	detail, _ := job.GetLatestEventDetail(db, p.ID, "done")
	if detail["auto_closed"] != true {
		t.Errorf("auto_closed: %+v", detail)
	}
	// trigger_kind should be "done" for done-triggered cascades (will require
	// the helper to stamp both cases).
	if detail["trigger_kind"] != "done" {
		t.Errorf("trigger_kind = %v, want 'done'", detail["trigger_kind"])
	}
}

// JSON shape check: parsing a CanceledResult JSON shows auto_closed entries
// with their status. Placeholder — exact JSON shape is up to the implementer
// to add; this test accepts any JSON containing both the canceled parent
// short_id and the auto_closed parent.
func TestCancel_CascadeJSON_MentionsAutoClosedAncestor(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", child, "--reason", "x", "--format=json")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("parse json: %v\n%s", err, stdout)
	}
	canceled, ok := payload["canceled"].([]any)
	if !ok || len(canceled) == 0 {
		t.Fatalf("json has no 'canceled' list:\n%s", stdout)
	}
	first, ok := canceled[0].(map[string]any)
	if !ok {
		t.Fatalf("canceled[0] not an object:\n%s", stdout)
	}
	ancestors, ok := first["auto_closed_ancestors"].([]any)
	if !ok || len(ancestors) == 0 {
		t.Errorf("expected auto_closed_ancestors on the canceled entry:\n%s", stdout)
	}
}
