package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `claim --next -m "…"` and `claim --next -F <path>` must record the note on
// the leaf the frontier picked, exactly as `claim <id> -m` does. The verb
// exited 0 and silently dropped the body before this.

func claimNextFixture(t *testing.T) (dbFile, leaf string) {
	t.Helper()
	dbFile = setupCLI(t)
	db := openTestDB(t, dbFile)
	plan := job.MustAdd(t, db, "", "Plan")
	leaf = job.MustAdd(t, db, plan, "Leaf")
	db.Close()
	return dbFile, leaf
}

func notedTexts(t *testing.T, dbFile, shortID string) []string {
	t.Helper()
	db := openTestDB(t, dbFile)
	events, err := job.GetEventsForTaskTree(db, shortID)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var out []string
	for _, e := range events {
		if e.EventType == "noted" {
			out = append(out, e.Detail)
		}
	}
	return out
}

func TestClaimNext_WithMessage_RecordsNotedEvent(t *testing.T) {
	dbFile, leaf := claimNextFixture(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "-q", "-m", "picking this up")
	if err != nil {
		t.Fatalf("claim --next -m: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+leaf) {
		t.Errorf("Claimed: line missing or naming the wrong leaf:\n%s", stdout)
	}

	notes := notedTexts(t, dbFile, leaf)
	if len(notes) == 0 {
		t.Fatal("claim --next -m recorded no noted event")
	}
	if !strings.Contains(strings.Join(notes, "\n"), "picking this up") {
		t.Errorf("noted detail should contain the message body; got %v", notes)
	}
}

func TestClaimNext_WithFileFlag_RecordsNotedEvent(t *testing.T) {
	dbFile, leaf := claimNextFixture(t)

	notePath := filepath.Join(t.TempDir(), "note.txt")
	body := "multi-line\nstarting context\nfrom a file"
	if err := os.WriteFile(notePath, []byte(body), 0o644); err != nil {
		t.Fatalf("write note file: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "-q", "-F", notePath); err != nil {
		t.Fatalf("claim --next -F: %v", err)
	}

	notes := notedTexts(t, dbFile, leaf)
	if !strings.Contains(strings.Join(notes, "\n"), "starting context") {
		t.Errorf("noted event should contain the file body; got %v", notes)
	}
}

func TestClaimNext_MessageAndFile_AreMutuallyExclusive(t *testing.T) {
	dbFile, leaf := claimNextFixture(t)

	_, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "-q", "-m", "inline", "-F", "/nonexistent")
	if err == nil {
		t.Fatal("expected -m with -F to be rejected on claim --next")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should name the conflict; got %v", err)
	}
	db := openTestDB(t, dbFile)
	task := job.MustGet(t, db, leaf)
	if task.Status == "claimed" {
		t.Error("a rejected body should abort before the claim lands")
	}
}

func TestClaimNext_NoMessage_NoNotedEvent(t *testing.T) {
	dbFile, leaf := claimNextFixture(t)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "-q"); err != nil {
		t.Fatalf("claim --next: %v", err)
	}
	if notes := notedTexts(t, dbFile, leaf); len(notes) != 0 {
		t.Errorf("plain claim --next should not emit a noted event; got %v", notes)
	}
}
