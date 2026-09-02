package signals

import (
	"context"
	"database/sql"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// Tests seed directly via SQL for two reasons: (1) each test can pin
// exact created_at values without racing the clock, and (2) we can
// drop tasks into arbitrary statuses ('done', 'canceled', …) without
// going through the job package's full state-transition flow.
// CurrentNowFunc is hookable for timestamps the job package writes,
// but raw SQL keeps these tests independent of that plumbing.

func seedTask(t *testing.T, db *sql.DB, shortID, title, status string, createdAt time.Time) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO tasks (short_id, title, description, status, sort_key, created_at, updated_at)
		VALUES (?, ?, '', ?, 0, ?, ?)
	`, shortID, title, status, createdAt.Unix(), createdAt.Unix())
	if err != nil {
		t.Fatalf("seed task %q: %v", shortID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	return id
}

func seedClaim(t *testing.T, db *sql.DB, taskID int64, actor string, claimedAt time.Time) {
	t.Helper()
	expiresAt := claimedAt.Unix() + 1800
	_, err := db.Exec(`
		UPDATE tasks SET status = 'claimed', claimed_by = ?, claim_expires_at = ?
		WHERE id = ?
	`, actor, expiresAt, taskID)
	if err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	seedEvent(t, db, taskID, "claimed", actor, claimedAt)
}

func seedEvent(t *testing.T, db *sql.DB, taskID int64, eventType, actor string, at time.Time) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO events (task_id, event_type, actor, detail, created_at)
		VALUES (?, ?, ?, '', ?)
	`, taskID, eventType, actor, at.Unix())
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedBlock(t *testing.T, db *sql.DB, blockedID, blockerID int64, at time.Time) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO blocks (blocker_id, blocked_id, created_at)
		VALUES (?, ?, ?)
	`, blockerID, blockedID, at.Unix())
	if err != nil {
		t.Fatalf("seed block: %v", err)
	}
}

// ------------------------------------------------------------------
// Activity histogram
// ------------------------------------------------------------------

func TestActivity_EmptyDatabase(t *testing.T) {
	db := job.SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)

	sig, err := Compute(context.Background(), db, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if got := sig.Activity.TotalEvents(); got != 0 {
		t.Errorf("TotalEvents: got %d, want 0", got)
	}
	for i, b := range sig.Activity.Buckets {
		if b.Total() != 0 {
			t.Errorf("bucket[%d] total: got %d, want 0", i, b.Total())
		}
	}
}

func TestActivity_CountsEventsInLast60m(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	tID := seedTask(t, db, "t1", "t", "available", base.Add(-2*time.Hour))

	// Three creates inside the 60m window.
	seedEvent(t, db, tID, "created", "u", base)
	seedEvent(t, db, tID, "created", "u", base.Add(-30*time.Minute))
	seedEvent(t, db, tID, "created", "u", base.Add(-59*time.Minute))
	// One create 90m ago — outside window.
	seedEvent(t, db, tID, "created", "u", base.Add(-90*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.Activity.TotalCreate != 3 {
		t.Errorf("TotalCreate: got %d, want 3", sig.Activity.TotalCreate)
	}
	if sig.Activity.TotalEvents() != 3 {
		t.Errorf("TotalEvents: got %d, want 3", sig.Activity.TotalEvents())
	}
}

func TestActivity_BucketPositionByMinutesAgo(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	tID := seedTask(t, db, "t1", "t", "available", base.Add(-2*time.Hour))

	// 30s ago → bucket[59] (floor(30s/60s) = 0 minutes ago).
	seedEvent(t, db, tID, "created", "u", base.Add(-30*time.Second))
	// 5m30s ago → floor(5.5) = 5 → bucket[54].
	seedEvent(t, db, tID, "created", "u", base.Add(-5*time.Minute-30*time.Second))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.Activity.Buckets[59].Create != 1 {
		t.Errorf("bucket[59].Create: got %d, want 1", sig.Activity.Buckets[59].Create)
	}
	if sig.Activity.Buckets[54].Create != 1 {
		t.Errorf("bucket[54].Create: got %d, want 1", sig.Activity.Buckets[54].Create)
	}
}

func TestActivity_SplitsByEventType(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	tID := seedTask(t, db, "t1", "t", "available", base.Add(-2*time.Hour))

	seedEvent(t, db, tID, "created", "u", base.Add(-5*time.Minute))
	seedEvent(t, db, tID, "created", "u", base.Add(-5*time.Minute))
	seedEvent(t, db, tID, "claimed", "u", base.Add(-4*time.Minute))
	seedEvent(t, db, tID, "done", "u", base.Add(-3*time.Minute))
	seedEvent(t, db, tID, "blocked", "u", base.Add(-2*time.Minute))
	// Event type outside the histogram's set — ignored.
	seedEvent(t, db, tID, "noted", "u", base.Add(-1*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.Activity.TotalCreate != 2 {
		t.Errorf("TotalCreate: got %d, want 2", sig.Activity.TotalCreate)
	}
	if sig.Activity.TotalClaim != 1 {
		t.Errorf("TotalClaim: got %d, want 1", sig.Activity.TotalClaim)
	}
	if sig.Activity.TotalDone != 1 {
		t.Errorf("TotalDone: got %d, want 1", sig.Activity.TotalDone)
	}
	if sig.Activity.TotalBlock != 1 {
		t.Errorf("TotalBlock: got %d, want 1", sig.Activity.TotalBlock)
	}
	if sig.Activity.TotalEvents() != 5 {
		t.Errorf("TotalEvents: got %d, want 5", sig.Activity.TotalEvents())
	}
}

// ------------------------------------------------------------------
// Newly blocked
// ------------------------------------------------------------------

func TestNewlyBlocked_EmptyWhenNoRecentBlocks(t *testing.T) {
	db := job.SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)

	sig, err := Compute(context.Background(), db, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.NewlyBlocked.Count != 0 {
		t.Errorf("Count: got %d, want 0", sig.NewlyBlocked.Count)
	}
	if sig.NewlyBlocked.Progress != 0 {
		t.Errorf("Progress: got %f, want 0", sig.NewlyBlocked.Progress)
	}
}

func TestNewlyBlocked_CountsEdgesInLast10m(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	aID := seedTask(t, db, "a", "a", "available", base.Add(-1*time.Hour))
	bID := seedTask(t, db, "b", "b", "available", base.Add(-1*time.Hour))
	cID := seedTask(t, db, "c", "c", "available", base.Add(-1*time.Hour))
	dID := seedTask(t, db, "d", "d", "available", base.Add(-1*time.Hour))

	// Edge 12m ago — outside window.
	seedBlock(t, db, bID, aID, base.Add(-12*time.Minute))
	// Two edges inside 10m window.
	seedBlock(t, db, cID, aID, base.Add(-5*time.Minute))
	seedBlock(t, db, dID, aID, base.Add(-2*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.NewlyBlocked.Count != 2 {
		t.Errorf("Count: got %d, want 2", sig.NewlyBlocked.Count)
	}
	if len(sig.NewlyBlocked.Items) != 2 {
		t.Fatalf("Items len: got %d, want 2", len(sig.NewlyBlocked.Items))
	}
	if sig.NewlyBlocked.Items[0].BlockedShortID != "d" {
		t.Errorf("Items[0].BlockedShortID: got %q, want %q",
			sig.NewlyBlocked.Items[0].BlockedShortID, "d")
	}
	if sig.NewlyBlocked.Items[0].WaitingOnShortID != "a" {
		t.Errorf("Items[0].WaitingOnShortID: got %q, want %q",
			sig.NewlyBlocked.Items[0].WaitingOnShortID, "a")
	}
}

func TestNewlyBlocked_ProgressSaturatesAtThreshold(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	rootID := seedTask(t, db, "r", "r", "available", base.Add(-1*time.Hour))
	count := NewlyBlockedThreshold + 1
	for i := range count {
		sid := string(rune('A' + i))
		id := seedTask(t, db, sid, sid, "available", base.Add(-1*time.Hour))
		seedBlock(t, db, id, rootID, base.Add(-2*time.Minute))
	}

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.NewlyBlocked.Count != count {
		t.Errorf("Count: got %d, want %d", sig.NewlyBlocked.Count, count)
	}
	if sig.NewlyBlocked.Progress != 1.0 {
		t.Errorf("Progress: got %f, want 1.0 (saturated)", sig.NewlyBlocked.Progress)
	}
	if len(sig.NewlyBlocked.Items) > NewlyBlockedItemLimit {
		t.Errorf("Items cap: got %d items, want <= %d",
			len(sig.NewlyBlocked.Items), NewlyBlockedItemLimit)
	}
}

// ------------------------------------------------------------------
// Longest active claim
// ------------------------------------------------------------------

func TestLongestClaim_AbsentWhenNoActiveClaims(t *testing.T) {
	db := job.SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)

	sig, err := Compute(context.Background(), db, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.LongestClaim.Present {
		t.Errorf("Present: got true, want false (no active claims)")
	}
}

func TestLongestClaim_ReturnsOldestActiveClaim(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	aID := seedTask(t, db, "a", "first", "available", base.Add(-1*time.Hour))
	bID := seedTask(t, db, "b", "second", "available", base.Add(-1*time.Hour))

	seedClaim(t, db, aID, "alice", base.Add(-10*time.Minute))
	seedClaim(t, db, bID, "bob", base.Add(-3*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.LongestClaim.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.LongestClaim.TaskShortID != "a" {
		t.Errorf("TaskShortID: got %q, want %q", sig.LongestClaim.TaskShortID, "a")
	}
	if sig.LongestClaim.Actor != "alice" {
		t.Errorf("Actor: got %q, want %q", sig.LongestClaim.Actor, "alice")
	}
	if sig.LongestClaim.DurationSeconds != 600 {
		t.Errorf("DurationSeconds: got %d, want 600", sig.LongestClaim.DurationSeconds)
	}
	if sig.LongestClaim.Progress < 0.32 || sig.LongestClaim.Progress > 0.34 {
		t.Errorf("Progress: got %f, want ~0.33", sig.LongestClaim.Progress)
	}
}

func TestLongestClaim_UsesMostRecentClaimEventWhenTaskWasReclaimed(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	aID := seedTask(t, db, "a", "reclaimed", "available", base.Add(-2*time.Hour))
	// Old 'claimed' event from a prior, since-released claim — must be ignored.
	seedEvent(t, db, aID, "claimed", "alice", base.Add(-90*time.Minute))
	// Current claim by bob started 7m ago.
	seedClaim(t, db, aID, "bob", base.Add(-7*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.LongestClaim.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.LongestClaim.Actor != "bob" {
		t.Errorf("Actor: got %q, want %q", sig.LongestClaim.Actor, "bob")
	}
	if sig.LongestClaim.DurationSeconds != 7*60 {
		t.Errorf("DurationSeconds: got %d, want %d", sig.LongestClaim.DurationSeconds, 7*60)
	}
}

func TestLongestClaim_ProgressSaturatesAtThreshold(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	aID := seedTask(t, db, "a", "stuck", "available", base.Add(-2*time.Hour))
	seedClaim(t, db, aID, "alice", base.Add(-80*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.LongestClaim.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.LongestClaim.Progress != 1.0 {
		t.Errorf("Progress: got %f, want 1.0 (saturated)", sig.LongestClaim.Progress)
	}
}

// ------------------------------------------------------------------
// Oldest todo
// ------------------------------------------------------------------

func TestOldestTodo_AbsentWhenNoAvailableTasks(t *testing.T) {
	db := job.SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)

	sig, err := Compute(context.Background(), db, now)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.OldestTodo.Present {
		t.Errorf("Present: got true, want false")
	}
}

func TestOldestTodo_PicksOldestUnclaimedUnblocked(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	// Oldest available — should be picked.
	oldestID := seedTask(t, db, "old", "oldest available", "available", base.Add(-3*24*time.Hour))
	_ = oldestID

	// Newer available — should not be picked.
	seedTask(t, db, "new", "newer available", "available", base.Add(-1*time.Hour))

	// Very old claimed — excluded by status.
	claimedID := seedTask(t, db, "clm", "claimed ancient", "available", base.Add(-10*24*time.Hour))
	seedClaim(t, db, claimedID, "alice", base.Add(-1*time.Hour))

	// Very old but blocked — excluded by NOT EXISTS. The blocker itself
	// is marked done so it doesn't leak in as a candidate.
	blockerID := seedTask(t, db, "blk", "blocker", "done", base.Add(-9*24*time.Hour))
	blockedID := seedTask(t, db, "bld", "blocked ancient", "available", base.Add(-9*24*time.Hour))
	seedBlock(t, db, blockedID, blockerID, base.Add(-1*time.Hour))

	// Done task — excluded by status.
	doneID := seedTask(t, db, "dn", "done long ago", "done", base.Add(-20*24*time.Hour))
	_ = doneID

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.OldestTodo.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.OldestTodo.TaskShortID != "old" {
		t.Errorf("TaskShortID: got %q, want %q", sig.OldestTodo.TaskShortID, "old")
	}
	if sig.OldestTodo.Title != "oldest available" {
		t.Errorf("Title: got %q, want %q", sig.OldestTodo.Title, "oldest available")
	}
	wantAge := int64(3 * 24 * 60 * 60)
	if sig.OldestTodo.AgeSeconds != wantAge {
		t.Errorf("AgeSeconds: got %d, want %d", sig.OldestTodo.AgeSeconds, wantAge)
	}
}

func TestOldestTodo_ProgressSaturatesAtThreshold(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	seedTask(t, db, "anc", "ancient", "available", base.Add(-30*24*time.Hour))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.OldestTodo.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.OldestTodo.Progress != 1.0 {
		t.Errorf("Progress: got %f, want 1.0", sig.OldestTodo.Progress)
	}
}

// ------------------------------------------------------------------
// Edge cases: window boundaries, exclusions, clamping
// ------------------------------------------------------------------

// An event at created_at == now is included (<= now); at created_at ==
// cutoff is excluded (> cutoff); at 59m + 1s is included and lands in
// the oldest bucket. This pins the SQL's half-open semantics.
func TestActivity_WindowBoundariesHalfOpen(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	tID := seedTask(t, db, "t1", "t", "available", base.Add(-2*time.Hour))

	// At cutoff exactly (60m ago) → excluded.
	seedEvent(t, db, tID, "created", "u", base.Add(-60*time.Minute))
	// At now exactly → included → bucket[59] (0 minutes ago).
	seedEvent(t, db, tID, "created", "u", base)
	// 59m + 1s ago → minutesAgo = 59 → bucket[0].
	seedEvent(t, db, tID, "created", "u", base.Add(-59*time.Minute-1*time.Second))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.Activity.TotalCreate != 2 {
		t.Errorf("TotalCreate: got %d, want 2 (cutoff excluded, now and -59m included)", sig.Activity.TotalCreate)
	}
	if sig.Activity.Buckets[59].Create != 1 {
		t.Errorf("bucket[59].Create: got %d, want 1", sig.Activity.Buckets[59].Create)
	}
	if sig.Activity.Buckets[0].Create != 1 {
		t.Errorf("bucket[0].Create: got %d, want 1", sig.Activity.Buckets[0].Create)
	}
}

// Multiple types landing in the same minute populate a single bucket's
// per-type fields independently — the stacked bar's components.
func TestActivity_StackedBucketMixedTypes(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)
	tID := seedTask(t, db, "t1", "t", "available", base.Add(-2*time.Hour))

	// All within the same minute → same bucket.
	seedEvent(t, db, tID, "created", "u", base.Add(-5*time.Minute-10*time.Second))
	seedEvent(t, db, tID, "claimed", "u", base.Add(-5*time.Minute-20*time.Second))
	seedEvent(t, db, tID, "done", "u", base.Add(-5*time.Minute-30*time.Second))
	seedEvent(t, db, tID, "blocked", "u", base.Add(-5*time.Minute-40*time.Second))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	b := sig.Activity.Buckets[54]
	if b.Create != 1 || b.Claim != 1 || b.Done != 1 || b.Block != 1 {
		t.Errorf("bucket[54]: got %+v, want all four types = 1", b)
	}
	if b.Total() != 4 {
		t.Errorf("bucket[54].Total: got %d, want 4", b.Total())
	}
}

// Items are newest-first regardless of insertion order — the ORDER BY
// on created_at DESC must beat row insertion order.
func TestNewlyBlocked_ItemsNewestFirst(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	rootID := seedTask(t, db, "r", "r", "available", base.Add(-1*time.Hour))
	aID := seedTask(t, db, "a", "a", "available", base.Add(-1*time.Hour))
	bID := seedTask(t, db, "b", "b", "available", base.Add(-1*time.Hour))
	cID := seedTask(t, db, "c", "c", "available", base.Add(-1*time.Hour))

	// Insert in scrambled order; created_at determines result order.
	seedBlock(t, db, bID, rootID, base.Add(-5*time.Minute)) // middle
	seedBlock(t, db, aID, rootID, base.Add(-8*time.Minute)) // oldest
	seedBlock(t, db, cID, rootID, base.Add(-2*time.Minute)) // newest

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := []string{"c", "b", "a"}
	if len(sig.NewlyBlocked.Items) != 3 {
		t.Fatalf("Items len: got %d, want 3", len(sig.NewlyBlocked.Items))
	}
	for i, w := range want {
		if sig.NewlyBlocked.Items[i].BlockedShortID != w {
			t.Errorf("Items[%d].BlockedShortID: got %q, want %q",
				i, sig.NewlyBlocked.Items[i].BlockedShortID, w)
		}
	}
}

// Count equal to the threshold should produce Progress = 1.0 exactly —
// not 0.999... or over — the "at threshold" label in the template
// depends on this.
func TestNewlyBlocked_ProgressExactlyAtThreshold(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	rootID := seedTask(t, db, "r", "r", "available", base.Add(-1*time.Hour))
	for i := range NewlyBlockedThreshold {
		sid := string(rune('A' + i))
		id := seedTask(t, db, sid, sid, "available", base.Add(-1*time.Hour))
		seedBlock(t, db, id, rootID, base.Add(-2*time.Minute))
	}

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if sig.NewlyBlocked.Count != NewlyBlockedThreshold {
		t.Errorf("Count: got %d, want %d", sig.NewlyBlocked.Count, NewlyBlockedThreshold)
	}
	if sig.NewlyBlocked.Progress != 1.0 {
		t.Errorf("Progress: got %f, want 1.0", sig.NewlyBlocked.Progress)
	}
}

// A task with claimed_by set but status = 'done' (a malformed snapshot
// we still don't want to surface) and a soft-deleted claimed task must
// both be skipped. Only a genuine active claim should show up.
func TestLongestClaim_ExcludesDoneAndDeleted(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	// Very old 'claimed' event on a now-done task — must not be picked.
	doneID := seedTask(t, db, "dn", "done task", "done", base.Add(-5*time.Hour))
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ? WHERE id = ?`, "alice", doneID); err != nil {
		t.Fatalf("update done claimed_by: %v", err)
	}
	seedEvent(t, db, doneID, "claimed", "alice", base.Add(-2*time.Hour))

	// Soft-deleted claimed task — must not be picked.
	delID := seedTask(t, db, "del", "deleted claimed", "claimed", base.Add(-5*time.Hour))
	if _, err := db.Exec(`UPDATE tasks SET claimed_by = ?, deleted_at = ? WHERE id = ?`,
		"bob", base.Unix(), delID); err != nil {
		t.Fatalf("update deleted claimed: %v", err)
	}
	seedEvent(t, db, delID, "claimed", "bob", base.Add(-90*time.Minute))

	// Real active claim — the only candidate.
	liveID := seedTask(t, db, "lv", "live", "available", base.Add(-1*time.Hour))
	seedClaim(t, db, liveID, "carol", base.Add(-5*time.Minute))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.LongestClaim.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.LongestClaim.TaskShortID != "lv" {
		t.Errorf("TaskShortID: got %q, want %q (done/deleted must be excluded)",
			sig.LongestClaim.TaskShortID, "lv")
	}
}

// A claimed event timestamp in the future (clock skew, backfill) must
// clamp to a non-negative duration rather than report a negative age.
func TestLongestClaim_FutureClaimTimestampClamps(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	aID := seedTask(t, db, "a", "skewed", "available", base.Add(-1*time.Hour))
	seedClaim(t, db, aID, "alice", base.Add(30*time.Second))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.LongestClaim.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.LongestClaim.DurationSeconds != 0 {
		t.Errorf("DurationSeconds: got %d, want 0 (future claimed_at clamped)",
			sig.LongestClaim.DurationSeconds)
	}
	if sig.LongestClaim.Progress != 0 {
		t.Errorf("Progress: got %f, want 0", sig.LongestClaim.Progress)
	}
}

// Soft-deleted available tasks must be excluded regardless of age.
func TestOldestTodo_ExcludesDeleted(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	// Very old but soft-deleted → ignored.
	delID := seedTask(t, db, "del", "deleted old", "available", base.Add(-30*24*time.Hour))
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = ? WHERE id = ?`,
		base.Unix(), delID); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	// Newer but live → picked.
	seedTask(t, db, "lv", "live newer", "available", base.Add(-2*time.Hour))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.OldestTodo.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.OldestTodo.TaskShortID != "lv" {
		t.Errorf("TaskShortID: got %q, want %q (deleted must be excluded)",
			sig.OldestTodo.TaskShortID, "lv")
	}
}

// A future-dated created_at must clamp age to 0 rather than going
// negative — same invariant as the claim card, different field.
func TestOldestTodo_FutureCreatedAtClamps(t *testing.T) {
	db := job.SetupTestDB(t)
	base := time.Unix(1_700_000_000, 0)

	seedTask(t, db, "fut", "future", "available", base.Add(1*time.Hour))

	sig, err := Compute(context.Background(), db, base)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !sig.OldestTodo.Present {
		t.Fatal("Present: got false, want true")
	}
	if sig.OldestTodo.AgeSeconds != 0 {
		t.Errorf("AgeSeconds: got %d, want 0 (future created_at clamped)",
			sig.OldestTodo.AgeSeconds)
	}
	if sig.OldestTodo.Progress != 0 {
		t.Errorf("Progress: got %f, want 0", sig.OldestTodo.Progress)
	}
}
