package job

import (
	"database/sql"
	"testing"
	"time"
)

// setNow pins CurrentNowFunc to a fixed time and restores it on cleanup.
func setNow(t *testing.T, at time.Time) {
	t.Helper()
	orig := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return at }
	t.Cleanup(func() { CurrentNowFunc = orig })
}

// keepOpen is added as an always-open sibling leaf so the cascade does
// not auto-close its parent — that lets sibling Done tasks be exercised
// without the parent's status bloating the counts.
func keepOpen(t *testing.T, db *sql.DB, parent string) string {
	t.Helper()
	return MustAdd(t, db, parent, "Keep open")
}

func TestRunUsage_AllTimeForest_CountsByStatus(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	MustAdd(t, db, root, "Available leaf")
	claimed := MustAdd(t, db, root, "Claimed leaf")
	MustClaim(t, db, claimed, "1h")
	done := MustAdd(t, db, root, "Done leaf")
	MustDone(t, db, done)
	canceled := MustAdd(t, db, root, "Canceled leaf")
	if _, _, _, err := RunCancel(db, []string{canceled}, "test reason", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	// Open counts the root (available) + "Keep open" + "Available leaf" = 3.
	if u.Open != 3 {
		t.Errorf("Open = %d, want 3", u.Open)
	}
	if u.Claimed != 1 {
		t.Errorf("Claimed = %d, want 1", u.Claimed)
	}
	if u.Done != 1 {
		t.Errorf("Done = %d, want 1", u.Done)
	}
	if u.Canceled != 1 {
		t.Errorf("Canceled = %d, want 1", u.Canceled)
	}
	if u.Blocked != 0 {
		t.Errorf("Blocked = %d, want 0", u.Blocked)
	}
	if u.WindowKind != "all-time" {
		t.Errorf("WindowKind = %q, want \"all-time\"", u.WindowKind)
	}
	if u.ScopeID != nil {
		t.Errorf("ScopeID = %v, want nil for forest", u.ScopeID)
	}
	if u.ScopeShortID != "" {
		t.Errorf("ScopeShortID = %q, want empty for forest", u.ScopeShortID)
	}
	if u.SinceUnix != nil {
		t.Errorf("SinceUnix = %v, want nil for all-time", u.SinceUnix)
	}
}

func TestRunUsage_AllTimeForest_BlockedCount(t *testing.T) {
	db := SetupTestDB(t)
	setNow(t, time.Unix(1_700_000_000, 0))

	root := MustAdd(t, db, "", "Root")
	a := MustAdd(t, db, root, "A")
	b := MustAdd(t, db, root, "B")
	c := MustAdd(t, db, root, "C")
	// "B is blocked by A" and "B is blocked by C".
	if err := RunBlock(db, b, a, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	if err := RunBlock(db, b, c, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	_ = c

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.Blocked != 1 {
		t.Errorf("Blocked = %d, want 1 (only B, which has ≥1 non-done blocker)", u.Blocked)
	}
	// Open counts the root (available) + A + B + C = 4.
	if u.Open != 4 {
		t.Errorf("Open = %d, want 4", u.Open)
	}
}

func TestRunUsage_SubtreeScope_RestrictsCounts(t *testing.T) {
	db := SetupTestDB(t)
	setNow(t, time.Unix(1_700_000_000, 0))

	root := MustAdd(t, db, "", "Root")
	// Single always-open leaf at root level so root never auto-closes.
	keepOpen(t, db, root)
	sub := MustAdd(t, db, root, "Subtree")
	MustAdd(t, db, sub, "Sub available 1")
	MustAdd(t, db, sub, "Sub available 2")
	subDone := MustAdd(t, db, sub, "Sub done")
	MustDone(t, db, subDone)
	// Siblings outside scope.
	MustAdd(t, db, root, "Outside available")
	outsideDone := MustAdd(t, db, root, "Outside done")
	MustDone(t, db, outsideDone)

	subTask := MustGet(t, db, sub)
	u, err := RunUsage(db, &subTask.ID, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	// Open: the subtree-root itself + 2 available leaf children = 3.
	if u.Open != 3 {
		t.Errorf("Open = %d, want 3 (subtree only)", u.Open)
	}
	if u.Done != 1 {
		t.Errorf("Done = %d, want 1 (subtree only)", u.Done)
	}
	if u.Canceled != 0 {
		t.Errorf("Canceled = %d, want 0", u.Canceled)
	}
	if u.Claimed != 0 {
		t.Errorf("Claimed = %d, want 0", u.Claimed)
	}
	if u.ScopeID == nil || *u.ScopeID != subTask.ID {
		t.Errorf("ScopeID = %v, want %d", u.ScopeID, subTask.ID)
	}
	if u.ScopeShortID != sub {
		t.Errorf("ScopeShortID = %q, want %q", u.ScopeShortID, sub)
	}
}

func TestRunUsage_Windowed_ScopesEventsAndDoneInWindow(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	// Close one task long ago (before the window)
	origNowOuter := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return now.Add(-72 * time.Hour) }
	old := MustAdd(t, db, root, "Old done")
	MustDone(t, db, old)
	CurrentNowFunc = origNowOuter
	// Close one task recently (inside the 24h window)
	recent := MustAdd(t, db, root, "Recent done")
	MustDone(t, db, recent)

	since := now.Add(-24 * time.Hour).Unix()
	u, err := RunUsage(db, nil, &since)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.WindowKind != "windowed" {
		t.Errorf("WindowKind = %q, want \"windowed\"", u.WindowKind)
	}
	if u.DoneInWindow != 1 {
		t.Errorf("DoneInWindow = %d, want 1 (only the recent done event)", u.DoneInWindow)
	}
	// EventCount counts all events emitted within the window: created
	// and done for `recent`, plus the done alone for `old` if it falls
	// in window — but old's events are at -72h, outside the window.
	// So in-window events ≈ created+done for `recent` + ... ≥ 2.
	if u.EventCount < 2 {
		t.Errorf("EventCount = %d, want ≥ 2 (recent task created+done within window)", u.EventCount)
	}
}

func TestRunUsage_AllTime_DoneNumeratorCountsDoneEvents(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	d1 := MustAdd(t, db, root, "Done 1")
	MustDone(t, db, d1)
	d2 := MustAdd(t, db, root, "Done 2")
	MustDone(t, db, d2)
	// Reopen then re-done → should count as 2 done events for velocity numerator.
	re := MustAdd(t, db, root, "Relearner")
	MustDone(t, db, re)
	if _, err := RunReopen(db, re, false, TestActor); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	MustDone(t, db, re)

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	// Done task rows in scope: d1, d2, re = 3 (root's kept sibling is open, so root stays available).
	if u.Done != 3 {
		t.Errorf("Done (status rows) = %d, want 3", u.Done)
	}
	// Done events emitted overall: d1 + d2 + re (twice) = 4
	if u.DoneAllEvents != 4 {
		t.Errorf("DoneAllEvents = %d, want 4 (two single completions + one reopen-redone)", u.DoneAllEvents)
	}
}

func TestRunUsage_Velocity_AllTime_OverCalendarSpan(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	// Set the first event 100 days ago.
	origNow := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return now.Add(-100 * 24 * time.Hour) }
	d1 := MustAdd(t, db, root, "Done 1") // emits a "created" event pinned at -100d
	MustDone(t, db, d1)
	CurrentNowFunc = origNow

	for range 9 {
		d := MustAdd(t, db, root, "More done")
		MustDone(t, db, d)
	}

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.VelocityRate <= 0 {
		t.Errorf("VelocityRate = %v, want > 0", u.VelocityRate)
	}
	// 10 done events over exactly ~100 days.
	wantApproxC := 10.0 / 100.0
	if u.VelocityRate < wantApproxC*0.95 || u.VelocityRate > wantApproxC*1.05 {
		t.Errorf("VelocityRate = %v, want ~%v", u.VelocityRate, wantApproxC)
	}
	if u.VelocityDenominatorDays < 99 || u.VelocityDenominatorDays > 101 {
		t.Errorf("VelocityDenominatorDays = %v, want ~100", u.VelocityDenominatorDays)
	}
}

func TestRunUsage_Velocity_WindowedOverWindowDays(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	// Window: last 30 days. Close 3 tasks inside the window.
	for range 3 {
		d := MustAdd(t, db, root, "Recent done")
		MustDone(t, db, d)
	}
	// Also one done outside the window (100 days ago).
	origNow := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return now.Add(-100 * 24 * time.Hour) }
	old := MustAdd(t, db, root, "Old done")
	MustDone(t, db, old)
	CurrentNowFunc = origNow

	since := now.Add(-30 * 24 * time.Hour).Unix()
	u, err := RunUsage(db, nil, &since)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.DoneInWindow != 3 {
		t.Errorf("DoneInWindow = %d, want 3", u.DoneInWindow)
	}
	if u.VelocityDenominatorDays < 29 || u.VelocityDenominatorDays > 31 {
		t.Errorf("VelocityDenominatorDays = %v, want ~30", u.VelocityDenominatorDays)
	}
	want := 3.0 / 30.0
	if u.VelocityRate < want*0.95 || u.VelocityRate > want*1.05 {
		t.Errorf("VelocityRate = %v, want ~%v", u.VelocityRate, want)
	}
}

func TestRunUsage_EmptyDB_NoZeroPanic(t *testing.T) {
	db := SetupTestDB(t)
	setNow(t, time.Unix(1_700_000_000, 0))

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.Open != 0 || u.Done != 0 || u.Canceled != 0 || u.Claimed != 0 || u.Blocked != 0 {
		t.Errorf("expected all zero counts, got %+v", u)
	}
	if u.EventCount != 0 {
		t.Errorf("EventCount = %d, want 0", u.EventCount)
	}
	if u.FirstEventAt != 0 || u.LastEventAt != 0 {
		t.Errorf("First/Last = %d/%d, want 0/0", u.FirstEventAt, u.LastEventAt)
	}
	if u.VelocityRate != 0 {
		t.Errorf("VelocityRate = %v, want 0 when no events", u.VelocityRate)
	}
	if u.VelocityDenominatorDays != 0 {
		t.Errorf("VelocityDenominatorDays = %v, want 0 when no events", u.VelocityDenominatorDays)
	}
}

func TestRunUsage_DBFileSize_NonZero(t *testing.T) {
	db := SetupTestDB(t)
	setNow(t, time.Unix(1_700_000_000, 0))

	MustAdd(t, db, "", "Root")

	u, err := RunUsage(db, nil, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.DBFileSizeBytes <= 0 {
		t.Errorf("DBFileSizeBytes = %d, want > 0", u.DBFileSizeBytes)
	}
}

func TestRunUsage_FirstLastEventSpanSubtreeScoped(t *testing.T) {
	db := SetupTestDB(t)
	now := time.Unix(1_700_000_000, 0)
	setNow(t, now)

	root := MustAdd(t, db, "", "Root")
	keepOpen(t, db, root)
	// Outside subtree, created early.
	origNow := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return now.Add(-50 * 24 * time.Hour) }
	MustAdd(t, db, root, "Outside early")
	CurrentNowFunc = origNow
	// Subtree created later.
	sub := MustAdd(t, db, root, "Subtree")
	subDone := MustAdd(t, db, sub, "Sub done")
	MustDone(t, db, subDone)

	subTask := MustGet(t, db, sub)
	u, err := RunUsage(db, &subTask.ID, nil)
	if err != nil {
		t.Fatalf("RunUsage: %v", err)
	}
	if u.FirstEventAt <= 0 {
		t.Fatalf("FirstEventAt = %d, want > 0", u.FirstEventAt)
	}
	// The earliest subtree event should be far newer than the outside-early event.
	outsideEarly := now.Add(-50 * 24 * time.Hour).Unix()
	if u.FirstEventAt < outsideEarly+10*24*60*60 {
		t.Errorf("FirstEventAt = %d, want after the outside-early event (%d) since subtree scope excludes it",
			u.FirstEventAt, outsideEarly)
	}
}
