package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"testing"
)

// 9Ed0cE — `job status <id>` (subtree scope) triggers the read-time
// claim-expiry sweep via an unfiltered RunListFiltered call, the same
// duplicate of the `job list` bug: the ListFilter it builds never carried
// the resolved identity, so any claim_expired event it emitted recorded an
// empty actor. This pins the subtree branch to the resolved caller identity.
func TestStatus_Subtree_ClaimExpiredEvent_RecordsResolvedActor(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	id := job.MustAdd(t, db, parent, "Stale leaf")
	if err := job.RunClaim(db, id, "1s", "", "claimer", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}
	if _, err := db.Exec("UPDATE tasks SET claim_expires_at = ? WHERE short_id = ?",
		job.CurrentNowFunc().Unix()-60, id); err != nil {
		t.Fatalf("expire: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "resolver", "status", parent); err != nil {
		t.Fatalf("status: %v", err)
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
