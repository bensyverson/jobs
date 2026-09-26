package chart

import (
	"math"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

func activityReport() job.Report {
	rep := fourDays()
	rep.Activity = []job.ActivityCount{
		{Start: t0, End: t0.Add(24 * time.Hour), Created: 10, Claimed: 2},
		{Start: t0.Add(24 * time.Hour), End: t0.Add(48 * time.Hour)},
		{Start: t0.Add(48 * time.Hour), End: t0.Add(72 * time.Hour), Done: 3, Blocked: 1},
		{Start: t0.Add(72 * time.Hour), End: t0.Add(96 * time.Hour), Created: 1, Claimed: 1, Done: 1, Blocked: 1},
	}
	return rep
}

func TestLayoutActivity_EmptyWhenNoEvents(t *testing.T) {
	rep := fourDays()
	rep.Activity = []job.ActivityCount{{Start: t0, End: t0.Add(24 * time.Hour)}}
	if a := LayoutActivity(rep, time.UTC); !a.Empty {
		t.Fatalf("Empty = false for a window with no events")
	}
}

func TestLayoutActivity_TotalsEachKind(t *testing.T) {
	a := LayoutActivity(activityReport(), time.UTC)
	if a.Created != 11 || a.Claimed != 3 || a.Done != 4 || a.Blocked != 2 || a.Total != 20 {
		t.Errorf("totals = created %d claimed %d done %d blocked %d total %d",
			a.Created, a.Claimed, a.Done, a.Blocked, a.Total)
	}
}

// One bar per bucket with events; an empty bucket draws nothing.
func TestLayoutActivity_OneBarPerBusyBucket(t *testing.T) {
	a := LayoutActivity(activityReport(), time.UTC)
	if len(a.Bars) != 3 {
		t.Fatalf("bars = %d, want 3 (the empty bucket draws none)", len(a.Bars))
	}
}

// The busiest bucket reaches the top of the 100-unit viewBox; the
// others scale against it. Segments stack done, claimed, created,
// blocked from the baseline up.
func TestLayoutActivity_BarsScaleToTheBusiestBucket(t *testing.T) {
	a := LayoutActivity(activityReport(), time.UTC)
	first := a.Bars[0] // 12 events: the busiest
	if top := first.Segments[len(first.Segments)-1].Y; top != 0 {
		t.Errorf("busiest bar's top segment Y = %v, want 0", top)
	}
	last := a.Bars[2] // 4 events of four kinds
	if len(last.Segments) != 4 {
		t.Fatalf("last bar segments = %d, want 4", len(last.Segments))
	}
	wantOrder := []ActivityKind{KindDone, KindClaimed, KindCreated, KindBlocked}
	for i, s := range last.Segments {
		if s.Kind != wantOrder[i] {
			t.Errorf("segment %d kind = %q, want %q", i, s.Kind, wantOrder[i])
		}
	}
	if bottom := last.Segments[0]; math.Abs(bottom.Y+bottom.H-ActivityViewH) > 1e-9 {
		t.Errorf("bottom segment ends at %v, want the baseline %v", bottom.Y+bottom.H, ActivityViewH)
	}
}

// A bar spans its bucket on the same since→until scale as the burn-up,
// so the two charts line up in one column.
func TestLayoutActivity_BarsShareTheBurnupScale(t *testing.T) {
	a := LayoutActivity(activityReport(), time.UTC)
	b := a.Bars[1] // the bucket starting at +48h of a 96h window
	if b.X < 500 || b.X+b.W > 750 {
		t.Errorf("bar spans [%v, %v], want inside [500, 750]", b.X, b.X+b.W)
	}
}

func TestLayoutActivity_RowsTabulateEveryBucket(t *testing.T) {
	a := LayoutActivity(activityReport(), time.UTC)
	if len(a.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(a.Rows))
	}
	if r := a.Rows[0]; r.When != "2026-09-20 00:00" || r.Created != 10 || r.Claimed != 2 {
		t.Errorf("first row = %+v", r)
	}
}
