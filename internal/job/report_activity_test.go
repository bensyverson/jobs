package job

import (
	"testing"
	"time"
)

// The histogram counts what the window figures count (decision 10 of
// project/2026-09-27-chart-panel-revision.md): created and done are the
// LeafFigures transitions attributed to their bucket, and claimed and blocked
// are those events on the same leaves.

// activityTotals sums a report's activity over its buckets.
func activityTotals(r Report) ActivityCount {
	var sum ActivityCount
	for _, a := range r.Activity {
		sum.Created += a.Created
		sum.Claimed += a.Claimed
		sum.Done += a.Done
		sum.Blocked += a.Blocked
	}
	return sum
}

// wantActivity compares the per-bucket counts of r.Activity, ignoring bounds.
func wantActivity(t *testing.T, r Report, want []ActivityCount) {
	t.Helper()
	if len(r.Activity) != len(want) {
		t.Fatalf("activity buckets = %d, want %d", len(r.Activity), len(want))
	}
	for i, a := range r.Activity {
		a.Start, a.End = time.Time{}, time.Time{}
		if a != want[i] {
			t.Errorf("bucket %d activity = %+v, want %+v", i, a, want[i])
		}
	}
}

// wantActivityMatchesLeaves is the invariant: the histogram's created and
// done segments sum to the window's created and done leaves.
func wantActivityMatchesLeaves(t *testing.T, label string, r Report) {
	t.Helper()
	sum := activityTotals(r)
	if sum.Created != r.Leaves.Created || sum.Done != r.Leaves.Done {
		t.Errorf("%s: activity sums to created=%d done=%d; leaves say created=%d done=%d",
			label, sum.Created, sum.Done, r.Leaves.Created, r.Leaves.Done)
	}
}

// A reopen→close cycle is one done, in the bucket of the final close; a
// close undone before Until is none; and the parent's automatic close is
// not a leaf's.
func TestActivity_DoneIsTheFinalCloseOfALeaf(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	c := f.add(f.add("", "Other"), "C")
	f.at(day(0, 2*time.Hour)).done(a)
	f.at(day(1, time.Hour)).reopen(a)
	f.at(day(1, 2*time.Hour)).done(c)
	f.at(day(2, time.Hour)).reopen(c)
	f.at(day(2, 2*time.Hour)).done(a)
	f.at(day(2, 3*time.Hour)).done(b) // auto-closes Plan

	r := f.report(daily(3))
	wantActivity(t, r, []ActivityCount{
		{Created: 3},
		{},
		{Done: 2},
	})
	wantActivityMatchesLeaves(t, "reopen", r)
}

// A leaf split since is a parent at Until and was not created as a leaf; its
// children were, in the bucket of the split. The plan root never counts.
func TestActivity_SplitLeafCountsItsChildren(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.add(root, "B")
	f.at(day(1, time.Hour)).split(a, "A1", "A2", "A3")

	r := f.report(daily(2))
	wantActivity(t, r, []ActivityCount{
		{Created: 1},
		{Created: 3},
	})
	wantActivityMatchesLeaves(t, "split", r)
}

// A canceled leaf is still a leaf created in the window, as Leaves.Created
// counts it, and it was never done.
func TestActivity_CanceledLeafWasCreatedNotDone(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.add(root, "B")
	f.at(day(1, time.Hour)).cancel(a)

	r := f.report(daily(2))
	wantActivity(t, r, []ActivityCount{
		{Created: 2},
		{},
	})
	if r.Leaves.Canceled != 1 {
		t.Errorf("Leaves.Canceled = %d, want 1", r.Leaves.Canceled)
	}
	wantActivityMatchesLeaves(t, "cancel", r)
}

// A claim on a task that is a parent at Until is not a leaf's claim, even if
// the task was a leaf when it was claimed.
func TestActivity_ClaimOnAParentAtUntilIsNotCounted(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	f.at(day(1, time.Hour))
	f.claim(a)
	f.claim(b)
	f.at(day(1, 2*time.Hour))
	f.add(a, "A1")

	r := f.report(daily(2))
	wantActivity(t, r, []ActivityCount{
		{Created: 1},
		{Created: 1, Claimed: 1},
	})
	wantActivityMatchesLeaves(t, "parent claim", r)
}

// A block on a parent is not a leaf's block; a block on a leaf is.
func TestActivity_BlockOnAParentIsNotCounted(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.add(a, "A1")
	b := f.add(root, "B")
	gate := f.add(root, "Gate")
	f.at(day(1, time.Hour))
	f.block(a, gate)
	f.block(b, gate)

	r := f.report(daily(2))
	wantActivity(t, r, []ActivityCount{
		{Created: 3},
		{Blocked: 1},
	})
	wantActivityMatchesLeaves(t, "blocked parent", r)
}

// A scoped report counts the scope's leaves only: not the scope task, and
// nothing outside it.
func TestActivity_ScopedToTheSubtree(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	r1 := f.add("", "One")
	a := f.add(r1, "A")
	b := f.add(r1, "B")
	r2 := f.add("", "Two")
	c := f.add(r2, "C")
	f.at(day(1, time.Hour))
	f.claim(a)
	f.claim(c)
	f.block(b, c)
	f.done(a)
	f.done(c)

	q := daily(2)
	q.Scope = r1
	r := f.report(q)
	wantActivity(t, r, []ActivityCount{
		{Created: 2},
		{Claimed: 1, Done: 1, Blocked: 1},
	})
	wantActivityMatchesLeaves(t, "scoped", r)
}

// The histogram sums to the window's figures for every window over one
// history: whole, clipped at either end, finer buckets, and an Until before
// a later reopen or split.
func TestActivity_SumsToLeafFiguresInEveryWindow(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	c := f.add(root, "C")
	d := f.add(root, "D")
	f.add("", "Issue-free root leaf")
	f.at(day(1, time.Hour)).claim(a)
	f.at(day(1, 2*time.Hour)).done(a)
	f.at(day(1, 3*time.Hour))
	f.block(d, b)
	f.block(c, b)
	f.at(day(2, time.Hour)).done(b)
	f.at(day(2, 5*time.Hour)).reopen(a)
	f.at(day(3, time.Hour)).split(c, "C1", "C2")
	f.at(day(3, 2*time.Hour)).cancel(d)
	f.at(day(4, time.Hour)).done(a)

	windows := []struct {
		name string
		q    ReportQuery
	}{
		{"whole", daily(5)},
		{"clipped start", ReportQuery{Since: day(1, 90*time.Minute), Until: day(5), Bucket: BucketDay, Location: time.UTC}},
		{"before the reopen", ReportQuery{Since: day(0), Until: day(2, 3*time.Hour), Bucket: BucketDay, Location: time.UTC}},
		{"before the split", ReportQuery{Since: day(0), Until: day(3), Bucket: BucketHour, Location: time.UTC}},
		{"late", ReportQuery{Since: day(3, 90*time.Minute), Until: day(5), Bucket: BucketSixHours, Location: time.UTC}},
		{"automatic", ReportQuery{Until: day(5), Location: time.UTC}},
	}
	for _, w := range windows {
		r := f.report(w.q)
		wantActivityMatchesLeaves(t, w.name, r)
	}

	// Spot-check the whole window: A's done is at its final close on day 4;
	// C, split on day 3, gives way to its children in created, and its block
	// is a parent's.
	r := f.report(daily(5))
	wantActivity(t, r, []ActivityCount{
		{Created: 4},
		{Claimed: 1, Blocked: 1},
		{Done: 1},
		{Created: 2},
		{Done: 1},
	})
}
