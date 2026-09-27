package chart

import (
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

func windowReport(since, until time.Time) job.Report {
	return job.Report{Window: job.ReportWindow{Since: since, Until: until}}
}

func tickLabels(a Axis) string {
	var out []string
	for _, tk := range a.Ticks {
		out = append(out, tk.Label)
	}
	return strings.Join(out, ",")
}

// Only a few dates: never more than five ticks, never fewer than two
// on a window of any real length.
func TestLayoutAxis_FewTicksAtEverySpan(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 23, 0, 0, time.UTC)
	for _, span := range []time.Duration{
		time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour,
		30 * 24 * time.Hour, 150 * 24 * time.Hour, 800 * 24 * time.Hour,
	} {
		a := LayoutAxis(windowReport(until.Add(-span), until), time.UTC)
		if n := len(a.Ticks); n < 2 || n > maxTicks {
			t.Errorf("span %v: %d ticks (%s), want 2..%d", span, n, tickLabels(a), maxTicks)
		}
		for _, tk := range a.Ticks {
			if p := pct(tk.X); p < 0 || p > 100 {
				t.Errorf("span %v: tick %q at %s is outside the plot", span, tk.Label, tk.X)
			}
		}
	}
}

func TestLayoutAxis_HourWindowLabelsClockTimes(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 23, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-time.Hour), until), time.UTC)
	if got := tickLabels(a); got != "16:30,16:45,17:00,17:15" {
		t.Errorf("1h ticks = %q, want clock times on round minutes", got)
	}
}

// A tick on local midnight names the day instead of "00:00".
func TestLayoutAxis_MidnightTickNamesTheDay(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-24*time.Hour), until), time.UTC)
	if !strings.Contains(tickLabels(a), "Sep 26") {
		t.Errorf("1d ticks = %q, want the midnight tick to read Sep 26", tickLabels(a))
	}
	if strings.Contains(tickLabels(a), "00:00") {
		t.Errorf("1d ticks = %q, want no bare 00:00", tickLabels(a))
	}
}

func TestLayoutAxis_LongWindowsLabelDates(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-30*24*time.Hour), until), time.UTC)
	for _, tk := range a.Ticks {
		if _, err := time.Parse("Jan 2", tk.Label); err != nil {
			t.Errorf("30d tick %q is not a date", tk.Label)
		}
	}
}

// Labels are drawn in the report's calendar, not UTC.
func TestLayoutAxis_UsesTheGivenLocation(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*3600)
	until := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC) // 17:00 JST
	a := LayoutAxis(windowReport(until.Add(-time.Hour), until), tokyo)
	if !strings.Contains(tickLabels(a), "17:00") {
		t.Errorf("ticks = %q, want JST clock times", tickLabels(a))
	}
}

// Edge ticks anchor inward so their labels stay inside the plot.
func TestLayoutAxis_EdgeLabelsAnchorInward(t *testing.T) {
	until := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-24*time.Hour), until), time.UTC)
	last := a.Ticks[len(a.Ticks)-1]
	if pct(last.X) > 97 && last.Anchor != AnchorEnd {
		t.Errorf("tick at %s anchors %q, want end", last.X, last.Anchor)
	}
}

// axisPhonePlotPx and axisPhoneCharPx mirror the phone-width facts
// documented on axisPlotFloorPx in axis.go: measured with `sleepy
// query --selector ".c-burnup" --size 390x844` against the
// chart-panel preview's one-sample state, the plot is 236px wide at
// a 390px viewport (the panel's fixed chrome — page padding, the
// panel border and padding, the column gap and the end-label column
// — eats the rest), and --font-mono at --font-data-id-size advances
// 6.80px per character regardless of which characters (a monospace
// face, confirmed against both a 5-char clock label and a 3-char
// month label).
const (
	axisPhonePlotPx = 236.0
	axisPhoneCharPx = 6.803
)

// labelBoxPx is where tk's label sits along the plot at
// axisPhonePlotPx, reasoning the way SVG text-anchor lays glyphs out:
// start grows right from x, end grows left into x, middle straddles
// it. Independent of axis.go's own collision math — it re-derives
// the same physical fact from the tick's public X/Anchor/Label
// fields, so it stays a real check rather than asserting the
// production function agrees with itself.
func labelBoxPx(tk Tick) (lo, hi float64) {
	x := pct(tk.X) / 100 * axisPhonePlotPx
	w := float64(len([]rune(tk.Label))) * axisPhoneCharPx
	switch tk.Anchor {
	case AnchorStart:
		return x, x + w
	case AnchorEnd:
		return x - w, x
	default:
		return x - w/2, x + w/2
	}
}

func assertNoPhoneCollisions(t *testing.T, label string, ticks []Tick) {
	t.Helper()
	for i := 1; i < len(ticks); i++ {
		_, prevHi := labelBoxPx(ticks[i-1])
		lo, _ := labelBoxPx(ticks[i])
		if lo < prevHi {
			t.Errorf("%s: ticks %q and %q collide at phone width (%.1fpx > %.1fpx)",
				label, ticks[i-1].Label, ticks[i].Label, prevHi, lo)
		}
	}
}

// Reproduces the one-sample preview state (project/2026-09-26-reporting.md,
// found at 390px): a report barely five hours old picks an hourly
// clock unit whose last two ticks — one middle-anchored, one
// end-anchored right at the plot's edge — render on top of each
// other once the plot narrows to a phone's width, even though the
// same ticks sit comfortably apart on a desktop-width plot.
func TestLayoutAxis_LabelsNeverCollideAtPhoneWidth(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-4*time.Hour-50*time.Minute), until), time.UTC)
	assertNoPhoneCollisions(t, "one-sample span", a.Ticks)
}

// The same property, generalized: whatever span the axis lays out,
// its labels must never collide at phone width — not just the one
// span that happened to be reported.
func TestLayoutAxis_LabelsNeverCollideAtPhoneWidth_AcrossSpans(t *testing.T) {
	until := time.Date(2026, 9, 26, 17, 23, 0, 0, time.UTC)
	for _, span := range []time.Duration{
		5 * time.Minute, time.Hour, 4*time.Hour + 50*time.Minute, 24 * time.Hour,
		7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour,
		150 * 24 * time.Hour, 800 * 24 * time.Hour,
	} {
		a := LayoutAxis(windowReport(until.Add(-span), until), time.UTC)
		assertNoPhoneCollisions(t, span.String(), a.Ticks)
	}
}

func TestLayoutAxis_ImportMarkersSitOnTheScale(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	rep := windowReport(since, since.Add(100*time.Hour))
	rep.Imports = []job.ImportMarker{
		{At: since.Add(25 * time.Hour), TaskID: "abc12", Title: "Reporting", Source: "reporting.md"},
		{At: since.Add(-time.Hour), TaskID: "old01", Title: "Before the window"},
	}
	a := LayoutAxis(rep, time.UTC)
	if len(a.Imports) != 1 {
		t.Fatalf("imports = %d, want 1 (the one before the window is dropped)", len(a.Imports))
	}
	if a.Imports[0].X != "25%" {
		t.Errorf("import X = %q, want 25%%", a.Imports[0].X)
	}
	if !strings.Contains(a.Imports[0].Label, "Reporting") || !strings.Contains(a.Imports[0].Label, "reporting.md") {
		t.Errorf("import label = %q, want the title and source", a.Imports[0].Label)
	}
}
