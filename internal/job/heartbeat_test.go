package job

import (
	"bytes"
	"encoding/json"
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

// A pre-commit run once saw expires_in_seconds come back 1799 instead of
// 1800 (cmd/job/heartbeat_test.go's TestHeartbeat_Json_Shape). The suspected
// cause: expires_at is stamped from CurrentNowFunc inside RunHeartbeat's
// transaction, but the JSON/text renderers read CurrentNowFunc again to
// compute expires_in_seconds — a second clock read that can land a tick
// later than the first and truncate the remaining time by one second. This
// pins the clock to advance by exactly one second between the two reads,
// reproducing the boundary deterministically instead of racing the real
// clock the way the flaky run did.
func TestHeartbeat_ExpiresInSecondsSurvivesAClockTickBeforeRender(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")

	base := time.Now().Truncate(time.Second)
	CurrentNowFunc = func() time.Time { return base }
	MustClaim(t, db, id, "10m")

	// RunHeartbeat stamps expires_at against `base`.
	results, err := RunHeartbeat(db, []string{id}, TestActor)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// The clock ticks a second forward before the result is rendered —
	// exactly the boundary that flaked in the pre-commit hook.
	CurrentNowFunc = func() time.Time { return base.Add(time.Second) }

	var buf bytes.Buffer
	if err := RenderHeartbeatJSON(&buf, results); err != nil {
		t.Fatalf("RenderHeartbeatJSON: %v", err)
	}
	var got struct {
		Heartbeat []struct {
			ExpiresInSeconds int64 `json:"expires_in_seconds"`
		} `json:"heartbeat"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if len(got.Heartbeat) != 1 || got.Heartbeat[0].ExpiresInSeconds != DefaultClaimTTLSeconds {
		t.Errorf("expires_in_seconds: got %+v, want %d — it must be derived from the same instant"+
			" expires_at was stamped against, not a fresh clock read at render time",
			got.Heartbeat, DefaultClaimTTLSeconds)
	}

	// The text renderer shares the same rounding: FormatDuration(1800s) is
	// "30m", so a truncated 1799s would also render "30m" and hide the bug —
	// assert the underlying seconds via the result struct instead.
	if len(results) != 1 || results[0].ExpiresAt-results[0].Now != DefaultClaimTTLSeconds {
		t.Errorf("result: got %+v, want ExpiresAt-Now == %d", results, DefaultClaimTTLSeconds)
	}
}
