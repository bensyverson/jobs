package job_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// Event-detail "breadcrumb" fields let consumers reverse-fold a state
// transition without re-reading the entire event log up to the
// previous event. The web dashboard's time-travel scrubber is the
// first user, but any API consumer constructing incremental updates
// benefits — same shape as a CRM logging before/after of a status
// change.
//
// Schema additions covered by these tests:
//
//   was_status       — the task's status before the event
//                      (added to: done, claim_expired)
//   was_claimed_by   — the prior claimer (or "" if unclaimed)
//                      (added to: done, released, canceled, claimed
//                       when --force overrides; already on claim_expired)
//   was_expires_at   — the prior claim_expires_at unix seconds
//                      (or 0 if unclaimed) (added everywhere claim
//                       state can be lost: done, released, canceled,
//                       claim_expired, claimed when overriding)
//
// The auto-released event already records "prior_claimant"; this
// rename normalizes it to "was_claimed_by" for cross-event
// consistency. Single producer, no consumers (BUILD mode), so the
// rename is safe.
//
// Old events that pre-date these fields will simply have them
// missing; reverse-fold consumers should treat absence as "unknown
// prior state" and fall back to forward replay from a snapshot.

func latestEventDetail(t *testing.T, db *sql.DB, shortID, eventType string) map[string]any {
	t.Helper()
	var raw string
	err := db.QueryRow(`
		SELECT e.detail FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = ?
		ORDER BY e.id DESC LIMIT 1
	`, shortID, eventType).Scan(&raw)
	if err != nil {
		t.Fatalf("latestEventDetail(%q, %q): %v", shortID, eventType, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal detail: %v\nraw=%q", err, raw)
	}
	return m
}

func mustString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q in detail: %#v", key, m)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("key %q not a string: %T (%v)", key, v, v)
	}
	return s
}

func mustNumber(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q in detail: %#v", key, m)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("key %q not a number: %T (%v)", key, v, v)
	}
	return n
}

// --- done ---

func TestDone_RecordsWasStatusForAvailableTask(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "available-task")

	job.MustDone(t, db, id)
	d := latestEventDetail(t, db, id, "done")

	if got := mustString(t, d, "was_status"); got != "available" {
		t.Errorf("was_status = %q, want %q", got, "available")
	}
}

func TestDone_RecordsClaimBreadcrumbsWhenWasClaimed(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "claim-then-done")
	job.MustClaim(t, db, id, "30m")

	// Capture the claim's expires_at to compare against the breadcrumb.
	task := job.MustGet(t, db, id)
	if task.ClaimExpiresAt == nil {
		t.Fatalf("expected claim_expires_at after claim")
	}
	wantExpires := *task.ClaimExpiresAt

	job.MustDone(t, db, id)
	d := latestEventDetail(t, db, id, "done")

	if got := mustString(t, d, "was_status"); got != "claimed" {
		t.Errorf("was_status = %q, want %q", got, "claimed")
	}
	if got := mustString(t, d, "was_claimed_by"); got != job.TestActor {
		t.Errorf("was_claimed_by = %q, want %q", got, job.TestActor)
	}
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires)
	}
}

func TestDone_CascadeChildRecordsWasStatus(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "parent")
	child := job.MustAdd(t, db, parent, "child")

	if _, _, err := job.RunDone(db, []string{parent}, true, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("RunDone --cascade: %v", err)
	}

	d := latestEventDetail(t, db, child, "done")
	if got := mustString(t, d, "was_status"); got != "available" {
		t.Errorf("cascade child was_status = %q, want %q", got, "available")
	}
}

func TestDone_AutoCloseAncestorRecordsWasStatus(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "parent")
	child := job.MustAdd(t, db, parent, "only-child")

	job.MustDone(t, db, child)

	d := latestEventDetail(t, db, parent, "done")
	if got := mustString(t, d, "was_status"); got != "available" {
		t.Errorf("auto-closed parent was_status = %q, want %q", got, "available")
	}
}

// --- released ---

func TestRelease_RecordsClaimBreadcrumbs(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "claim-then-release")
	job.MustClaim(t, db, id, "30m")

	task := job.MustGet(t, db, id)
	wantExpires := *task.ClaimExpiresAt

	if err := job.RunRelease(db, id, "", job.TestActor); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	d := latestEventDetail(t, db, id, "released")
	if got := mustString(t, d, "was_claimed_by"); got != job.TestActor {
		t.Errorf("was_claimed_by = %q, want %q", got, job.TestActor)
	}
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires)
	}
}

// Auto-released parents (when an open child is added under a claimed
// parent) emit a `released` event. The site already recorded
// "prior_claimant"; this normalizes the field name to "was_claimed_by"
// so consumers don't need a per-trigger schema.
func TestRelease_AutoOnAddChildUsesNormalizedFieldName(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "parent")
	job.MustClaim(t, db, parent, "30m")

	parentTask := job.MustGet(t, db, parent)
	wantExpires := *parentTask.ClaimExpiresAt

	job.MustAdd(t, db, parent, "child") // triggers auto-release

	d := latestEventDetail(t, db, parent, "released")
	if got := mustString(t, d, "was_claimed_by"); got != job.TestActor {
		t.Errorf("was_claimed_by = %q, want %q", got, job.TestActor)
	}
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires)
	}
	// Old field name should not appear after the rename.
	if _, ok := d["prior_claimant"]; ok {
		t.Errorf("released event still carries deprecated key %q: %#v", "prior_claimant", d)
	}
}

// --- claim_expired ---

func TestClaimExpired_RecordsWasExpiresAt(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "expiring-task")
	job.MustClaim(t, db, id, "30m")

	task := job.MustGet(t, db, id)
	wantExpires := *task.ClaimExpiresAt

	// Force the claim past its TTL by rewriting expires_at directly.
	if _, err := db.Exec(`UPDATE tasks SET claim_expires_at = ? WHERE id = ?`, wantExpires-100000, task.ID); err != nil {
		t.Fatalf("force-expire: %v", err)
	}

	// The next RunClaim with --force won't trigger the sweep, but any
	// write that calls expireStaleClaimsInTx will. RunClaim's own
	// preflight does. We use RunRelease as a no-op-style trigger? No —
	// release requires the caller to be the holder. Use RunClaim with
	// --force by another actor, which runs the sweep first.
	if err := job.RunClaim(db, id, "30m", "", "other-actor", true); err != nil {
		t.Fatalf("RunClaim (forces sweep): %v", err)
	}

	d := latestEventDetail(t, db, id, "claim_expired")
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires-100000 {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires-100000)
	}
	// Existing field stays intact.
	if got := mustString(t, d, "was_claimed_by"); got != job.TestActor {
		t.Errorf("was_claimed_by = %q, want %q", got, job.TestActor)
	}
}

// --- claim --force ---

func TestClaim_ForceRecordsOverriddenClaimBreadcrumbs(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "stealable-task")

	// alice claims; bob overrides with --force.
	if err := job.RunClaim(db, id, "30m", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	task := job.MustGet(t, db, id)
	wantExpires := *task.ClaimExpiresAt

	if err := job.RunClaim(db, id, "30m", "", "bob", true); err != nil {
		t.Fatalf("bob force-claim: %v", err)
	}

	d := latestEventDetail(t, db, id, "claimed")
	if got := mustString(t, d, "was_claimed_by"); got != "alice" {
		t.Errorf("was_claimed_by = %q, want %q", got, "alice")
	}
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires)
	}
}

func TestClaim_FreshClaimOmitsOverrideBreadcrumbs(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "fresh-claim-task")

	if err := job.RunClaim(db, id, "30m", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}

	d := latestEventDetail(t, db, id, "claimed")
	// Fresh claims (no overridden prior holder) must not surface the
	// override breadcrumbs — consumers rely on absence to mean "this
	// claim displaced no one."
	if _, ok := d["was_claimed_by"]; ok {
		t.Errorf("fresh claim should not carry was_claimed_by: %#v", d)
	}
	if _, ok := d["was_expires_at"]; ok {
		t.Errorf("fresh claim should not carry was_expires_at: %#v", d)
	}
}

// --- canceled ---

func TestCancel_RecordsClaimBreadcrumbsWhenWasClaimed(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "claimed-then-canceled")
	job.MustClaim(t, db, id, "30m")

	task := job.MustGet(t, db, id)
	wantExpires := *task.ClaimExpiresAt

	if _, _, _, err := job.RunCancel(db, []string{id}, "no longer needed", false, false, false, job.TestActor); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	d := latestEventDetail(t, db, id, "canceled")
	if got := mustString(t, d, "was_status"); got != "claimed" {
		t.Errorf("was_status = %q, want %q", got, "claimed")
	}
	if got := mustString(t, d, "was_claimed_by"); got != job.TestActor {
		t.Errorf("was_claimed_by = %q, want %q", got, job.TestActor)
	}
	if got := int64(mustNumber(t, d, "was_expires_at")); got != wantExpires {
		t.Errorf("was_expires_at = %d, want %d", got, wantExpires)
	}
}
