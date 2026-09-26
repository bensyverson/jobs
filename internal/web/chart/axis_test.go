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
