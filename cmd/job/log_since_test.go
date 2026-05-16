package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
	"time"
)

func TestLogSince_FiltersEvents(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	t0 := time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec("UPDATE events SET created_at = ? WHERE event_type = 'created'", t0.Unix()); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := db.Exec("UPDATE events SET created_at = ? WHERE event_type = 'claimed'", t0.Add(2*time.Hour).Unix()); err != nil {
		t.Fatalf("update: %v", err)
	}
	db.Close()

	cutoff := t0.Add(1 * time.Hour).Format(time.RFC3339)
	stdout, _, err := runCLI(t, dbFile, "log", id, "--since", cutoff)
	if err != nil {
		t.Fatalf("log --since: %v", err)
	}
	if strings.Contains(stdout, "created:") {
		t.Errorf("created event should be filtered out:\n%s", stdout)
	}
	if !strings.Contains(stdout, "claimed") {
		t.Errorf("claimed event should be present:\n%s", stdout)
	}
}

func TestLogSince_BoundaryInclusive(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")

	t0 := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	if _, err := db.Exec("UPDATE events SET created_at = ? WHERE event_type = 'created'", t0.Unix()); err != nil {
		t.Fatalf("update: %v", err)
	}
	db.Close()

	cutoff := t0.Format(time.RFC3339)
	stdout, _, err := runCLI(t, dbFile, "log", id, "--since", cutoff)
	if err != nil {
		t.Fatalf("log --since: %v", err)
	}
	if !strings.Contains(stdout, "created:") {
		t.Errorf("event at exact cutoff should be included:\n%s", stdout)
	}
}

func TestLogSince_InvalidRFC3339_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "log", id, "--since", "yesterday")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "RFC3339") || !strings.Contains(err.Error(), "duration") {
		t.Errorf("err should mention both RFC3339 and duration accepted forms: %v", err)
	}
}

// P8 red: `job log` with no arg → events from all top-level trees.
func TestLog_NoArg_GlobalScope(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "log")
	if err != nil {
		t.Fatalf("log (no arg): %v", err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("global log missing %s:\n%s", a, stdout)
	}
	if !strings.Contains(stdout, b) {
		t.Errorf("global log missing %s:\n%s", b, stdout)
	}
}

// P8 red: `job log all` is a synonym for global scope.
func TestLog_All_Synonym(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "log", "all")
	if err != nil {
		t.Fatalf("log all: %v", err)
	}
	if !strings.Contains(stdout, a) || !strings.Contains(stdout, b) {
		t.Errorf("log all should show all top-level trees:\n%s", stdout)
	}
}

// P8 red: existing `job log <id>` unchanged — scoped to the subtree.
func TestLog_SpecificID_Unchanged(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "log", a)
	if err != nil {
		t.Fatalf("log %s: %v", a, err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("log %s should include events for it:\n%s", a, stdout)
	}
	if strings.Contains(stdout, b) {
		t.Errorf("log %s should NOT include sibling tree %s:\n%s", a, b, stdout)
	}
}

// P8 red: --since composes with global scope.
func TestLog_GlobalScope_SinceFilter(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")

	t0 := time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec(
		"UPDATE events SET created_at = ? WHERE task_id = (SELECT id FROM tasks WHERE short_id = ?)",
		t0.Unix(), a,
	); err != nil {
		t.Fatalf("update a: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE events SET created_at = ? WHERE task_id = (SELECT id FROM tasks WHERE short_id = ?)",
		t0.Add(3*time.Hour).Unix(), b,
	); err != nil {
		t.Fatalf("update b: %v", err)
	}
	db.Close()

	cutoff := t0.Add(1 * time.Hour).Format(time.RFC3339)
	stdout, _, err := runCLI(t, dbFile, "log", "--since", cutoff)
	if err != nil {
		t.Fatalf("log --since: %v", err)
	}
	if strings.Contains(stdout, a) {
		t.Errorf("pre-cutoff tree %s should be filtered out:\n%s", a, stdout)
	}
	if !strings.Contains(stdout, b) {
		t.Errorf("post-cutoff tree %s should appear:\n%s", b, stdout)
	}
}
