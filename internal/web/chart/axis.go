package chart

import (
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// Axis is the shared time axis under the burn-up and the histogram: a
// few date ticks and a quiet tick for each import in the window.
// Positions are percent of the plot width.
type Axis struct {
	Ticks   []Tick
	Imports []ImportTick
}

// Anchor is an SVG text-anchor value.
type Anchor string

const (
	AnchorStart  Anchor = "start"
	AnchorMiddle Anchor = "middle"
	AnchorEnd    Anchor = "end"
)

// Tick is one labelled moment on the axis.
type Tick struct {
	X      string
	Label  string
	Anchor Anchor
}

// ImportTick marks one imported event (decision 6: the chart's only
// annotations). Label names the plan and its source for the tooltip.
type ImportTick struct {
	X     string
	Label string
}

const (
	// maxTicks keeps the axis to "only a few dates".
	maxTicks = 5
	// edgeAnchorPct is how close to an edge a label must sit before it
	// anchors inward so it stays inside the plot.
	edgeAnchorPct = 4.0

	// axisPlotFloorPx is the narrowest rendered plot width this axis
	// must never crowd at. LayoutAxis runs server-side, before any
	// browser exists to ask, so it cannot know the width it will
	// actually be stretched to — the viewBox is fixed and CSS scales
	// the plot to fill whatever's there (chart.go's package doc).
	// Rather than guess, it reasons about the narrowest phone this
	// dashboard is checked at (a 390px viewport; DESIGN.md names no
	// narrower floor) and picks ticks that never collide there; a wider
	// viewport only gains clearance; it never loses it, because
	// nothing below the panel's 720px breakpoint is percentage-based.
	//
	// Measured with `sleepy query --selector ".c-burnup" --size
	// 390x844` against the chart-panel preview's one-sample state:
	// the plot is 236px wide there. That is 390px minus the panel's
	// fixed chrome (154px): the page container's padding (24px ×2),
	// the panel's border (1px ×2) and inner padding (12px ×2 at this
	// width), the column gap (8px) and the end-label column at its
	// phone width (72px) — all fixed pixel tokens, none of them
	// percentage-based, so the same 154px chrome is true for every
	// viewport at or under 720px, only the plot's 1fr share changes.
	axisPlotFloorPx = 236.0

	// axisCharPx is --font-mono at --font-data-id-size's fixed
	// advance width — a monospace face renders every character the
	// same width — measured the same way against two label lengths:
	// 34.02px / 5 chars ("17:00") and 20.41px / 3 chars both give
	// 6.80px.
	axisCharPx = 6.803

	// axisLabelGapPx is the least clearance kept between two labels'
	// boxes, so a passing pair never renders edge-to-edge.
	axisLabelGapPx = 4.0
)

// tickUnit is one candidate spacing for axis ticks, finest first.
type tickUnit struct {
	floor func(time.Time) time.Time
	next  func(time.Time) time.Time
	label func(time.Time) string
}

func clockUnit(d time.Duration) tickUnit {
	return tickUnit{
		floor: func(t time.Time) time.Time {
			y, m, dd := t.Date()
			return time.Date(y, m, dd, 0, 0, 0, 0, t.Location())
		},
		next:  func(t time.Time) time.Time { return t.Add(d) },
		label: clockLabel,
	}
}

func dayUnit(days int) tickUnit {
	return tickUnit{
		floor: func(t time.Time) time.Time {
			y, m, d := t.Date()
			return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
		},
		next:  func(t time.Time) time.Time { return t.AddDate(0, 0, days) },
		label: func(t time.Time) string { return t.Format("Jan 2") },
	}
}

var weekUnit = tickUnit{
	floor: func(t time.Time) time.Time { return job.BucketWeek.Floor(t) },
	next:  func(t time.Time) time.Time { return t.AddDate(0, 0, 7) },
	label: func(t time.Time) string { return t.Format("Jan 2") },
}

func monthUnit(months int) tickUnit {
	return tickUnit{
		floor: func(t time.Time) time.Time {
			return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
		},
		next: func(t time.Time) time.Time { return t.AddDate(0, months, 0) },
		label: func(t time.Time) string {
			if t.Month() == time.January {
				return t.Format("2006")
			}
			return t.Format("Jan")
		},
	}
}

var tickUnits = []tickUnit{
	clockUnit(5 * time.Minute), clockUnit(10 * time.Minute), clockUnit(15 * time.Minute),
	clockUnit(30 * time.Minute), clockUnit(time.Hour), clockUnit(2 * time.Hour),
	clockUnit(3 * time.Hour), clockUnit(6 * time.Hour), clockUnit(12 * time.Hour),
	dayUnit(1), dayUnit(2), weekUnit, monthUnit(1), monthUnit(3), monthUnit(6), monthUnit(12),
}

// clockLabel reads a sub-day tick as a clock time, except that local
// midnight names the day it opens.
func clockLabel(t time.Time) string {
	if t.Hour() == 0 && t.Minute() == 0 {
		return t.Format("Jan 2")
	}
	return t.Format("15:04")
}

// LayoutAxis picks the finest calendar unit that puts at most maxTicks
// ticks inside rep's window, in loc's calendar, whose labels also fit
// without colliding at axisPlotFloorPx, and places each import marker
// that falls inside it.
func LayoutAxis(rep job.Report, loc *time.Location) Axis {
	var a Axis
	since, until := rep.Window.Since.In(loc), rep.Window.Until.In(loc)
	sc := newTimeScale(rep.Window)
	if until.After(since) {
		var coarsest []Tick // the sparsest candidate tried, in case none fit cleanly
		for _, u := range tickUnits {
			times := unitTicks(u, since, until)
			if len(times) > maxTicks {
				continue
			}
			cand := labelTicks(sc, u, times)
			coarsest = cand
			if ticksFit(cand) {
				a.Ticks = cand
				break
			}
		}
		if a.Ticks == nil {
			a.Ticks = coarsest
		}
	}
	for _, m := range rep.Imports {
		if m.At.Before(rep.Window.Since) || m.At.After(rep.Window.Until) {
			continue
		}
		label := "Imported " + m.Title
		if m.Source != "" {
			label += " from " + m.Source
		}
		a.Imports = append(a.Imports, ImportTick{X: fmtPct(sc.frac(m.At) * 100), Label: label})
	}
	return a
}

// unitTicks lists u's boundaries inside (since, until], stopping early
// once there are more than maxTicks.
func unitTicks(u tickUnit, since, until time.Time) []time.Time {
	var out []time.Time
	for t := u.floor(since); !t.After(until); t = u.next(t) {
		if !t.After(since) {
			continue
		}
		out = append(out, t)
		if len(out) > maxTicks {
			break
		}
	}
	return out
}

func anchorFor(p float64) Anchor {
	switch {
	case p < edgeAnchorPct:
		return AnchorStart
	case p > 100-edgeAnchorPct:
		return AnchorEnd
	default:
		return AnchorMiddle
	}
}

// labelTicks places times on sc's scale as labelled, anchored ticks.
func labelTicks(sc timeScale, u tickUnit, times []time.Time) []Tick {
	ticks := make([]Tick, len(times))
	for i, t := range times {
		p := sc.frac(t) * 100
		ticks[i] = Tick{X: fmtPct(p), Label: u.label(t), Anchor: anchorFor(p)}
	}
	return ticks
}

// ticksFit reports whether ticks' labels sit clear of one another,
// rendered at axisPlotFloorPx — the narrowest width this axis must
// support (axisPlotFloorPx's doc comment). Ticks are already ordered
// left to right, so only adjacent pairs can possibly overlap.
func ticksFit(ticks []Tick) bool {
	for i := 1; i < len(ticks); i++ {
		_, prevHi := labelSpanPx(ticks[i-1])
		lo, _ := labelSpanPx(ticks[i])
		if lo < prevHi+axisLabelGapPx {
			return false
		}
	}
	return true
}

// labelSpanPx is where tk's label sits along the plot at
// axisPlotFloorPx, in px from the plot's left edge — start grows
// right from x, end grows left into x, middle straddles it, the way
// SVG's text-anchor lays glyphs out.
func labelSpanPx(tk Tick) (lo, hi float64) {
	x := pct(tk.X) / 100 * axisPlotFloorPx
	w := float64(len([]rune(tk.Label))) * axisCharPx
	switch tk.Anchor {
	case AnchorStart:
		return x, x + w
	case AnchorEnd:
		return x - w, x
	default:
		return x - w/2, x + w/2
	}
}
