package chart

import (
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

var t0 = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)

// fourDays is a window of four daily samples: scope grows 10 → 40,
// done 0 → 30, with a blocked share on the way.
func fourDays() job.Report {
	return job.Report{
		Schema: job.ReportSchema,
		Window: job.ReportWindow{Since: t0, Until: t0.Add(96 * time.Hour), Bucket: job.BucketDay, Timezone: "UTC"},
		Series: []job.Sample{
			{End: t0.Add(24 * time.Hour), Scope: 10, Done: 0, Open: 10, Blocked: 2},
			{End: t0.Add(48 * time.Hour), Scope: 20, Done: 10, Open: 10, Blocked: 4},
			{End: t0.Add(72 * time.Hour), Scope: 30, Done: 20, Open: 10, Blocked: 0},
			{End: t0.Add(96 * time.Hour), Scope: 40, Done: 30, Open: 10, Blocked: 3, Canceled: 5},
		},
	}
}

func TestLayoutBurnup_EmptyWhenNoSamples(t *testing.T) {
	b := LayoutBurnup(job.Report{}, time.UTC)
	if !b.Empty {
		t.Fatalf("Empty = false for a report with no samples")
	}
}

func TestLayoutBurnup_EmptyWhenNothingEverExisted(t *testing.T) {
	rep := fourDays()
	for i := range rep.Series {
		rep.Series[i] = job.Sample{End: rep.Series[i].End}
	}
	if b := LayoutBurnup(rep, time.UTC); !b.Empty {
		t.Fatalf("Empty = false for a series of all-zero samples")
	}
}

// A store whose only leaves were canceled has zero scope but is not
// empty: the canceled count is the story.
func TestLayoutBurnup_NotEmptyWhenOnlyCanceled(t *testing.T) {
	rep := fourDays()
	for i := range rep.Series {
		rep.Series[i] = job.Sample{End: rep.Series[i].End, Canceled: 3}
	}
	if b := LayoutBurnup(rep, time.UTC); b.Empty {
		t.Fatalf("Empty = true for a series with canceled leaves")
	}
}

func TestLayoutBurnup_EndLabelsCarryTheLastSample(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	if b.Scope.Value != 40 || b.Done.Value != 30 {
		t.Fatalf("ends = scope %d done %d, want 40 and 30", b.Scope.Value, b.Done.Value)
	}
	if b.Open != 10 || b.Blocked != 3 || b.Canceled != 5 {
		t.Errorf("open/blocked/canceled = %d/%d/%d, want 10/3/5", b.Open, b.Blocked, b.Canceled)
	}
}

func TestLayoutBurnup_EndLabelsUseThousandsSeparators(t *testing.T) {
	rep := fourDays()
	rep.Series[3].Scope, rep.Series[3].Done = 1155, 1080
	b := LayoutBurnup(rep, time.UTC)
	if b.Scope.Text != "1,155" || b.Done.Text != "1,080" {
		t.Fatalf("end texts = %q / %q, want 1,155 / 1,080", b.Scope.Text, b.Done.Text)
	}
}

// Points sit at each sample's End on a since→until scale, in a
// 1000×1000 viewBox with y growing downward.
func TestLayoutBurnup_PathsPlotSamplesOnTheWindow(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	if !strings.HasPrefix(b.ScopePath, "M250 ") {
		t.Errorf("ScopePath starts %q, want the first sample at x=250", b.ScopePath)
	}
	if !strings.Contains(b.ScopePath, "L1000 ") {
		t.Errorf("ScopePath %q does not reach x=1000", b.ScopePath)
	}
	if strings.Count(b.DonePath, "L") != 3 {
		t.Errorf("DonePath %q: want 4 points (3 segments)", b.DonePath)
	}
	// The first done point is zero: on the baseline.
	if !strings.HasPrefix(b.DonePath, "M250 1000") {
		t.Errorf("DonePath starts %q, want M250 1000", b.DonePath)
	}
}

// The gap runs forward along scope and back along done, closed.
func TestLayoutBurnup_GapIsClosedBetweenTheLines(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	if !strings.HasPrefix(b.GapPath, "M250 ") || !strings.HasSuffix(b.GapPath, "Z") {
		t.Fatalf("GapPath = %q, want a closed path starting at the first sample", b.GapPath)
	}
	if strings.Count(b.GapPath, "L") != 7 {
		t.Errorf("GapPath %q: want 8 points (4 forward, 4 back)", b.GapPath)
	}
	if !strings.HasSuffix(b.BlockedPath, "Z") {
		t.Errorf("BlockedPath = %q, want a closed path", b.BlockedPath)
	}
}

// Blocked is drawn on top of done and never above scope, even when a
// sample's counts disagree.
func TestLayoutBurnup_BlockedBandIsClampedToScope(t *testing.T) {
	rep := job.Report{
		Window: job.ReportWindow{Since: t0, Until: t0.Add(2 * time.Hour)},
		Series: []job.Sample{
			{End: t0.Add(time.Hour), Scope: 10, Done: 8, Blocked: 9},
			{End: t0.Add(2 * time.Hour), Scope: 10, Done: 8, Blocked: 9},
		},
	}
	b := LayoutBurnup(rep, time.UTC)
	top := b.y(10)
	if strings.Contains(b.BlockedPath, " "+fmtNum(b.y(17))) {
		t.Errorf("BlockedPath %q rises to done+blocked=17, above scope", b.BlockedPath)
	}
	if !strings.Contains(b.BlockedPath, " "+fmtNum(top)) {
		t.Errorf("BlockedPath %q never reaches the scope line at y=%s", b.BlockedPath, fmtNum(top))
	}
}

// Gridlines are sparse (two to four), at round values, labelled with
// thousands separators, and never above the plot.
func TestLayoutBurnup_GridlinesAreSparseAndRound(t *testing.T) {
	rep := fourDays()
	rep.Series[3].Scope = 1155
	b := LayoutBurnup(rep, time.UTC)
	var labels []string
	for _, g := range b.Gridlines {
		labels = append(labels, g.Label)
	}
	if got := strings.Join(labels, ","); got != "500,1,000" {
		t.Errorf("gridline labels = %q, want 500,1,000", got)
	}
	for _, g := range b.Gridlines {
		if !strings.HasSuffix(g.Y, "%") {
			t.Errorf("gridline Y %q is not a percentage", g.Y)
		}
	}
}

func TestLayoutBurnup_SmallCountsGridOnWholeNumbers(t *testing.T) {
	rep := fourDays()
	for i := range rep.Series {
		rep.Series[i].Scope, rep.Series[i].Done, rep.Series[i].Blocked = 3, 1, 0
	}
	b := LayoutBurnup(rep, time.UTC)
	var labels []string
	for _, g := range b.Gridlines {
		labels = append(labels, g.Label)
	}
	if got := strings.Join(labels, ","); got != "1,2,3" {
		t.Errorf("gridline labels = %q, want 1,2,3", got)
	}
}

// When the two lines end close together their labels are pushed apart
// so the numbers never overlap.
func TestLayoutBurnup_EndLabelsKeepApart(t *testing.T) {
	rep := fourDays()
	rep.Series[3].Scope, rep.Series[3].Done = 40, 39
	b := LayoutBurnup(rep, time.UTC)
	gap := pct(b.Done.Y) - pct(b.Scope.Y)
	if gap < minEndLabelGapPct-0.01 {
		t.Errorf("end labels %s and %s are %.2f%% apart, want at least %v%%", b.Scope.Y, b.Done.Y, gap, minEndLabelGapPct)
	}
}

func TestLayoutBurnup_RowsTabulateEverySample(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	if len(b.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(b.Rows))
	}
	last := b.Rows[3]
	if last.When != "2026-09-24 00:00" || last.Scope != 40 || last.Done != 30 || last.Blocked != 3 || last.Canceled != 5 {
		t.Errorf("last row = %+v", last)
	}
}

func TestLayoutBurnup_SummaryStatesTheNumbers(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	for _, want := range []string{"40 in scope", "30 done", "10 open", "3 blocked", "5 canceled"} {
		if !strings.Contains(b.Summary, want) {
			t.Errorf("Summary %q lacks %q", b.Summary, want)
		}
	}
}

// A reopen dips the done line: the dip is drawn, not smoothed away.
func TestLayoutBurnup_ReopenDipIsDrawn(t *testing.T) {
	rep := fourDays()
	rep.Series[2].Done = 5
	b := LayoutBurnup(rep, time.UTC)
	if !strings.Contains(b.DonePath, " "+fmtNum(b.y(5))) {
		t.Errorf("DonePath %q lacks the dip to 5", b.DonePath)
	}
}

// A single sample still draws: a zero-length segment the round line
// caps render as a dot.
func TestLayoutBurnup_SingleSampleDrawsAPoint(t *testing.T) {
	rep := job.Report{
		Window: job.ReportWindow{Since: t0, Until: t0.Add(8 * time.Hour), Bucket: job.BucketDay},
		Series: []job.Sample{{End: t0.Add(8 * time.Hour), Scope: 4, Done: 1, Open: 3}},
	}
	b := LayoutBurnup(rep, time.UTC)
	if b.Empty || !strings.Contains(b.DonePath, "L") {
		t.Fatalf("single sample: Empty=%v DonePath=%q, want a drawable segment", b.Empty, b.DonePath)
	}
}

// Each line ends in a dot at its last sample, so a one-sample series
// (All over a young store) is still visible at a glance.
func TestLayoutBurnup_EndDotsMarkTheLastSample(t *testing.T) {
	b := LayoutBurnup(fourDays(), time.UTC)
	if b.ScopeDot.X != "100%" || b.DoneDot.X != "100%" {
		t.Errorf("dot X = %q / %q, want 100%%", b.ScopeDot.X, b.DoneDot.X)
	}
	if want := fmtPct(b.y(40) / BurnupViewH * 100); b.ScopeDot.Y != want {
		t.Errorf("scope dot Y = %q, want %q", b.ScopeDot.Y, want)
	}
	if want := fmtPct(b.y(30) / BurnupViewH * 100); b.DoneDot.Y != want {
		t.Errorf("done dot Y = %q, want %q", b.DoneDot.Y, want)
	}
}
