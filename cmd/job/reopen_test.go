package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
)

func TestReopen_Plain_DoesNotTouchDescendants(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "P")
	c := job.MustAdd(t, db, p, "C")
	if _, _, err := job.RunDone(db, []string{p}, true, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if strings.Contains(stdout, "subtasks") {
		t.Errorf("plain reopen should not mention subtasks:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	parent := job.MustGet(t, db, p)
	// Auto-claim fires by default, so parent ends up "claimed" not "available".
	if parent.Status != "claimed" {
		t.Errorf("parent: status=%q, want claimed (auto-claim fires on reopen)", parent.Status)
	}
	child := job.MustGet(t, db, c)
	if child.Status != "done" {
		t.Errorf("child: status=%q, want done (not reopened)", child.Status)
	}
}

func TestReopen_Cascade_ReopensAllDone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "P")
	c := job.MustAdd(t, db, p, "C")
	gc := job.MustAdd(t, db, c, "GC")
	if _, _, err := job.RunDone(db, []string{p}, true, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", p, "--cascade"); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{p, c, gc} {
		task := job.MustGet(t, db, id)
		if task.Status != "available" {
			t.Errorf("%s: status=%q, want available", id, task.Status)
		}
	}
}

func TestReopen_FromCanceled(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "--reason", "x"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", id); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	// Auto-claim fires by default after reopen, so status is "claimed".
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want claimed (auto-claim fires on reopen)", task.Status)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "reopened")
	if detail["from_status"] != "canceled" {
		t.Errorf("from_status: got %v, want canceled", detail["from_status"])
	}
}

func TestReopen_Cascade_IncludesCanceledDescendants(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "P")
	c := job.MustAdd(t, db, p, "C")
	db.Close()

	// Cancel parent with cascade — closes both p and c as canceled.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", p, "--cascade", "--reason", "x"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", p, "--cascade"); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{p, c} {
		if task := job.MustGet(t, db, id); task.Status != "available" {
			t.Errorf("%s: status=%q, want available", id, task.Status)
		}
	}
}

func TestReopen_EventShape_Cascade(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "P")
	c := job.MustAdd(t, db, p, "C")
	if _, _, err := job.RunDone(db, []string{p}, true, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", p, "--cascade"); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	db = openTestDB(t, dbFile)
	parent := job.MustGet(t, db, p)
	detail, _ := job.GetLatestEventDetail(db, parent.ID, "reopened")
	if detail["cascade"] != true {
		t.Errorf("cascade: got %v, want true", detail["cascade"])
	}
	children, ok := detail["reopened_children"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("reopened_children: got %v", detail["reopened_children"])
	}
	if children[0] != c {
		t.Errorf("child: got %v, want %s", children[0], c)
	}
}

// R2 — reopen auto-claims the task by default.
func TestReopen_AutoClaims_ByDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "LeafTask")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", id)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !strings.Contains(stdout, "claimed by alice") {
		t.Errorf("expected auto-claim line:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want claimed", task.Status)
	}
}

// R2 — --no-claim skips the claim step and omits the claim line.
func TestReopen_NoClaim_Flag_SkipsClaim(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "LeafTask")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", id, "--no-claim")
	if err != nil {
		t.Fatalf("reopen --no-claim: %v", err)
	}
	if strings.Contains(stdout, "claimed") {
		t.Errorf("--no-claim must not include claim line:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status: got %q, want available", task.Status)
	}
}

// R2 — --cascade suppresses auto-claim even on leaf nodes.
func TestReopen_Cascade_NoAutoClaim(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "Parent")
	c := job.MustAdd(t, db, p, "Child")
	if _, _, err := job.RunDone(db, []string{p}, true, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", p, "--cascade")
	if err != nil {
		t.Fatalf("reopen --cascade: %v", err)
	}
	if strings.Contains(stdout, "claimed") {
		t.Errorf("--cascade must not auto-claim:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{p, c} {
		task := job.MustGet(t, db, id)
		if task.Status != "available" {
			t.Errorf("%s: status=%q, want available (cascade suppresses auto-claim)", id, task.Status)
		}
	}
}

// R2 — output includes the task title.
func TestReopen_Output_IncludesTitle(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "MySpecialTask")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", id)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !strings.Contains(stdout, "MySpecialTask") {
		t.Errorf("output must include task title:\n%s", stdout)
	}
}

// The ack names what reopen put back besides the task itself: the ancestors
// the close had auto-closed, and the block edges it had removed.
func TestReopen_Ack_NamesReopenedAncestorsAndRestoredBlocks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "Rate limiting")
	leaf := job.MustAdd(t, db, p, "Middleware")
	d := job.MustAdd(t, db, "", "Metrics counter")
	if err := job.RunBlock(db, d, leaf, job.TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	job.MustDone(t, db, leaf)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", leaf, "--no-claim")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	for _, want := range []string{
		`Reopened: ` + leaf + ` "Middleware"`,
		`  Auto-reopened: ` + p + ` "Rate limiting"`,
		`  Re-blocked: ` + d + ` "Metrics counter" (blocked by ` + leaf + `)`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("ack missing %q:\n%s", want, stdout)
		}
	}
}
