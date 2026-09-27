package job

import (
	"testing"
	"time"
)

// wantSample compares the counted fields of a sample, ignoring End.
func wantSample(t *testing.T, label string, got Sample, want Sample) {
	t.Helper()
	want.End = got.End
	if got != want {
		t.Errorf("%s: sample = %+v, want %+v", label, got, want)
	}
}

// A reopened leaf leaves Done and comes back when it closes again: the done
// line dips and recovers, and the leaf is counted once.
func TestReport_ReopenDipsThenRecovers(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, 10*time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.add(root, "B")
	f.at(day(1, 10*time.Hour)).done(a)
	f.at(day(2, 10*time.Hour)).reopen(a)
	f.at(day(3, 10*time.Hour)).done(a)

	r := f.report(daily(4))
	if len(r.Series) != 4 {
		t.Fatalf("series = %v, want 4 daily samples", seriesEnds(r))
	}
	wantSample(t, "end of day 0", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2})
	wantSample(t, "end of day 1", sampleEnding(t, r, day(2)), Sample{Scope: 2, Done: 1, Open: 1})
	wantSample(t, "end of day 2 (reopened)", sampleEnding(t, r, day(3)), Sample{Scope: 2, Open: 2})
	wantSample(t, "end of day 3", sampleEnding(t, r, day(4)), Sample{Scope: 2, Done: 1, Open: 1})
	if r.Leaves.Done != 1 {
		t.Errorf("Leaves.Done = %d, want 1 — a reopen→close cycle is one leaf", r.Leaves.Done)
	}
}

// Splitting a leaf retires it and admits its children at the split.
func TestReport_SplitRetiresLeafAndAdmitsChildren(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.at(day(1, time.Hour))
	kids := f.split(a, "A1", "A2")
	f.at(day(2, time.Hour)).done(kids[0])

	r := f.report(daily(3))
	wantSample(t, "before split", sampleEnding(t, r, day(1)), Sample{Scope: 1, Open: 1})
	wantSample(t, "after split", sampleEnding(t, r, day(2)), Sample{Scope: 2, Open: 2})
	wantSample(t, "child closed", sampleEnding(t, r, day(3)), Sample{Scope: 2, Done: 1, Open: 1})
	if r.Leaves.Created != 2 {
		t.Errorf("Leaves.Created = %d, want 2 — the split leaf is a parent at Until", r.Leaves.Created)
	}
}

// A canceled leaf leaves scope and is counted separately.
func TestReport_CanceledLeafLeavesScope(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	f.add(root, "A")
	b := f.add(root, "B")
	f.at(day(1, time.Hour)).cancel(b)

	r := f.report(daily(2))
	wantSample(t, "before cancel", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2})
	wantSample(t, "after cancel", sampleEnding(t, r, day(2)), Sample{Scope: 1, Open: 1, Canceled: 1})
	if r.Leaves.Canceled != 1 || r.Leaves.Open != 1 {
		t.Errorf("Leaves = %+v, want 1 canceled and 1 open", r.Leaves)
	}
}

// Issue leaves count; issue roots never do, even childless; parents never do;
// a childless task root is a leaf and a plan.
func TestReport_IssueLeavesCountIssueRootsDoNot(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	issues := f.issueRoot("Bugs")
	f.add(issues, "Crash on save")
	f.issueRoot("Empty issue tree")
	f.add("", "Solo task")

	r := f.report(daily(1))
	wantSample(t, "day 0", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2})
	if r.Plans.Open != 1 {
		t.Errorf("Plans.Open = %d, want 1 — issue roots are not plans", r.Plans.Open)
	}
}

// A leaf is blocked while a blocker is open, and unblocked when it closes.
func TestReport_BlockedThenUnblocked(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	f.block(a, b)
	f.at(day(1, time.Hour)).done(b)

	r := f.report(daily(2))
	wantSample(t, "blocked", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2, Blocked: 1})
	wantSample(t, "unblocked", sampleEnding(t, r, day(2)), Sample{Scope: 2, Done: 1, Open: 1})
	if r.Leaves.Blocked != 0 {
		t.Errorf("Leaves.Blocked = %d, want 0 at Until", r.Leaves.Blocked)
	}
}

// A purged task is excluded entirely — even before the purge.
func TestReport_PurgedTaskNeverCounts(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	f.add(root, "Keep")
	gone := f.add(root, "Gone")
	f.at(day(1, time.Hour)).purge(gone)

	r := f.report(daily(2))
	wantSample(t, "before purge", sampleEnding(t, r, day(1)), Sample{Scope: 1, Open: 1})
	wantSample(t, "after purge", sampleEnding(t, r, day(2)), Sample{Scope: 1, Open: 1})
}

// Plans are non-issue roots; PlansDone is the done roots at each sample.
func TestReport_PlansDonePerSample(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	alpha := f.add("", "Alpha")
	leaf := f.add(alpha, "only leaf")
	f.add("", "Beta")
	f.at(day(1, time.Hour)).done(leaf) // auto-closes Alpha

	r := f.report(daily(2))
	wantSample(t, "before", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2})
	wantSample(t, "after", sampleEnding(t, r, day(2)), Sample{Scope: 2, Done: 1, Open: 1, PlansDone: 1})
	if r.Plans.Closed != 1 || r.Plans.Open != 1 {
		t.Errorf("Plans = %+v, want 1 closed and 1 open", r.Plans)
	}
}

// A scope query covers that subtree only, and the scope task is its only plan.
func TestReport_ScopeCoversSubtree(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	r1 := f.add("", "One")
	a := f.add(r1, "A")
	f.add(r1, "B")
	r2 := f.add("", "Two")
	f.add(r2, "C")
	f.at(day(1, time.Hour)).done(a)

	q := daily(2)
	q.Scope = r1
	r := f.report(q)
	wantSample(t, "scoped", sampleEnding(t, r, day(2)), Sample{Scope: 2, Done: 1, Open: 1})
	if r.Plans.Open != 1 {
		t.Errorf("Plans.Open = %d, want 1 — the scope task is the only plan", r.Plans.Open)
	}
	if r.Window.Scope != r1 {
		t.Errorf("Window.Scope = %q, want %q", r.Window.Scope, r1)
	}
	var created int
	for _, a := range r.Activity {
		created += a.Created
	}
	if created != 2 {
		t.Errorf("activity created = %d, want 2 (A, B; One is a parent)", created)
	}
}

// Subtree membership is decided as of Until: a task moved in later counts
// for its whole history.
func TestReport_ScopeMembershipIsAsOfUntil(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	r1 := f.add("", "One")
	f.add(r1, "A")
	r2 := f.add("", "Two")
	f.add(r2, "keeps Two a parent")
	c := f.add(r2, "C")
	f.at(day(1, time.Hour)).reparent(c, r1)

	q := daily(2)
	q.Scope = r1
	r := f.report(q)
	wantSample(t, "before the move", sampleEnding(t, r, day(1)), Sample{Scope: 2, Open: 2})
	wantSample(t, "after the move", sampleEnding(t, r, day(2)), Sample{Scope: 2, Open: 2})
}

func TestReport_UnknownScopeIsAnError(t *testing.T) {
	f := newReportFixture(t)
	q := daily(1)
	q.Scope = "nope9"
	if _, err := BuildReport(f.db, q); err == nil {
		t.Fatal("BuildReport with an unknown scope: want an error")
	}
}

// Activity counts, per bucket, leaves created and closed there and claim and
// block events on leaves; the plan root is a parent and never counts.
func TestReport_ActivityPerBucket(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	f.at(day(1, time.Hour))
	f.block(b, a)
	f.claim(a)
	f.at(day(1, 2*time.Hour)).done(a)

	r := f.report(daily(2))
	if len(r.Activity) != 2 {
		t.Fatalf("activity buckets = %d, want 2", len(r.Activity))
	}
	a0, a1 := r.Activity[0], r.Activity[1]
	if !a0.Start.Equal(day(0)) || !a0.End.Equal(day(1)) || !a1.End.Equal(day(2)) {
		t.Errorf("activity bounds = [%s,%s) [%s,%s)", a0.Start, a0.End, a1.Start, a1.End)
	}
	if a0.Created != 2 || a0.Claimed+a0.Done+a0.Blocked != 0 {
		t.Errorf("day 0 activity = %+v, want 2 created", a0)
	}
	if a1.Created != 0 || a1.Claimed != 1 || a1.Done != 1 || a1.Blocked != 1 {
		t.Errorf("day 1 activity = %+v, want 1 claimed, 1 done, 1 blocked", a1)
	}
}

func TestReport_EmptyStore(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(3))
	r, err := BuildReport(f.db, ReportQuery{Location: time.UTC})
	if err != nil {
		t.Fatalf("BuildReport on an empty store: %v", err)
	}
	if r.Schema != ReportSchema {
		t.Errorf("Schema = %d, want %d", r.Schema, ReportSchema)
	}
	if !r.Window.Until.Equal(day(3)) || !r.Window.Since.Equal(day(3)) {
		t.Errorf("window = %s..%s, want both at now (%s)", r.Window.Since, r.Window.Until, day(3))
	}
	if len(r.Series) != 1 || r.Series[0] != (Sample{End: day(3)}) {
		t.Errorf("series = %+v, want one empty sample at Until", r.Series)
	}
	if len(r.Activity) != 1 {
		t.Errorf("activity = %+v, want one bucket aligned with the series", r.Activity)
	}
	if r.DoneByActor == nil || r.Imports == nil {
		t.Errorf("DoneByActor and Imports should be empty, not nil, so JSON carries []")
	}
	if r.Pace.MedianClaimedToDoneSeconds != nil || r.Pace.MedianCreatedToDoneSeconds != nil ||
		r.Plans.MedianImportToCloseSeconds != nil {
		t.Errorf("medians should be nil on an empty store: %+v %+v", r.Pace, r.Plans)
	}
}
