package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// Regression: `job next` must resolve the actor identity (softly, like
// status/orient) so focus scoping applies. It shipped passing a hardcoded
// "" actor, so GetFocus never saw the caller and bare `next` leaked out of
// the focused root — caught in end-to-end verification, invisible to the
// domain-level tests.
func TestNextCLI_RespectsFocus(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootA := job.MustAdd(t, db, "", "Alpha tree")
	job.MustAdd(t, db, rootA, "Alpha leaf")
	rootB := job.MustAdd(t, db, "", "Beta tree")
	leafB1 := job.MustAdd(t, db, rootB, "Beta leaf 1")
	leafB2 := job.MustAdd(t, db, rootB, "Beta leaf 2")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leafB1); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "--as", "alice", "next")
	if err != nil {
		t.Fatalf("next: %v\n%s", err, out)
	}
	if !strings.Contains(out, leafB2) {
		t.Errorf("bare next with focus on Beta: got %q, want %s", strings.TrimSpace(out), leafB2)
	}

	// `next all` stays the whole-DB frontier by design — focus narrows the
	// single-next default, never the explicit "show me everything" form.
	out, _, err = runCLI(t, dbFile, "--as", "alice", "next", "all")
	if err != nil {
		t.Fatalf("next all: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Alpha leaf") {
		t.Errorf("next all must remain forest-wide:\n%s", out)
	}
}

// The exhausted-focus loud failure reaches the CLI surface.
func TestNextCLI_FocusedRootExhausted_FailsLoudly(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootA := job.MustAdd(t, db, "", "Alpha tree")
	job.MustAdd(t, db, rootA, "Alpha leaf")
	rootB := job.MustAdd(t, db, "", "Beta tree")
	leafB := job.MustAdd(t, db, rootB, "Only beta leaf")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leafB); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, stderr, err := runCLI(t, dbFile, "--as", "alice", "next")
	if err == nil {
		t.Fatalf("next with exhausted focused root: want error, got:\n%s", out)
	}
	combined := out + stderr + err.Error()
	if !strings.Contains(combined, rootB) || !strings.Contains(combined, "focus --release") {
		t.Errorf("loud failure must name the root and escapes: %q", combined)
	}
}
