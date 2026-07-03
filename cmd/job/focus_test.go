package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// W7XoA — `job focus` CLI: show the actor's current focus (root id, title,
// availability line) or a clear no-focus message; `--clear` releases it and
// confirms. Deliberately no setter argument — claiming is the setter.

// gn9 — job focus prints the focused root or a clear no-focus message.
func TestFocus_ShowsFocusedRootOrNoFocusMessage(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "The active tree")
	leaf1 := job.MustAdd(t, db, root, "Leaf 1")
	job.MustAdd(t, db, root, "Leaf 2")
	db.Close()

	out, _, err := runCLI(t, dbFile, "--as", "alice", "focus")
	if err != nil {
		t.Fatalf("focus (none set): %v\n%s", err, out)
	}
	if !strings.Contains(out, "No focus set") {
		t.Errorf("no-focus message missing:\n%s", out)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leaf1, "1h"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err = runCLI(t, dbFile, "--as", "alice", "focus")
	if err != nil {
		t.Fatalf("focus: %v\n%s", err, out)
	}
	if !strings.Contains(out, root) || !strings.Contains(out, "The active tree") {
		t.Errorf("focus output must name the root id and title:\n%s", out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("focus output must carry an availability summary:\n%s", out)
	}
}

// Focus is per-actor: another identity sees no focus.
func TestFocus_PerActor(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	leaf := job.MustAdd(t, db, root, "Leaf")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leaf, "1h"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	out, _, err := runCLI(t, dbFile, "--as", "bob", "focus")
	if err != nil {
		t.Fatalf("focus as bob: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No focus set") {
		t.Errorf("bob must see no focus:\n%s", out)
	}
}

// B5x — job focus --clear releases and confirms.
func TestFocus_ClearReleasesAndConfirms(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	leaf := job.MustAdd(t, db, root, "Leaf")
	job.MustAdd(t, db, root, "Leaf 2")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leaf, "1h"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "--as", "alice", "focus", "--clear")
	if err != nil {
		t.Fatalf("focus --clear: %v\n%s", err, out)
	}
	if !strings.Contains(out, root) {
		t.Errorf("clear confirmation must name the released root:\n%s", out)
	}

	out, _, err = runCLI(t, dbFile, "--as", "alice", "focus")
	if err != nil {
		t.Fatalf("focus after clear: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No focus set") {
		t.Errorf("focus must be gone after --clear:\n%s", out)
	}

	db = openTestDB(t, dbFile)
	defer db.Close()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type = 'focus_released' AND actor = 'alice'",
	).Scan(&n); err != nil {
		t.Fatalf("count focus_released: %v", err)
	}
	if n != 1 {
		t.Errorf("focus_released events: got %d, want 1", n)
	}
}

// Clearing with nothing set is a friendly no-op.
func TestFocus_ClearWithoutFocus_NoOp(t *testing.T) {
	dbFile := setupCLI(t)

	out, _, err := runCLI(t, dbFile, "--as", "alice", "focus", "--clear")
	if err != nil {
		t.Fatalf("focus --clear with none set: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No focus set") {
		t.Errorf("no-op clear should say there was nothing to clear:\n%s", out)
	}
}
