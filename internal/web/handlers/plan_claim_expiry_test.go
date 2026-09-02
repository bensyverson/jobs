package handlers_test

import (
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// TestPlan_ClaimExpirySweep_AttributesToDefaultIdentity covers the bug found
// by the 9Ed0cE audit: /plan calls job.RunListFiltered with no Actor, so
// loading the dashboard while a claim has expired used to record a
// claim_expired event with an empty actor. The dashboard has no per-request
// viewer identity, so it must attribute a sweep it triggers to the
// database's default identity instead (project decision, 2026-09-02).
func TestPlan_ClaimExpirySweep_AttributesToDefaultIdentity(t *testing.T) {
	db := setupPlanTestDB(t)
	if err := job.SetDefaultIdentity(db, "ben"); err != nil {
		t.Fatalf("SetDefaultIdentity: %v", err)
	}

	leaf := mustAdd(t, db, "claude", "A leaf task", nil, nil)
	if err := job.RunClaim(db, leaf, "1s", "", "claude", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}
	if _, err := db.Exec("UPDATE tasks SET claim_expires_at = ? WHERE short_id = ?",
		job.CurrentNowFunc().Unix()-60, leaf); err != nil {
		t.Fatalf("force expiry: %v", err)
	}

	deps := newPlanDeps(t, db)
	fetchPlanAll(t, deps)

	events, err := job.GetEventsForTask(db, leaf)
	if err != nil {
		t.Fatalf("GetEventsForTask: %v", err)
	}
	var found *job.EventEntry
	for i := range events {
		if events[i].EventType == "claim_expired" {
			found = &events[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a claim_expired event, got events=%+v", events)
	}
	if found.Actor != "ben" {
		t.Errorf("claim_expired actor = %q, want the default identity %q", found.Actor, "ben")
	}
}
