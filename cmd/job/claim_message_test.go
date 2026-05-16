package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `claim` accepts `-m "..."` for parity with `release` and `done`. The note
// records as a `noted` event before the `claimed` event in the same
// transaction, anchoring the agent's starting context at the head of the
// task's lifecycle rather than buried at the close.

func TestClaim_WithMessage_RecordsNotedEvent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "-m", "writing the red tests first")
	if err != nil {
		t.Fatalf("claim -m: %v", err)
	}
	if !strings.HasPrefix(stdout, "Claimed: "+id+" \"Task\"") {
		t.Errorf("Claimed: line missing or malformed:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var sawNoted, sawClaimed bool
	for _, e := range events {
		if e.EventType == "noted" {
			sawNoted = true
			if !strings.Contains(e.Detail, "writing the red tests first") {
				t.Errorf("noted detail should contain the message body; got %q", e.Detail)
			}
		}
		if e.EventType == "claimed" {
			sawClaimed = true
		}
	}
	if !sawNoted {
		t.Error("expected a noted event for `claim -m`")
	}
	if !sawClaimed {
		t.Error("expected a claimed event")
	}
}

// `-m @path` reads the note body from a file, mirroring done/release.
func TestClaim_WithMessage_AtPath_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	notePath := filepath.Join(t.TempDir(), "note.txt")
	body := "multi-line\nstarting context\nfrom a file"
	if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
		t.Fatalf("write note file: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "-m", "@"+notePath); err != nil {
		t.Fatalf("claim -m @file: %v", err)
	}

	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var sawBody bool
	for _, e := range events {
		if e.EventType == "noted" && strings.Contains(e.Detail, "starting context") {
			sawBody = true
		}
	}
	if !sawBody {
		t.Errorf("noted event should contain the file body; events: %+v", events)
	}
}

// `-m -` reads the note body from stdin, mirroring done/release.
func TestClaim_WithMessage_StdinDash(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	body := "piped note from stdin"
	if _, _, err := runCLIWithStdin(t, dbFile, body, "--as", "alice", "claim", id, "-m", "-"); err != nil {
		t.Fatalf("claim -m -: %v", err)
	}

	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var sawBody bool
	for _, e := range events {
		if e.EventType == "noted" && strings.Contains(e.Detail, "piped note from stdin") {
			sawBody = true
		}
	}
	if !sawBody {
		t.Errorf("noted event should contain stdin body; events: %+v", events)
	}
}

// No `-m` flag → no `noted` event lands. The plain claim shape is the
// common case and must stay clutter-free.
func TestClaim_NoMessage_NoNotedEvent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id); err != nil {
		t.Fatalf("claim: %v", err)
	}

	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	for _, e := range events {
		if e.EventType == "noted" {
			t.Errorf("plain claim should not emit a noted event; got %+v", e)
		}
	}
}
