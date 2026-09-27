package main

import (
	"encoding/json"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// block add and import both refuse an edge that would close a loop in the
// wait graph, so a healthy store never holds one. These tests seed a loop
// below that check (job.SeedBlockEdge — see its doc comment) to stand in for
// a store written before the check existed, and confirm `job status` finds
// and names it.

func TestStatus_CLI_Deadlock_Surfaces(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	job.SeedBlockEdge(t, db, a, b)
	job.SeedBlockEdge(t, db, b, a)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "Deadlock:") {
		t.Fatalf("expected 'Deadlock:' line in status:\n%s", stdout)
	}
	if !strings.Contains(stdout, a+" blocked by "+b) && !strings.Contains(stdout, b+" blocked by "+a) {
		t.Errorf("expected the loop's edges named in status:\n%s", stdout)
	}
	if !strings.Contains(stdout, "job block remove") {
		t.Errorf("expected a `job block remove` fix hint in status:\n%s", stdout)
	}
}

func TestStatus_CLI_Deadlock_NoneWhenClean(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Normal")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(stdout, "Deadlock:") {
		t.Errorf("clean store must not produce a Deadlock: line:\n%s", stdout)
	}
}

// Deadlock: lines follow Decision: — Next / Stale / Decision / Deadlock.
func TestStatus_CLI_Deadlock_OrderedAfterDecision(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	decTask := job.MustAdd(t, db, "", "PickStrategy")
	if _, err := job.RunLabelAdd(db, decTask, []string{"decision"}, "alice"); err != nil {
		t.Fatalf("label add: %v", err)
	}
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	job.SeedBlockEdge(t, db, a, b)
	job.SeedBlockEdge(t, db, b, a)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	decIdx := strings.Index(stdout, "Decision:")
	dlIdx := strings.Index(stdout, "Deadlock:")
	if decIdx == -1 {
		t.Fatalf("Decision: line missing:\n%s", stdout)
	}
	if dlIdx == -1 {
		t.Fatalf("Deadlock: line missing:\n%s", stdout)
	}
	if dlIdx < decIdx {
		t.Errorf("Deadlock: (pos %d) must appear after Decision: (pos %d):\n%s", dlIdx, decIdx, stdout)
	}
}

func TestStatus_CLI_Deadlock_JSON_Present(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	job.SeedBlockEdge(t, db, a, b)
	job.SeedBlockEdge(t, db, b, a)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON:\n%s\nerr: %v", stdout, err)
	}
	deadlocks, ok := got["deadlocks"].([]any)
	if !ok {
		t.Fatalf("deadlocks should be an array; got %T %v", got["deadlocks"], got["deadlocks"])
	}
	if len(deadlocks) != 1 {
		t.Fatalf("expected 1 deadlock, got %d: %v", len(deadlocks), deadlocks)
	}
	d := deadlocks[0].(map[string]any)
	for _, key := range []string{"chain", "blocked", "blocker", "fix"} {
		if _, ok := d[key]; !ok {
			t.Errorf("deadlock entry missing %q: %v", key, d)
		}
	}
}

func TestStatus_CLI_Deadlock_JSON_EmptyArrayWhenClean(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Normal")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	deadlocks, ok := got["deadlocks"].([]any)
	if !ok {
		t.Fatalf("deadlocks should be an array (possibly empty); got %T %v", got["deadlocks"], got["deadlocks"])
	}
	if len(deadlocks) != 0 {
		t.Errorf("expected no deadlocks, got %v", deadlocks)
	}
}

// Subtree scope (`status <id>`) still scans the whole store: a deadlock
// elsewhere is a store-wide bug, not something a subtree view should hide.
func TestStatus_CLI_Deadlock_SurfacesInSubtreeScope(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Child")
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	job.SeedBlockEdge(t, db, a, b)
	job.SeedBlockEdge(t, db, b, a)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "status", root, "--format=json")
	if err != nil {
		t.Fatalf("status %s --format=json: %v", root, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	deadlocks, ok := got["deadlocks"].([]any)
	if !ok || len(deadlocks) != 1 {
		t.Fatalf("expected 1 deadlock even scoped to an unrelated subtree; got %v", got["deadlocks"])
	}
}
