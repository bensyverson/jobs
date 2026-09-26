package job

import (
	"slices"
	"testing"
	"time"
)

const nestedPlan = "```yaml\n" +
	"tasks:\n" +
	"  - title: Ship it\n" +
	"    children:\n" +
	"      - title: Build\n" +
	"      - title: Test\n" +
	"```\n"

// An import nested under --parent records its event on the imported task, not
// the parent: that task is what closes, so import→close is measured on it.
func TestReport_NestedImportFigures(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	parent := f.add("", "Program")
	f.add(parent, "Existing leaf")
	f.at(day(1, time.Hour))
	ids := f.importPlan("ship.md", parent, nestedPlan)
	ship, build, test := ids[0], ids[1], ids[2]
	f.at(day(2, time.Hour)).done(build)
	f.at(day(3, 7*time.Hour)).done(test) // auto-closes Ship it

	r := f.report(daily(4))
	if r.Plans.Imported != 1 {
		t.Errorf("Plans.Imported = %d, want 1", r.Plans.Imported)
	}
	want := []ImportMarker{{At: day(1, time.Hour), TaskID: ship, Title: "Ship it", Source: "ship.md"}}
	if len(r.Imports) != 1 || !r.Imports[0].At.Truncate(time.Second).Equal(want[0].At) || r.Imports[0].TaskID != ship ||
		r.Imports[0].Title != want[0].Title || r.Imports[0].Source != want[0].Source {
		t.Errorf("Imports = %+v, want %+v", r.Imports, want)
	}
	wantMedian := int64((2*24 + 6) * 3600)
	if got := r.Plans.MedianImportToCloseSeconds; got == nil || *got != wantMedian {
		t.Errorf("MedianImportToCloseSeconds = %s, want %s", int64PtrString(got), int64PtrString(&wantMedian))
	}
	// Program is the only plan and is still open; Ship it is not a root.
	if r.Plans.Closed != 0 || r.Plans.Open != 1 {
		t.Errorf("Plans = %+v, want 0 closed, 1 open", r.Plans)
	}
	wantSample(t, "Until", sampleEnding(t, r, day(4)), Sample{Scope: 3, Done: 2, Open: 1})
}

// An imported task closed outside the window, or a window with no imports,
// leaves the median nil rather than zero.
func TestReport_ImportMedianNilOutsideWindow(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	ids := f.importPlan("p.md", "", nestedPlan)
	f.at(day(0, 2*time.Hour)).done(ids[1])
	f.at(day(0, 3*time.Hour)).done(ids[2])

	q := ReportQuery{Since: day(1), Until: day(2), Bucket: BucketDay}
	r := f.report(q)
	if r.Plans.MedianImportToCloseSeconds != nil || r.Plans.Imported != 0 || r.Plans.Closed != 0 {
		t.Errorf("Plans = %+v, want nothing in a window after the close", r.Plans)
	}
	if len(r.Imports) != 0 {
		t.Errorf("Imports = %+v, want none in the window", r.Imports)
	}
	// The store has recorded imports, just none in this window.
	if at := r.Plans.FirstImportAt; at == nil || !at.Truncate(time.Second).Equal(day(0, time.Hour)) {
		t.Errorf("FirstImportAt = %v, want the import before the window at %s", at, day(0, time.Hour))
	}
	wantSample(t, "state still carries", sampleEnding(t, r, day(2)), Sample{Scope: 2, Done: 2, PlansDone: 1})
}

// Pace and done-by-actor are over leaves closed in the window, each counted
// once; claim→done measures from the last claim before the close.
func TestReport_PaceAndDoneByActor(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	c := f.add(root, "C")
	f.add(root, "D stays open")

	f.at(day(1)).claimAs(a, "alice")
	f.at(day(1, time.Hour)).releaseAs(a, "alice")
	f.at(day(2)).claimAs(a, "alice")
	f.at(day(2, 2*time.Hour)).doneAs(a, "alice") // created→done 50h, claim→done 2h
	f.at(day(3)).doneAs(b, "bob")                // created→done 72h, never claimed
	f.at(day(4)).doneAs(c, "alice")              // created→done 96h, never claimed

	r := f.report(daily(7))
	if r.Leaves.Done != 3 || r.Leaves.Open != 1 || r.Leaves.Created != 4 {
		t.Errorf("Leaves = %+v, want 4 created, 3 done, 1 open", r.Leaves)
	}
	if r.Pace.DonePerWeek != 3 {
		t.Errorf("DonePerWeek = %v, want 3 over a 7-day window", r.Pace.DonePerWeek)
	}
	if got, want := r.Pace.MedianCreatedToDoneSeconds, new(int64(72*3600)); got == nil || *got != *want {
		t.Errorf("MedianCreatedToDone = %s, want %s", int64PtrString(got), int64PtrString(want))
	}
	if got, want := r.Pace.MedianClaimedToDoneSeconds, new(int64(2*3600)); got == nil || *got != *want {
		t.Errorf("MedianClaimedToDone = %s, want %s — the last claim before the close", int64PtrString(got), int64PtrString(want))
	}
	wantActors := []ActorCount{{Actor: "alice", Done: 2}, {Actor: "bob", Done: 1}}
	if !slices.Equal(r.DoneByActor, wantActors) {
		t.Errorf("DoneByActor = %+v, want %+v", r.DoneByActor, wantActors)
	}
}

// An even count's median is the mean of the middle two.
func TestReport_MedianOfEvenCount(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	f.add(root, "open")
	f.at(day(1)).done(a)
	f.at(day(2)).done(b)

	r := f.report(daily(3))
	if got, want := r.Pace.MedianCreatedToDoneSeconds, new(int64(36*3600)); got == nil || *got != *want {
		t.Errorf("MedianCreatedToDone = %s, want %s", int64PtrString(got), int64PtrString(want))
	}
}

// Medians are nil when no leaf in the window qualifies.
func TestReport_MediansNilWhenNothingQualifies(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	f.add(root, "open")
	b := f.add(root, "closed unclaimed")
	f.at(day(1)).done(b)

	r := f.report(daily(2))
	if r.Pace.MedianCreatedToDoneSeconds == nil {
		t.Error("MedianCreatedToDone is nil, want set — one leaf closed")
	}
	if r.Pace.MedianClaimedToDoneSeconds != nil {
		t.Errorf("MedianClaimedToDone = %d, want nil — no closed leaf was claimed", *r.Pace.MedianClaimedToDoneSeconds)
	}
	if r.Plans.MedianImportToCloseSeconds != nil {
		t.Error("MedianImportToClose should be nil with no imports")
	}
	if r.Plans.FirstImportAt != nil {
		t.Errorf("FirstImportAt = %v, want nil — no import was ever recorded", r.Plans.FirstImportAt)
	}

	r = f.report(ReportQuery{Since: day(1, time.Hour), Until: day(2), Bucket: BucketDay})
	if r.Pace.MedianCreatedToDoneSeconds != nil || r.Leaves.Done != 0 || r.Pace.DonePerWeek != 0 {
		t.Errorf("window after the close: Pace = %+v, Leaves = %+v, want nothing closed", r.Pace, r.Leaves)
	}
}
