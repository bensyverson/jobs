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

// windowOf is a four-sample daily window with the given scope and done
// values.
func windowOf(scope, done [4]int) job.Report {
	rep := fourDays()
	for i := range rep.Series {
		rep.Series[i].Scope, rep.Series[i].Done = scope[i], done[i]
		rep.Series[i].Open, rep.Series[i].Blocked, rep.Series[i].Canceled = scope[i]-done[i], 0, 0
	}
	return rep
}

func gridLabels(b Burnup) string {
	var labels []string
	for _, g := range b.Gridlines {
		labels = append(labels, g.Label)
	}
	return strings.Join(labels, ",")
}

// The y domain is exactly the plotted values' [min, max], so a window
// that moved from 400 to 500 fills the full height rather than hugging
// the top of a zero-based axis.
func TestLayoutBurnup_DomainFitsThePlottedValues(t *testing.T) {
	b := LayoutBurnup(windowOf([4]int{400, 450, 480, 500}, [4]int{410, 430, 460, 490}), time.UTC)
	if !strings.HasPrefix(b.ScopePath, "M250 1000") {
		t.Errorf("ScopePath starts %q, want the minimum (400) on the baseline", b.ScopePath)
	}
	if !strings.HasSuffix(b.ScopePath, "L1000 0") {
		t.Errorf("ScopePath ends %q, want the maximum (500) at the top", b.ScopePath)
	}
	if b.ScopeDot.Y != "0%" {
		t.Errorf("scope dot Y = %q, want 0%%", b.ScopeDot.Y)
	}
}

// Done can be the minimum: the domain spans both lines.
func TestLayoutBurnup_DomainSpansDoneToo(t *testing.T) {
	b := LayoutBurnup(windowOf([4]int{400, 410, 420, 430}, [4]int{380, 390, 400, 410}), time.UTC)
	if !strings.HasPrefix(b.DonePath, "M250 1000") {
		t.Errorf("DonePath starts %q, want done's minimum (380) on the baseline", b.DonePath)
	}
	if !strings.HasSuffix(b.ScopePath, "L1000 0") {
		t.Errorf("ScopePath ends %q, want scope's maximum (430) at the top", b.ScopePath)
	}
}

// All is fitted like any other range: nothing pins the axis at zero.
func TestLayoutBurnup_DomainIsFittedOnAllToo(t *testing.T) {
	rep := windowOf([4]int{190, 250, 330, 402}, [4]int{190, 240, 320, 400})
	rep.Window.Bucket = job.BucketWeek
	b := LayoutBurnup(rep, time.UTC)
	if !strings.HasPrefix(b.DonePath, "M250 1000") {
		t.Errorf("DonePath starts %q, want 190 on the baseline", b.DonePath)
	}
}

// A flat window gets a small symmetric pad, so the line sits mid-chart
// rather than on an edge.
func TestLayoutBurnup_FlatWindowSitsMidChart(t *testing.T) {
	b := LayoutBurnup(windowOf([4]int{400, 400, 400, 400}, [4]int{400, 400, 400, 400}), time.UTC)
	if b.ScopePath != "M250 500L500 500L750 500L1000 500" {
		t.Errorf("ScopePath = %q, want a line at y=500", b.ScopePath)
	}
	if b.ScopeDot.Y != "50%" {
		t.Errorf("scope dot Y = %q, want 50%%", b.ScopeDot.Y)
	}
	if n := len(b.Gridlines); n < 2 || n > 4 {
		t.Errorf("flat window has %d gridlines (%s), want 2–4", n, gridLabels(b))
	}
}

// A flat window at zero (a store whose only leaves were canceled)
// never labels a negative gridline.
func TestLayoutBurnup_FlatZeroHasNoNegativeGridlines(t *testing.T) {
	rep := windowOf([4]int{0, 0, 0, 0}, [4]int{0, 0, 0, 0})
	for i := range rep.Series {
		rep.Series[i].Canceled = 3
	}
	b := LayoutBurnup(rep, time.UTC)
	if b.Empty || b.ScopeDot.Y != "50%" {
		t.Fatalf("Empty=%v scope dot Y=%q, want a drawn line mid-chart", b.Empty, b.ScopeDot.Y)
	}
	for _, g := range b.Gridlines {
		if strings.HasPrefix(g.Label, "-") {
			t.Errorf("gridline %q is negative", g.Label)
		}
	}
}

// Gridlines fall at round values strictly inside the fitted span, two
// to four of them.
func TestLayoutBurnup_GridlinesAreRoundInsideTheSpan(t *testing.T) {
	cases := []struct {
		scope, done [4]int
		want        string
	}{
		{[4]int{400, 450, 480, 500}, [4]int{400, 430, 460, 490}, "425,450,475"},
		{[4]int{392, 396, 400, 402}, [4]int{390, 394, 398, 400}, "395,400"},
		{[4]int{190, 250, 330, 402}, [4]int{190, 240, 320, 400}, "200,300,400"},
	}
	for _, c := range cases {
		if got := gridLabels(LayoutBurnup(windowOf(c.scope, c.done), time.UTC)); got != c.want {
			t.Errorf("scope %v done %v: gridlines %q, want %q", c.scope, c.done, got, c.want)
		}
	}
}

// Each gridline sits where its value plots.
func TestLayoutBurnup_GridlinesSitAtTheirValue(t *testing.T) {
	b := LayoutBurnup(windowOf([4]int{400, 450, 480, 500}, [4]int{400, 430, 460, 490}), time.UTC)
	if len(b.Gridlines) == 0 || b.Gridlines[1].Label != "450" || b.Gridlines[1].Y != "50%" {
		t.Errorf("gridlines = %+v, want 450 at 50%%", b.Gridlines)
	}
}

// A flat window's gridlines keep clear of its line, so no label is
// struck through by it (the 402 preview put "400" under the line).
func TestLayoutBurnup_FlatGridlinesClearTheLine(t *testing.T) {
	b := LayoutBurnup(windowOf([4]int{402, 402, 402, 402}, [4]int{402, 402, 402, 402}), time.UTC)
	for _, g := range b.Gridlines {
		if y := pct(g.Y); y > 40 && y < 60 {
			t.Errorf("gridline %s at %s crowds the line at 50%%", g.Label, g.Y)
		}
	}
	if n := len(b.Gridlines); n < 2 {
		t.Errorf("gridlines = %s, want at least 2", gridLabels(b))
	}
}
