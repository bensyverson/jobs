package chart

import (
	"math"
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

// interior is a's labelled ticks between the two edge labels.
func interior(a Axis) []Tick {
	if len(a.Ticks) < 2 {
		return nil
	}
	return a.Ticks[1 : len(a.Ticks)-1]
}

var axisUntil = time.Date(2026, 9, 26, 17, 23, 0, 0, time.UTC)

// Every range's span, as a live window ending at axisUntil.
var rangeSpans = map[string]time.Duration{
	"1H":  time.Hour,
	"1D":  24 * time.Hour,
	"7D":  7 * 24 * time.Hour,
	"14D": 14 * 24 * time.Hour,
	"30D": 30 * 24 * time.Hour,
}

// Decision 7: unlabelled minor marks every 5 minutes on 1H, every hour
// on 1D, every day on 7D and 14D, every week on 30D — so the gap
// between any two adjacent marks is that unit's share of the window.
func TestLayoutAxis_MinorTicksPerRange(t *testing.T) {
	cases := []struct {
		key  string
		unit time.Duration
	}{
		{"1H", 5 * time.Minute},
		{"1D", time.Hour},
		{"7D", 24 * time.Hour},
		{"14D", 24 * time.Hour},
		{"30D", 7 * 24 * time.Hour},
	}
	for _, c := range cases {
		span := rangeSpans[c.key]
		a := LayoutAxis(windowReport(axisUntil.Add(-span), axisUntil), time.UTC, EndsNow)
		wantN := int(span / c.unit)
		if n := len(a.Minor); n < wantN-1 || n > wantN+1 {
			t.Errorf("%s: %d minor ticks, want %d±1", c.key, n, wantN)
		}
		step := float64(c.unit) / float64(span) * 100
		for i := 1; i < len(a.Minor); i++ {
			if d := pct(a.Minor[i]) - pct(a.Minor[i-1]); math.Abs(d-step) > 0.02 {
				t.Errorf("%s: minor ticks %s → %s are %.3f%% apart, want %.3f%%", c.key, a.Minor[i-1], a.Minor[i], d, step)
				break
			}
		}
	}
}

// All picks its minor unit by span with the same rule: a young store
// ticks hourly, a five-month one monthly.
func TestLayoutAxis_MinorTicksOnAllFollowTheSpan(t *testing.T) {
	young := LayoutAxis(windowReport(axisUntil.Add(-4*time.Hour-50*time.Minute), axisUntil), time.UTC, EndsNow)
	if n := len(young.Minor); n != 5 {
		t.Errorf("4h50m window: %d minor ticks, want 5 hourly marks", n)
	}
	old := LayoutAxis(windowReport(time.Date(2026, 4, 20, 9, 30, 0, 0, time.UTC), axisUntil), time.UTC, EndsNow)
	if n := len(old.Minor); n != 5 {
		t.Errorf("five-month window: %d minor ticks, want 5 monthly marks (May–Sep)", n)
	}
}

// Minor marks sit on calendar boundaries in the report's calendar:
// 1D's hourly marks land on the hour.
func TestLayoutAxis_MinorTicksSitOnBoundaries(t *testing.T) {
	a := LayoutAxis(windowReport(axisUntil.Add(-24*time.Hour), axisUntil), time.UTC, EndsNow)
	// The first hour boundary after 17:23 is 18:00, 37 minutes in.
	if want := fmtPct(37.0 / (24 * 60) * 100); len(a.Minor) == 0 || a.Minor[0] != want {
		t.Errorf("first minor tick = %v, want %s", a.Minor, want)
	}
}

// The left edge always names the window's start moment; the right
// edge reads "Now" on a live window.
func TestLayoutAxis_EdgesAreLabelled(t *testing.T) {
	for key, span := range rangeSpans {
		a := LayoutAxis(windowReport(axisUntil.Add(-span), axisUntil), time.UTC, EndsNow)
		if len(a.Ticks) < 2 {
			t.Fatalf("%s: ticks = %q, want at least the two edges", key, tickLabels(a))
		}
		first, last := a.Ticks[0], a.Ticks[len(a.Ticks)-1]
		if first.X != "0%" || first.Anchor != AnchorStart {
			t.Errorf("%s: first tick %+v, want the start edge at 0%% anchored start", key, first)
		}
		if last.X != "100%" || last.Anchor != AnchorEnd || last.Label != "Now" {
			t.Errorf("%s: last tick %+v, want Now at 100%% anchored end", key, last)
		}
	}
}

// The start label reads at the scale's grain: a clock time inside a
// day, a date beyond.
func TestLayoutAxis_StartLabelNamesTheMoment(t *testing.T) {
	cases := map[string]string{"1H": "16:23", "1D": "17:23", "7D": "Sep 19", "30D": "Aug 27"}
	for key, want := range cases {
		a := LayoutAxis(windowReport(axisUntil.Add(-rangeSpans[key]), axisUntil), time.UTC, EndsNow)
		if got := a.Ticks[0].Label; got != want {
			t.Errorf("%s: start label %q, want %q", key, got, want)
		}
	}
}

// Parked under the scrubber the right edge reads the cursor's moment,
// at the same grain as the start.
func TestLayoutAxis_CursorEndNamesTheCursor(t *testing.T) {
	cases := map[string]string{"1H": "17:23", "1D": "17:23", "7D": "Sep 26", "30D": "Sep 26"}
	for key, want := range cases {
		a := LayoutAxis(windowReport(axisUntil.Add(-rangeSpans[key]), axisUntil), time.UTC, EndsAtCursor)
		if got := a.Ticks[len(a.Ticks)-1].Label; got != want {
			t.Errorf("%s: end label %q, want %q", key, got, want)
		}
	}
}

// A long history names years at its edges, so "Apr" is never ambiguous.
func TestLayoutAxis_MultiYearEdgesNameTheYear(t *testing.T) {
	a := LayoutAxis(windowReport(axisUntil.Add(-800*24*time.Hour), axisUntil), time.UTC, EndsAtCursor)
	if first, last := a.Ticks[0].Label, a.Ticks[len(a.Ticks)-1].Label; first != "Jul 2024" || last != "Sep 2026" {
		t.Errorf("800-day edges = %q … %q, want Jul 2024 … Sep 2026", first, last)
	}
}

// Between the edges, a few labels on calendar boundaries: 1D names the
// midnight it crosses as the day, never "00:00". (Here midnight sits
// mid-window, clear of both edge labels.)
func TestLayoutAxis_MidnightTickNamesTheDay(t *testing.T) {
	until := time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC)
	a := LayoutAxis(windowReport(until.Add(-24*time.Hour), until), time.UTC, EndsNow)
	if !strings.Contains(tickLabels(a), "Sep 26") {
		t.Errorf("1d ticks = %q, want the midnight tick to read Sep 26", tickLabels(a))
	}
	if strings.Contains(tickLabels(a), "00:00") {
		t.Errorf("1d ticks = %q, want no bare 00:00", tickLabels(a))
	}
}

// Every range carries at least one interior label, and never more than
// maxTicks: the edges alone do not make a scale.
func TestLayoutAxis_InteriorLabelsAtEveryRange(t *testing.T) {
	for key, span := range rangeSpans {
		a := LayoutAxis(windowReport(axisUntil.Add(-span), axisUntil), time.UTC, EndsNow)
		if n := len(interior(a)); n < 1 || n > maxTicks {
			t.Errorf("%s: %d interior labels (%s), want 1..%d", key, n, tickLabels(a), maxTicks)
		}
		for _, tk := range interior(a) {
			if tk.Anchor != AnchorMiddle {
				t.Errorf("%s: interior label %q anchors %q, want middle", key, tk.Label, tk.Anchor)
			}
		}
	}
}

// Labels are drawn in the report's calendar, not UTC.
func TestLayoutAxis_UsesTheGivenLocation(t *testing.T) {
	tokyo := time.FixedZone("JST", 9*3600)
	until := time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC) // 17:00 JST
	a := LayoutAxis(windowReport(until.Add(-time.Hour), until), tokyo, EndsNow)
	if a.Ticks[0].Label != "16:00" {
		t.Errorf("ticks = %q, want JST clock times", tickLabels(a))
	}
}

// axisFloorPx and axisFloorCharPx mirror the facts documented on
// axisPlotFloorPx and axisCharPx in axis.go (how they were measured is
// there). labelBoxPx is independent of axis.go's own collision math —
// it re-derives the same physical fact from the tick's public fields,
// so it stays a real check rather than asserting the production
// function agrees with itself.
const (
	axisFloorPx     = 218.0
	axisFloorCharPx = 6.803
)

func labelBoxPx(tk Tick) (lo, hi float64) {
	x := pct(tk.X) / 100 * axisFloorPx
	w := float64(len([]rune(tk.Label))) * axisFloorCharPx
	switch tk.Anchor {
	case AnchorStart:
		return x, x + w
	case AnchorEnd:
		return x - w, x
	default:
		return x - w/2, x + w/2
	}
}

func assertNoCollisions(t *testing.T, label string, ticks []Tick) {
	t.Helper()
	for i := 1; i < len(ticks); i++ {
		_, prevHi := labelBoxPx(ticks[i-1])
		lo, _ := labelBoxPx(ticks[i])
		if lo < prevHi {
			t.Errorf("%s: ticks %q and %q collide at the floor width (%.1fpx > %.1fpx)",
				label, ticks[i-1].Label, ticks[i].Label, prevHi, lo)
		}
	}
	for _, tk := range ticks {
		if lo, hi := labelBoxPx(tk); lo < 0 || hi > axisFloorPx {
			t.Errorf("%s: tick %q spans [%.1f, %.1f]px, outside the plot", label, tk.Label, lo, hi)
		}
	}
}

// Whatever span the axis lays out, live or parked, no two labels —
// edges included — collide at the narrowest plot the panel renders
// (found at 390px on the one-sample preview in the first cut, where an
// interior tick ran into the one at the edge).
func TestLayoutAxis_LabelsNeverCollideAtTheFloorWidth(t *testing.T) {
	for _, end := range []WindowEnd{EndsNow, EndsAtCursor} {
		for _, span := range []time.Duration{
			5 * time.Minute, time.Hour, 4*time.Hour + 50*time.Minute, 24 * time.Hour,
			7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour,
			150 * 24 * time.Hour, 800 * 24 * time.Hour,
		} {
			a := LayoutAxis(windowReport(axisUntil.Add(-span), axisUntil), time.UTC, end)
			assertNoCollisions(t, string(end)+" "+span.String(), a.Ticks)
		}
	}
}

func TestLayoutAxis_ImportMarkersSitOnTheScale(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	rep := windowReport(since, since.Add(100*time.Hour))
	rep.Imports = []job.ImportMarker{
		{At: since.Add(25 * time.Hour), TaskID: "abc12", Title: "Reporting", Source: "reporting.md"},
		{At: since.Add(-time.Hour), TaskID: "old01", Title: "Before the window"},
	}
	a := LayoutAxis(rep, time.UTC, EndsNow)
	if len(a.Imports) != 1 {
		t.Fatalf("imports = %d, want 1 (the one before the window is dropped)", len(a.Imports))
	}
	if a.Imports[0].X != "25%" {
		t.Errorf("import X = %q, want 25%%", a.Imports[0].X)
	}
	if a.Imports[0].Label != "Imported Reporting from reporting.md" {
		t.Errorf("import label = %q, want the title and source", a.Imports[0].Label)
	}
}

// Decision 8: an import tick links to the imported task's page, which
// the peek sheet opens in place.
func TestLayoutAxis_ImportMarkersLinkToTheirTask(t *testing.T) {
	since := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	rep := windowReport(since, since.Add(100*time.Hour))
	rep.Imports = []job.ImportMarker{{At: since.Add(25 * time.Hour), TaskID: "abc12", Title: "Reporting"}}
	a := LayoutAxis(rep, time.UTC, EndsNow)
	if a.Imports[0].Href != "/tasks/abc12" {
		t.Errorf("import href = %q, want /tasks/abc12", a.Imports[0].Href)
	}
	if a.Imports[0].Label != "Imported Reporting" {
		t.Errorf("import label = %q, want no source clause when there is none", a.Imports[0].Label)
	}
}
