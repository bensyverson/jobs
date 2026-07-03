package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// WGThZ — `job orient --full` restores the unelided (pre-elision) view; the
// default remains the context-budget view that drops done-node history.

func seedOrientFullFixture(t *testing.T) (dbFile, doneLeaf string) {
	t.Helper()
	dbFile = setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	root := job.MustAdd(t, db, "", "Root")
	doneLeaf = job.MustAddDesc(t, db, root, "Done leaf", "spent instructions live here")
	if err := job.RunNote(db, doneLeaf, "progress trail", nil, job.TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	job.MustDone(t, db, doneLeaf)
	job.MustAdd(t, db, root, "Open leaf")
	return dbFile, doneLeaf
}

// 3MX — the default view elides done-leaf history; --full brings back the
// pre-change output (desc and notes on done nodes, no breadcrumb key).
func TestOrient_FullFlag_RestoresUnelidedView(t *testing.T) {
	dbFile, _ := seedOrientFullFixture(t)

	def, _, err := runCLI(t, dbFile, "orient")
	if err != nil {
		t.Fatalf("orient: %v\n%s", err, def)
	}
	if strings.Contains(def, "spent instructions live here") {
		t.Errorf("default view must elide done-leaf desc:\n%s", def)
	}
	if strings.Contains(def, "progress trail") {
		t.Errorf("default view must elide done-leaf notes:\n%s", def)
	}

	full, _, err := runCLI(t, dbFile, "orient", "--full")
	if err != nil {
		t.Fatalf("orient --full: %v\n%s", err, full)
	}
	if !strings.Contains(full, "spent instructions live here") {
		t.Errorf("--full must keep done-leaf desc:\n%s", full)
	}
	if !strings.Contains(full, "progress trail") {
		t.Errorf("--full must keep done-leaf notes:\n%s", full)
	}
	if strings.Contains(full, "completion_note") {
		t.Errorf("--full must not emit the breadcrumb key (pre-change output had none):\n%s", full)
	}
}
