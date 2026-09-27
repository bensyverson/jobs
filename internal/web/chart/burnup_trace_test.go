package chart

import (
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// layoutTraced lays out rep with its Series standing in for the trace
// when the fixture has none: the geometry tests predate the trace and
// read the same whichever samples are drawn.
func layoutTraced(rep job.Report, loc *time.Location) Burnup {
	if len(rep.Trace) == 0 {
		rep.Trace = rep.Series
	}
	return LayoutBurnup(rep, loc)
}

// hourly is a window of n+1 trace samples an hour apart, the first at
// Since, from f; its Series is the two six-hour-ish bucket samples a
// report would carry beside it (first and last of the trace).
func hourly(n int, f func(i int) job.Sample) job.Report {
	rep := job.Report{
		Schema: job.ReportSchema,
		Window: job.ReportWindow{Since: t0, Until: t0.Add(time.Duration(n) * time.Hour), Bucket: job.BucketSixHours, Timezone: "UTC"},
	}
	for i := 0; i <= n; i++ {
		s := f(i)
		s.End = t0.Add(time.Duration(i) * time.Hour)
		s.Open = s.Scope - s.Done
		rep.Trace = append(rep.Trace, s)
	}
	rep.Series = []job.Sample{rep.Trace[n/2], rep.Trace[n]}
	return rep
}

func points(path string) int { return strings.Count(path, "M") + strings.Count(path, "L") }

// Decision 1: the lines are drawn from the fine trace, not the
// per-bucket series — one point per trace sample, starting at Since.
func TestLayoutBurnup_DrawsFromTheTrace(t *testing.T) {
	rep := hourly(6, func(i int) job.Sample { return job.Sample{Scope: 10 + i, Done: i} })
	b := LayoutBurnup(rep, time.UTC)
	if got := points(b.ScopePath); got != 7 {
		t.Errorf("ScopePath %q has %d points, want the trace's 7", b.ScopePath, got)
	}
	if got := points(b.DonePath); got != 7 {
		t.Errorf("DonePath %q has %d points, want 7", b.DonePath, got)
	}
	if !strings.HasPrefix(b.ScopePath, "M0 ") {
		t.Errorf("ScopePath starts %q, want the Since sample at x=0", b.ScopePath)
	}
}

// The data table stays per bucket: the trace's few hundred rows are
// not a readable table.
func TestLayoutBurnup_RowsStayPerBucket(t *testing.T) {
	rep := hourly(6, func(i int) job.Sample { return job.Sample{Scope: 10 + i, Done: i} })
	b := LayoutBurnup(rep, time.UTC)
	if len(b.Rows) != 2 {
		t.Fatalf("rows = %d, want the series' 2", len(b.Rows))
	}
}

// Decision 6: canceled work is a band stacked on the scope line,
// measured from zero at Since — leaves canceled before the window are
// not in it.
func TestLayoutBurnup_CanceledBandStartsAtZeroAtSince(t *testing.T) {
	rep := hourly(4, func(i int) job.Sample {
		return job.Sample{Scope: 20 - 2*i, Done: 5, Canceled: 7 + 2*i}
	})
	b := LayoutBurnup(rep, time.UTC)
	if b.CanceledPath == "" {
		t.Fatalf("no CanceledPath for a window that canceled 8 leaves")
	}
	// Forward along the band's top, back along scope.
	if want := "M0 " + fmtNum(b.y(20)); !strings.HasPrefix(b.CanceledPath, want) {
		t.Errorf("CanceledPath starts %q, want %q: zero height at Since", b.CanceledPath, want)
	}
	// At Until scope is 12 and 8 were canceled in the window: the top
	// is 20, the top of the domain.
	if !strings.Contains(b.CanceledPath, "L1000 0") {
		t.Errorf("CanceledPath %q does not reach scope+canceled (20) at the top", b.CanceledPath)
	}
	if !strings.Contains(b.CanceledPath, "L1000 "+fmtNum(b.y(12))) {
		t.Errorf("CanceledPath %q does not come back along scope (12) at Until", b.CanceledPath)
	}
}

// The y domain includes the band's top, so it is never clipped.
func TestLayoutBurnup_DomainIncludesTheCanceledBand(t *testing.T) {
	rep := hourly(4, func(i int) job.Sample {
		return job.Sample{Scope: 20, Done: 10, Canceled: 10 * i}
	})
	b := LayoutBurnup(rep, time.UTC)
	if b.dom.hi != 60 {
		t.Errorf("domain top = %v, want scope 20 + 40 canceled in the window = 60", b.dom.hi)
	}
}

// A window with no cancellations draws no band, however many leaves
// were canceled before it.
func TestLayoutBurnup_NoBandWithoutCancelingInTheWindow(t *testing.T) {
	rep := hourly(4, func(i int) job.Sample { return job.Sample{Scope: 20, Done: 2 * i, Canceled: 9} })
	b := LayoutBurnup(rep, time.UTC)
	if b.CanceledPath != "" {
		t.Errorf("CanceledPath = %q, want none", b.CanceledPath)
	}
	if b.dom.hi != 20 {
		t.Errorf("domain top = %v, want scope's 20", b.dom.hi)
	}
}

// A leaf canceled before Since and restored inside the window lowers
// the canceled count below the baseline; the band never goes negative.
func TestLayoutBurnup_CanceledBandNeverDipsBelowScope(t *testing.T) {
	rep := hourly(2, func(i int) job.Sample {
		return job.Sample{Scope: 20 + i, Done: 5, Canceled: 9 - i}
	})
	b := LayoutBurnup(rep, time.UTC)
	if b.CanceledPath != "" {
		t.Errorf("CanceledPath = %q, want none: nothing was canceled in the window", b.CanceledPath)
	}
}

// The "+N created" label sits beside the band's top when there is a
// band — created is scope's rise plus what was canceled — and beside
// scope otherwise.
func TestLayoutBurnup_CreatedLabelSitsBesideTheBandsTop(t *testing.T) {
	rep := hourly(4, func(i int) job.Sample {
		return job.Sample{Scope: 60, Done: 0, Canceled: 10 * i}
	})
	rep.Leaves.Created = 40
	b := LayoutBurnup(rep, time.UTC)
	// Top of the band is 100 → 0%; scope's 60 is 40%.
	if b.Created.Y != "6%" {
		t.Errorf("created label Y = %q, want at the band's top (0%%, kept inside at 6%%)", b.Created.Y)
	}
	// Its total is the value where it sits: "+40 created of 60" beside
	// a point at 100 would misread.
	if b.Created.Total != "of 100" {
		t.Errorf("created total = %q, want the band's top, of 100", b.Created.Total)
	}

	// No band: scope falls 64 → 60 over a 30..64 domain, ending 11.76%
	// down, well clear of done's label (40, at 70.6%).
	flat := hourly(4, func(i int) job.Sample { return job.Sample{Scope: 64 - i, Done: 30 + 10*i/4} })
	fb := LayoutBurnup(flat, time.UTC)
	if fb.Created.Y != fb.ScopeDot.Y {
		t.Errorf("created label Y = %q, want beside scope's end %q", fb.Created.Y, fb.ScopeDot.Y)
	}
}

// The <desc> says what the band is when there is one.
func TestLayoutBurnup_SummaryNamesTheBand(t *testing.T) {
	rep := hourly(4, func(i int) job.Sample { return job.Sample{Scope: 20, Done: 5, Canceled: 3 + i} })
	b := LayoutBurnup(rep, time.UTC)
	if want := "4 leaves canceled in this window are drawn as a band above scope."; !strings.Contains(b.Summary, want) {
		t.Errorf("Summary %q lacks %q", b.Summary, want)
	}
	quiet := LayoutBurnup(hourly(4, func(i int) job.Sample { return job.Sample{Scope: 20, Done: 5} }), time.UTC)
	if strings.Contains(quiet.Summary, "band") {
		t.Errorf("Summary %q mentions a band that is not drawn", quiet.Summary)
	}
}
