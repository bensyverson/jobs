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
// ticks inside rep's window, in loc's calendar, and places each import
// marker that falls inside it.
func LayoutAxis(rep job.Report, loc *time.Location) Axis {
	var a Axis
	since, until := rep.Window.Since.In(loc), rep.Window.Until.In(loc)
	sc := newTimeScale(rep.Window)
	if until.After(since) {
		for _, u := range tickUnits {
			ticks := unitTicks(u, since, until)
			if len(ticks) > maxTicks {
				continue
			}
			for _, t := range ticks {
				p := sc.frac(t) * 100
				a.Ticks = append(a.Ticks, Tick{X: fmtPct(p), Label: u.label(t), Anchor: anchorFor(p)})
			}
			break
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
