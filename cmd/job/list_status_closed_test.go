package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// A done task whose parent is also done is never in the open tree; it lives
// only in the closed tail. `--status done` must still list it.
func TestListStatusDone_ListsDoneTaskUnderDoneParent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "open sibling")
	container := job.MustAdd(t, db, root, "Container")
	leaf := job.MustAdd(t, db, container, "nested leaf")
	job.MustDone(t, db, leaf) // closes the container too
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--status", "done")
	if err != nil {
		t.Fatalf("ls --status done: %v", err)
	}
	if !strings.Contains(stdout, "`"+leaf+"` nested leaf") {
		t.Errorf("expected the done leaf under a done parent, got:\n%s", stdout)
	}
}

func TestListStatusDone_ComposesWithLabelUnderDoneParent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "open sibling")
	container := job.MustAdd(t, db, root, "Container")
	labeled := job.MustAdd(t, db, container, "labeled leaf")
	plain := job.MustAdd(t, db, container, "plain leaf")
	db.Close()
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", labeled, "decision"); err != nil {
		t.Fatalf("label add: %v", err)
	}
	db = openTestDB(t, dbFile)
	job.MustDone(t, db, labeled)
	job.MustDone(t, db, plain)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--status", "done", "--label", "decision")
	if err != nil {
		t.Fatalf("ls --status done --label decision: %v", err)
	}
	if !strings.Contains(stdout, "`"+labeled+"` labeled leaf") {
		t.Errorf("expected the labeled done leaf, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "plain leaf") {
		t.Errorf("expected the unlabeled leaf filtered out, got:\n%s", stdout)
	}
}
