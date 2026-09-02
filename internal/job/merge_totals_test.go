package job

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

// The summary line's "N tasks reconciled" is the count of the "Touched on
// both sides" section, and a row identical on both sides is not touched — not
// even a closed one still carrying an expired claim, which the merge used to
// normalise on every shared task and then count (Hirewell, 2026-09-02: 195
// reconciled against three listed).

func TestMerge_IdenticalRowsWithExpiredClaimsAreNotReconciled(t *testing.T) {
	clock := newMergeClock(t)
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		id := MustAdd(t, db, "", "Finished long ago")
		MustDone(t, db, id)
		if _, err := db.Exec(`UPDATE tasks SET claimed_by = 'ghost', claim_expires_at = ?
			WHERE short_id = ?`, CurrentNowFunc().Add(-time.Hour).Unix(), id); err != nil {
			t.Fatal(err)
		}
	})
	clock.advance(time.Minute)
	MustAdd(t, other, "", "Only over there")

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(report.Merged) != 0 {
		t.Errorf("an identical row is not touched, got %+v", report.Merged)
	}
	if report.Totals.TasksMerged != 0 {
		t.Errorf("tasks reconciled = %d, want 0", report.Totals.TasksMerged)
	}
	if md := report.Markdown(); strings.Contains(md, "Touched on both sides") {
		t.Errorf("the report lists a section for nothing:\n%s", md)
	}
}

func TestMerge_ReconciledCountMatchesTheTouchedSection(t *testing.T) {
	clock := newMergeClock(t)
	var shared string
	local, other, _, otherPath := divergedPair(t, func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared task")
	})
	clock.advance(time.Minute)
	if _, err := RunLabelAdd(other, shared, []string{"store"}, TestActor); err != nil {
		t.Fatal(err)
	}
	MustAdd(t, other, "", "Only over there")

	report, err := RunMerge(local, otherPath, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(report.Merged) != 1 || report.Merged[0].ShortID != shared {
		t.Fatalf("the labelled task should be the one touched, got %+v", report.Merged)
	}
	if report.Totals.TasksMerged != len(report.Merged) {
		t.Errorf("tasks reconciled = %d, but %d tasks are listed as touched",
			report.Totals.TasksMerged, len(report.Merged))
	}
	if md := report.Markdown(); !strings.Contains(md, "1 task reconciled") {
		t.Errorf("summary should count the one touched task:\n%s", md)
	}
}
