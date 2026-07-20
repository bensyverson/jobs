package main

import (
	"encoding/json"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

func TestStatus_Usage_AllTime_PrintsActivityReportNotBriefing(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Open leaf")
	d := job.MustAdd(t, db, root, "Done leaf")
	job.MustDone(t, db, d)

	stdout, _, err := runCLI(t, dbFile, "status", "--usage")
	if err != nil {
		t.Fatalf("status --usage: %v", err)
	}
	if !strings.Contains(stdout, "Usage (all-time)") {
		t.Errorf("missing Usage (all-time) header:\n%s", stdout)
	}
	// The briefing's identity line must not appear in usage mode.
	if strings.Contains(stdout, "Identity:") {
		t.Errorf("usage mode must not emit the briefing preamble:\n%s", stdout)
	}
}

func TestStatus_Usage_Since_WindowedHeader(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Open")
	d := job.MustAdd(t, db, root, "Recent")
	job.MustDone(t, db, d)

	stdout, _, err := runCLI(t, dbFile, "status", "--usage", "--since", "7d")
	if err != nil {
		t.Fatalf("status --usage --since: %v", err)
	}
	if !strings.Contains(stdout, "Usage (last 7d)") {
		t.Errorf("missing windowed header:\n%s", stdout)
	}
}

func TestStatus_Usage_SubtreeID(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Outside")
	sub := job.MustAdd(t, db, root, "Subtree")
	job.MustAdd(t, db, sub, "Sub open")
	d := job.MustAdd(t, db, sub, "Sub done")
	job.MustDone(t, db, d)

	stdout, _, err := runCLI(t, dbFile, "status", "--usage", sub)
	if err != nil {
		t.Fatalf("status --usage <id>: %v", err)
	}
	if !strings.Contains(stdout, "Usage (all-time)") {
		t.Errorf("missing header:\n%s", stdout)
	}
	// Done count should be 1 (the subtree-scoped sub done), not 2.
	if !strings.Contains(stdout, "done 1") {
		t.Errorf("subtree scope should report done 1:\n%s", stdout)
	}
}

func TestStatus_Usage_JSONShape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Open")
	d := job.MustAdd(t, db, root, "Done")
	job.MustDone(t, db, d)

	stdout, _, err := runCLI(t, dbFile, "status", "--usage", "--format", "json")
	if err != nil {
		t.Fatalf("status --usage --format json: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("invalid JSON:\n%s\nerr: %v", stdout, err)
	}
	counts, ok := parsed["counts"].(map[string]any)
	if !ok {
		t.Fatalf("missing counts object:\n%s", stdout)
	}
	if _, exists := counts["claimed"]; !exists {
		t.Errorf("counts.claimed missing (zeros preserved in JSON):\n%s", stdout)
	}
	vel, ok := parsed["velocity"].(map[string]any)
	if !ok {
		t.Fatalf("missing velocity object:\n%s", stdout)
	}
	if vel["window"] != "all-time" {
		t.Errorf("velocity.window = %v, want \"all-time\"", vel["window"])
	}
}

func TestStatus_SinceWithoutUsage_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	defer db.Close()
	job.MustAdd(t, db, "", "Root")

	_, _, err := runCLI(t, dbFile, "status", "--since", "7d")
	if err == nil {
		t.Fatal("expected --since without --usage to error, got nil")
	}
	if !strings.Contains(err.Error(), "--usage") {
		t.Errorf("error should mention --usage; got %v", err)
	}
}

// openTestDB is shared with commands_test.go.
