package job

import (
	"testing"
	"time"
)

// Heartbeat extends a claim to at least the default window from now; it
// never shortens one. A claim taken for longer than the default keeps its
// deadline when heartbeated early.

func TestHeartbeat_DoesNotShortenALongClaim(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "2h")
	want := base.Add(2 * time.Hour).Unix()

	// One minute in, the holder heartbeats. now + default (30m) is far
	// short of the 2h deadline, so the deadline must not move.
	CurrentNowFunc = func() time.Time { return base.Add(time.Minute) }
	results, err := RunHeartbeat(db, []string{id}, TestActor)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	task := MustGet(t, db, id)
	if task.ClaimExpiresAt == nil {
		t.Fatal("claim_expires_at should still be set")
	}
	if *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %d, want %d (the original 2h deadline)", *task.ClaimExpiresAt, want)
	}
	if len(results) != 1 || results[0].ExpiresAt != want {
		t.Errorf("result: got %+v, want ExpiresAt %d", results, want)
	}

	detail, derr := GetLatestEventDetail(db, task.ID, "heartbeat")
	if derr != nil || detail == nil {
		t.Fatalf("heartbeat event missing: err=%v detail=%v", derr, detail)
	}
	if got, _ := detail["new_expires_at"].(float64); int64(got) != want {
		t.Errorf("new_expires_at: got %v, want %d", detail["new_expires_at"], want)
	}
}

func TestHeartbeat_ExtendsAClaimWithLessThanTheDefaultLeft(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "10m")

	CurrentNowFunc = func() time.Time { return base.Add(2 * time.Minute) }
	results, err := RunHeartbeat(db, []string{id}, TestActor)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	want := base.Add(2*time.Minute).Unix() + DefaultClaimTTLSeconds
	task := MustGet(t, db, id)
	if task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + default)", task.ClaimExpiresAt, want)
	}
	if len(results) != 1 || results[0].ExpiresAt != want {
		t.Errorf("result: got %+v, want ExpiresAt %d", results, want)
	}
}
