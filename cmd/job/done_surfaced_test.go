package main

import (
	"encoding/json"
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
)

// `done` reports the still-open issues a closed task surfaced (Decision 5,
// project/2026-08-28-issues-ux.md): the close never refuses on their
// account, it just names them.

func TestDone_Surfaced_NamesOnlyTheOpenIssue(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	leaf := job.MustAdd(t, db, "", "Wire the router")
	openBug := job.MustAdd(t, db, "", "Router drops trailing slash")
	closedBug := job.MustAdd(t, db, "", "Typo in error message")
	if err := job.RunSetFoundIn(db, openBug, leaf, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(openBug): %v", err)
	}
	if err := job.RunSetFoundIn(db, closedBug, leaf, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(closedBug): %v", err)
	}
	job.MustDone(t, db, closedBug)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "Surfaced: 1 open issue — "+openBug) {
		t.Errorf("expected a Surfaced: line naming only %s:\n%s", openBug, stdout)
	}
	if strings.Contains(stdout, closedBug) {
		t.Errorf("closed surfaced issue %s should not be listed:\n%s", closedBug, stdout)
	}
}

func TestDone_Surfaced_PluralAndTitles(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	leaf := job.MustAdd(t, db, "", "Wire the router")
	bugA := job.MustAdd(t, db, "", "Bug A title")
	bugB := job.MustAdd(t, db, "", "Bug B title")
	if err := job.RunSetFoundIn(db, bugA, leaf, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(bugA): %v", err)
	}
	if err := job.RunSetFoundIn(db, bugB, leaf, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(bugB): %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	// GetSurfaced (reused from `show`'s Surfaced: query) orders by short_id,
	// which is arbitrary relative to creation order, so build the expected
	// line in whichever order that sort produces.
	first, second := bugA, bugB
	firstTitle, secondTitle := "Bug A title", "Bug B title"
	if bugB < bugA {
		first, second = bugB, bugA
		firstTitle, secondTitle = secondTitle, firstTitle
	}
	want := "Surfaced: 2 open issues — " + first + " \"" + firstTitle + "\", " + second + " \"" + secondTitle + "\""
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout:\n%s\nwant a line containing:\n%s", stdout, want)
	}
}

func TestDone_Surfaced_NoLineWhenNoneSurfaced(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	leaf := job.MustAdd(t, db, "", "A plain leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if strings.Contains(stdout, "Surfaced:") {
		t.Errorf("expected no Surfaced: line:\n%s", stdout)
	}
}

func TestDone_Surfaced_MultiID_PerTask(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	leafA := job.MustAdd(t, db, "", "Leaf A")
	leafB := job.MustAdd(t, db, "", "Leaf B")
	bugA := job.MustAdd(t, db, "", "Found from A")
	bugB := job.MustAdd(t, db, "", "Found from B")
	if err := job.RunSetFoundIn(db, bugA, leafA, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(bugA): %v", err)
	}
	if err := job.RunSetFoundIn(db, bugB, leafB, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(bugB): %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leafA, leafB)
	if err != nil {
		t.Fatalf("done: %v", err)
	}

	idxA := strings.Index(stdout, leafA)
	idxB := strings.Index(stdout, leafB)
	idxSurfA := strings.Index(stdout, "Surfaced: 1 open issue — "+bugA)
	idxSurfB := strings.Index(stdout, "Surfaced: 1 open issue — "+bugB)
	if idxA < 0 || idxB < 0 || idxSurfA < 0 || idxSurfB < 0 {
		t.Fatalf("expected both closed ids and both per-task Surfaced: lines:\n%s", stdout)
	}
	if !(idxA < idxSurfA && idxSurfA < idxB && idxB < idxSurfB) {
		t.Errorf("expected each Surfaced: line to follow its own task's block, in order:\n%s", stdout)
	}
}

func TestDone_Surfaced_JSONCarriesSurfacedOpen(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	leaf := job.MustAdd(t, db, "", "Wire the router")
	openBug := job.MustAdd(t, db, "", "Router drops trailing slash")
	if err := job.RunSetFoundIn(db, openBug, leaf, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf, "--format", "json")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	var out struct {
		Closed []struct {
			ID           string `json:"id"`
			SurfacedOpen []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"surfaced_open"`
		} `json:"closed"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(out.Closed) != 1 {
		t.Fatalf("closed count = %d, want 1", len(out.Closed))
	}
	if len(out.Closed[0].SurfacedOpen) != 1 || out.Closed[0].SurfacedOpen[0].ID != openBug {
		t.Fatalf("surfaced_open = %v, want [%s]", out.Closed[0].SurfacedOpen, openBug)
	}
}
