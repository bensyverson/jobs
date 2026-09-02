package job

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The claims family under apply.
//
// A claim is shared state — who holds a task and until when — so every change
// to it is an event, and apply is the only thing that writes the columns. The
// tests here pin the two properties that makes true: apply takes the expiry
// from the payload rather than the clock, and no handler in the family
// records an unpositioned event.

// mustApplyEnvelope applies one hand-built envelope in its own transaction.
// Hand-built rather than driven through a handler because these tests are
// about apply alone: given this event, these rows.
func mustApplyEnvelope(t *testing.T, db *sql.DB, e eventlog.Envelope) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := apply(tx, e); err != nil {
		t.Fatalf("apply %s: %v", e.Type, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// testEnvelope builds a positioned envelope for a hand-written event.
func testEnvelope(t *testing.T, typ EventType, task, actor string, ts int64, seq uint64, payload any) eventlog.Envelope {
	t.Helper()
	var data json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		data = b
	}
	return eventlog.Envelope{
		V:     eventlog.Version,
		Rep:   "TESTREP",
		Seq:   seq,
		TS:    ts,
		Actor: actor,
		Type:  eventlog.Type(typ),
		Task:  task,
		Data:  data,
	}
}

// freezeClock parks the wall clock somewhere apply must never read from.
func freezeClock(t *testing.T, at time.Time) {
	t.Helper()
	orig := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = orig })
	CurrentNowFunc = func() time.Time { return at }
}

// The whole point of the absolute `until`: a claimed event replayed on
// another machine, at another time, must reproduce the same expiry.
func TestApplyClaimed_TakesExpiryFromThePayloadNotTheClock(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	const eventTS = int64(1_700_000_000_000)
	const until = int64(1_700_000_900)
	freezeClock(t, time.Unix(1_900_000_000, 0))

	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-a", eventTS, 1,
		ClaimedPayload{Duration: "15m", ExpiresAt: until}))

	task := MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status = %q, want claimed", task.Status)
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != "agent-a" {
		t.Errorf("claimed_by = %v, want agent-a", task.ClaimedBy)
	}
	if task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != until {
		t.Errorf("claim_expires_at = %v, want the payload's %d", task.ClaimExpiresAt, until)
	}
	if task.UpdatedAt != eventTS/1000 {
		t.Errorf("updated_at = %d, want the event's ts/1000 = %d", task.UpdatedAt, eventTS/1000)
	}
}

// --force takes over a live claim: the new holder wins and the breadcrumbs
// stay in the payload, where a reverse-fold reads them.
func TestApplyClaimed_ForceOverridesTheLiveHolder(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-a", 1_700_000_000_000, 1,
		ClaimedPayload{Duration: "15m", ExpiresAt: 1_700_000_900}))
	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-b", 1_700_000_100_000, 2,
		ClaimedPayload{
			Duration: "15m", ExpiresAt: 1_700_001_000,
			WasClaimedBy: "agent-a", WasExpiresAt: 1_700_000_900,
		}))

	task := MustGet(t, db, id)
	if task.ClaimedBy == nil || *task.ClaimedBy != "agent-b" {
		t.Errorf("claimed_by = %v, want agent-b", task.ClaimedBy)
	}
	if task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != 1_700_001_000 {
		t.Errorf("claim_expires_at = %v, want the second claim's expiry", task.ClaimExpiresAt)
	}
}

func TestApplyReleased_ClearsTheClaimAndStampsFromTheEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")
	freezeClock(t, time.Unix(1_900_000_000, 0))

	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-a", 1_700_000_000_000, 1,
		ClaimedPayload{Duration: "15m", ExpiresAt: 1_700_000_900}))
	const releaseTS = int64(1_700_000_500_000)
	mustApplyEnvelope(t, db, testEnvelope(t, EventReleased, id, "agent-a", releaseTS, 2,
		ReleasedPayload{WasClaimedBy: "agent-a", WasExpiresAt: 1_700_000_900}))

	task := MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status = %q, want available", task.Status)
	}
	if task.ClaimedBy != nil || task.ClaimExpiresAt != nil {
		t.Errorf("release must clear both claim columns, got %v / %v", task.ClaimedBy, task.ClaimExpiresAt)
	}
	if task.UpdatedAt != releaseTS/1000 {
		t.Errorf("updated_at = %d, want %d", task.UpdatedAt, releaseTS/1000)
	}
}

func TestApplyClaimExpired_ClearsTheClaim(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")
	freezeClock(t, time.Unix(1_900_000_000, 0))

	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-a", 1_700_000_000_000, 1,
		ClaimedPayload{Duration: "15m", ExpiresAt: 1_700_000_900}))
	const expiredTS = int64(1_700_001_000_000)
	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimExpired, id, "agent-b", expiredTS, 2,
		ClaimExpiredPayload{WasClaimedBy: "agent-a", WasExpiresAt: 1_700_000_900}))

	task := MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status = %q, want available", task.Status)
	}
	if task.ClaimedBy != nil || task.ClaimExpiresAt != nil {
		t.Errorf("expiry must clear both claim columns, got %v / %v", task.ClaimedBy, task.ClaimExpiresAt)
	}
	if task.UpdatedAt != expiredTS/1000 {
		t.Errorf("updated_at = %d, want %d", task.UpdatedAt, expiredTS/1000)
	}
}

func TestApplyHeartbeat_SetsExpiryFromThePayload(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")
	freezeClock(t, time.Unix(1_900_000_000, 0))

	mustApplyEnvelope(t, db, testEnvelope(t, EventClaimed, id, "agent-a", 1_700_000_000_000, 1,
		ClaimedPayload{Duration: "15m", ExpiresAt: 1_700_000_900}))
	mustApplyEnvelope(t, db, testEnvelope(t, EventHeartbeat, id, "agent-a", 1_700_000_400_000, 2,
		HeartbeatPayload{NewExpiresAt: 1_700_002_200}))

	task := MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status = %q, want claimed", task.Status)
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != "agent-a" {
		t.Errorf("heartbeat must not change the holder, got %v", task.ClaimedBy)
	}
	if task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != 1_700_002_200 {
		t.Errorf("claim_expires_at = %v, want the payload's new_expires_at", task.ClaimExpiresAt)
	}
}

// claimsFamilyTypes is the set this leaf owns. Every one of them changes
// state, so every one of them must be recorded with a position — an
// unpositioned row is a line a rebuild cannot replay.
var claimsFamilyTypes = []EventType{
	EventClaimed, EventReleased, EventClaimExpired, EventHeartbeat,
}

func TestClaimsFamilyEventsAreAllPositioned(t *testing.T) {
	db := SetupTestDB(t)
	driveClaimsFamily(t, db)

	for _, typ := range claimsFamilyTypes {
		var total, positioned int
		if err := db.QueryRow(
			"SELECT COUNT(*), COUNT(CASE WHEN rep != '' AND seq != 0 THEN 1 END) FROM events WHERE event_type = ?",
			string(typ),
		).Scan(&total, &positioned); err != nil {
			t.Fatal(err)
		}
		if total == 0 {
			t.Errorf("the claim sequence recorded no %s event", typ)
			continue
		}
		if positioned != total {
			t.Errorf("%s: %d of %d rows carry no position; a claims handler still uses recordEvent",
				typ, total-positioned, total)
		}
	}
}

// maybeExtendClaim used to move claim_expires_at with no event at all. Under
// apply every state change is an event, so the auto-extend is a heartbeat.
func TestAutoExtendClaim_RecordsAHeartbeatEvent(t *testing.T) {
	orig := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = orig })

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "10m")

	CurrentNowFunc = func() time.Time { return base.Add(2 * time.Minute) }
	if err := RunNote(db, id, "still working", nil, TestActor); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		 WHERE t.short_id = ? AND e.event_type = 'heartbeat'`, id,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("auto-extend recorded %d heartbeat events, want 1", n)
	}

	want := base.Add(2*time.Minute).Unix() + DefaultClaimTTLSeconds
	task := MustGet(t, db, id)
	if task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at = %v, want %d", task.ClaimExpiresAt, want)
	}
}

// A write that does not extend the claim — the holder's claim already runs
// further out — must not record a heartbeat either. An event that changes
// nothing is noise in the log and in the dashboard.
func TestAutoExtendClaim_RecordsNothingWhenItWouldShorten(t *testing.T) {
	orig := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = orig })

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "4h")

	CurrentNowFunc = func() time.Time { return base.Add(10 * time.Minute) }
	if err := RunNote(db, id, "checkpoint", nil, TestActor); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = 'heartbeat'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a write that cannot extend recorded %d heartbeat events, want 0", n)
	}
}

// Read-time expiry writes, and it must write through the same path: the
// claim_expired event it records carries a position and clears the claim.
func TestExpireStaleClaims_RecordsAPositionedEvent(t *testing.T) {
	orig := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = orig })

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "10m")

	CurrentNowFunc = func() time.Time { return base.Add(20 * time.Minute) }
	if _, err := RunListFiltered(db, ListFilter{Actor: "reader"}); err != nil {
		t.Fatal(err)
	}

	task := MustGet(t, db, id)
	if task.Status != "available" || task.ClaimedBy != nil || task.ClaimExpiresAt != nil {
		t.Fatalf("stale claim survived a read verb: %s / %v / %v",
			task.Status, task.ClaimedBy, task.ClaimExpiresAt)
	}

	var rep string
	var seq int64
	if err := db.QueryRow(
		"SELECT rep, seq FROM events WHERE event_type = 'claim_expired'",
	).Scan(&rep, &seq); err != nil {
		t.Fatal(err)
	}
	if rep == "" || seq == 0 {
		t.Errorf("claim_expired recorded without a position: rep %q seq %d", rep, seq)
	}
}

// Nothing stale means nothing written: a read verb that finds no expired
// claim must not append an event or advance anything.
func TestExpireStaleClaims_WritesNothingWhenNoClaimIsStale(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "4h")

	var before int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := RunListFiltered(db, ListFilter{Actor: "reader"}); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("a read verb wrote %d events with nothing stale to expire", after-before)
	}
}
