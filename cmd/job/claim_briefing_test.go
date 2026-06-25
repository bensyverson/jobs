package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `claim`, `claim-next`, and `done --claim-next` print the equivalent
// of `show <id>` after the existing one-line ack. The first line stays
// the scriptable signal ("Claimed: X 'Title' (expires in 30m)") so
// scripts grepping for "Claimed:" keep working; the briefing follows
// to fold the universal `claim X && show X` pattern into a single call.

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i+1]
	}
	return s
}

func TestClaim_PrintsBriefingAfterAck(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Write the thing", "A non-empty description.")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	wantFirst := "Claimed: " + id + " \"Write the thing\" (expires in 30m) as=alice\n"
	if firstLine(stdout) != wantFirst {
		t.Errorf("first-line ack mismatch:\n got %q\nwant %q", firstLine(stdout), wantFirst)
	}
	if !strings.Contains(stdout, "ID:           "+id) {
		t.Errorf("briefing missing `ID:           %s`:\n%s", id, stdout)
	}
	if !strings.Contains(stdout, "Title:        Write the thing") {
		t.Errorf("briefing missing `Title:` line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Description:\n  A non-empty description.") {
		t.Errorf("briefing missing `Description:` block:\n%s", stdout)
	}
}

func TestClaim_JsonNotAffected(t *testing.T) {
	// `claim --next --format=json` should emit JSON only — neither the
	// markdown ack nor the briefing.
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Write the thing")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "--format=json")
	if err != nil {
		t.Fatalf("claim --next --format=json: %v", err)
	}
	if strings.Contains(stdout, "Claimed:") {
		t.Errorf("JSON output should not contain the markdown ack:\n%s", stdout)
	}
	if strings.Contains(stdout, "ID:           ") {
		t.Errorf("JSON output should not contain the markdown briefing:\n%s", stdout)
	}
}

func TestClaimNext_PrintsBriefingAfterAck(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Take this", "What you need to know.")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next")
	if err != nil {
		t.Fatalf("claim --next: %v", err)
	}
	if !strings.HasPrefix(stdout, "Claimed: "+id+" \"Take this\" (expires in 30m) as=alice\n") {
		t.Errorf("first line of stdout should be the scriptable Claimed: ack, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ID:           "+id) {
		t.Errorf("briefing missing for claim --next:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Title:        Take this") {
		t.Errorf("briefing missing `Title:` line:\n%s", stdout)
	}
}

func TestDone_ClaimNext_PrintsBriefingForClaimed(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	// Siblings under a shared root so the default --claim-next scope (the
	// closed task's root subtree) reaches the next leaf. This test exercises
	// the briefing output, not cross-root claiming.
	root := job.MustAdd(t, db, "", "Batch")
	a := job.MustAdd(t, db, root, "Closer")
	b := job.MustAddDesc(t, db, root, "Next up", "What's next.")
	if err := job.RunClaim(db, a, "", "", "alice", false); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+b+" \"Next up\" (expires in 30m)") {
		t.Errorf("expected `Claimed: %s ...` line for the auto-claimed leaf:\n%s", b, stdout)
	}
	if !strings.Contains(stdout, "ID:           "+b) {
		t.Errorf("briefing missing for done --claim-next:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Title:        Next up") {
		t.Errorf("briefing missing `Title:` line:\n%s", stdout)
	}
}
