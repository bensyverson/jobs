package job

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// chartLines returns the burn-up block of a text render: the legend
// line and everything after it.
func chartLines(t *testing.T, out string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "Burn-up") {
			return lines[i:]
		}
	}
	t.Fatalf("no Burn-up block:\n%s", out)
	return nil
}

func TestBurnUp_LegendNamesEveryGlyph(t *testing.T) {
	legend := chartLines(t, renderText(t, sampleReport(), 80))[0]
	mustContain(t, legend, glyphDone+" done", glyphBlocked+" blocked", glyphOpen+" open")
}

func TestBurnUp_DoneAndOpenDistinguishableWithoutColour(t *testing.T) {
	out := strings.Join(chartLines(t, renderText(t, sampleReport(), 80))[1:], "\n")
	for _, g := range []string{glyphDone, glyphOpen} {
		if !strings.Contains(out, g) {
			t.Errorf("plot missing %q:\n%s", g, out)
		}
	}
	if glyphDone == glyphOpen || glyphDone == glyphBlocked || glyphOpen == glyphBlocked {
		t.Fatal("glyphs must differ")
	}
}

func TestBurnUp_BlockedShareDrawn(t *testing.T) {
	r := sampleReport()
	for i := range r.Series {
		r.Series[i].Blocked = r.Series[i].Open / 2
	}
	plot := strings.Join(chartLines(t, renderText(t, r, 80))[1:], "\n")
	if !strings.Contains(plot, glyphBlocked) {
		t.Errorf("a large blocked share should draw %q:\n%s", glyphBlocked, plot)
	}
}

func TestBurnUp_YAxisLabelsMaxAndZero(t *testing.T) {
	lines := chartLines(t, renderText(t, sampleReport(), 80))
	if !strings.HasPrefix(strings.TrimSpace(lines[1]), "1,120") {
		t.Errorf("top row should carry the max scope 1,120: %q", lines[1])
	}
	found := false
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "0 ") {
			found = true
		}
	}
	if !found {
		t.Errorf("no zero baseline:\n%s", strings.Join(lines, "\n"))
	}
}

func TestBurnUp_XAxisLabelsFirstAndLastBucket(t *testing.T) {
	lines := chartLines(t, renderText(t, sampleReport(), 80))
	last := lines[len(lines)-1]
	mustContain(t, last, "Sep 7", "Sep 28")
}

func TestBurnUp_FitsTheWidth(t *testing.T) {
	for _, width := range []int{24, 40, 80, 132} {
		for _, l := range chartLines(t, renderText(t, sampleReport(), width)) {
			if n := utf8.RuneCountInString(l); n > width {
				t.Errorf("width %d: line is %d runes: %q", width, n, l)
			}
		}
	}
}

func TestBurnUp_NarrowerThanMinimumClampsToMinimum(t *testing.T) {
	for _, l := range chartLines(t, renderText(t, sampleReport(), 5)) {
		if n := utf8.RuneCountInString(l); n > minChartWidth {
			t.Errorf("line is %d runes, want ≤ %d: %q", n, minChartWidth, l)
		}
	}
}

func TestBurnUp_ZeroWidthFallsBackToDefault(t *testing.T) {
	long := 0
	for _, l := range chartLines(t, renderText(t, longReport(200), 0)) {
		long = max(long, utf8.RuneCountInString(l))
	}
	if long != DefaultReportWidth {
		t.Errorf("widest chart line = %d, want the default %d", long, DefaultReportWidth)
	}
}

// longReport is n daily samples with scope and done both climbing, the
// last sample the largest.
func longReport(n int) Report {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := Report{Schema: ReportSchema, Window: ReportWindow{Since: start, Until: start.AddDate(0, 0, n), Bucket: BucketDay, Timezone: "UTC"}}
	for i := range n {
		r.Series = append(r.Series, Sample{End: start.AddDate(0, 0, i+1), Scope: 10 + i, Done: i / 2, Open: 10 + i - i/2})
	}
	return r
}

func TestBurnUp_DownsamplesKeepingTheLastSample(t *testing.T) {
	lines := chartLines(t, renderText(t, longReport(200), 60))
	top := lines[1]
	if !strings.HasSuffix(top, glyphOpen) {
		t.Errorf("the rightmost column is the last (tallest) sample and should reach the top row: %q", top)
	}
	if n := utf8.RuneCountInString(top); n > 60 {
		t.Errorf("downsampled row is %d runes, want ≤ 60", n)
	}
	mustContain(t, lines[len(lines)-1], "Jul 20")
}

func TestBurnUp_ShortSeriesWidensColumns(t *testing.T) {
	lines := chartLines(t, renderText(t, sampleReport(), 80))
	bottom := lines[len(lines)-3] // lowest plot row, above the axis and the dates
	if n := strings.Count(bottom, glyphDone); n <= len(sampleReport().Series) {
		t.Errorf("four samples at width 80 should draw wider than one cell each; got %d done cells in %q", n, bottom)
	}
}

func TestBurnUp_NothingInScope(t *testing.T) {
	r := sampleReport()
	for i := range r.Series {
		r.Series[i] = Sample{End: r.Series[i].End}
	}
	out := renderText(t, r, 80)
	mustContain(t, out, "Burn-up  nothing in scope in this window")
	if strings.Contains(out, glyphDone) {
		t.Errorf("an all-zero series should draw no plot:\n%s", out)
	}
}
