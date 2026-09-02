package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"testing"
)

// 9Ed0cE — `job list` never resolved an identity onto ListFilter.Actor, so
// when the read-time claim-expiry sweep fired from `list` it recorded the
// claim_expired event with an empty actor. Every other read verb (status,
// orient, next, closed tail, issueroot) threads the resolved identity
// through. This regression pins `list` to the same behavior: the recorded
// claim_expired event's actor must be the caller's resolved identity, not "".
func TestList_ClaimExpiredEvent_RecordsResolvedActor(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Stale leaf")
	if err := job.RunClaim(db, id, "1s", "", "claimer", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}
	// Force the claim into the past so the read-time sweep treats it as
	// expired (mirrors the fabrication pattern in issueroot_test.go).
	if _, err := db.Exec("UPDATE tasks SET claim_expires_at = ? WHERE short_id = ?",
		job.CurrentNowFunc().Unix()-60, id); err != nil {
		t.Fatalf("expire: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "resolver", "list", "--all"); err != nil {
		t.Fatalf("list: %v", err)
	}

	db = openTestDB(t, dbFile)
	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == "claim_expired" {
			found = true
			if e.Actor != "resolver" {
				t.Errorf("claim_expired actor: got %q, want %q", e.Actor, "resolver")
			}
		}
	}
	if !found {
		t.Fatalf("no claim_expired event recorded for %s", id)
	}
}
