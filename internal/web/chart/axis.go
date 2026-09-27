package chart

import (
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// Axis is the time axis drawn under each chart (both share one
// window): unlabelled minor marks on a calendar unit, a few labelled
// moments between two labelled edges, and a link for each import in
// the window. Positions are percent of the plot width.
type Axis struct {
	// Ticks are the labelled moments, left to right. The first is
	// always the window's start at 0% and the last its end at 100%.
	Ticks []Tick
	// Minor are the unlabelled marks (decision 7), as x percentages.
	Minor   []string
	Imports []ImportTick
}

// WindowEnd is what the window's right edge is, which only the handler
// knows: the live moment, or the scrubber's cursor.
type WindowEnd string

const (
	// EndsNow is a live window; its right edge reads "Now".
	EndsNow WindowEnd = "now"
	// EndsAtCursor is a window rendered for ?at=; its right edge reads
	// the cursor's moment.
	EndsAtCursor WindowEnd = "cursor"
)

// nowLabel is the right edge of a live window.
const nowLabel = "Now"

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

// IsEnd reports whether tk is the window's right edge — the tick that
// reads "Now" or the cursor's moment.
func (tk Tick) IsEnd() bool { return tk.X == "100%" && tk.Anchor == AnchorEnd }

// ImportTick marks one imported event (reporting decision 6: the
// charts' only annotations) as a link to the imported task (decision
// 8). Label names the plan and its source.
type ImportTick struct {
	X     string
	Href  string
	Label string
}

const (
	// maxTicks caps the interior labels: only a few dates.
	maxTicks = 5

	// axisPlotFloorPx is the narrowest rendered plot width this axis
	// must never crowd at. LayoutAxis runs server-side, before any
	// browser exists to ask, so it cannot know the width it will be
	// stretched to — the viewBox is fixed and CSS scales the plot to
	// fill whatever's there (chart.go's package doc). It reasons about
	// the narrowest plot the panel renders instead and picks labels
	// that never collide there; a wider plot only gains clearance.
	//
	// Both charts carry an axis (chart panel revision, decision 3), and
	// the histogram's is the narrower: its legend column, sized to its
	// content, is wider than the burn-up's end labels. Measured with
	// `sleepy query <preview>/chart-panel/<state> --selector
	// ".c-activity" --size WxH` on the crowded state (four-digit legend
	// counts, the widest the catalog draws): 225px at 390x1600
	// (stacked, the narrowest), 238.5px at 721x900 (side by side, just
	// above the 720px breakpoint), 476px at 1440x900. The burn-up
	// measured 257px, 255px and 493px. One more legend digit costs
	// axisCharPx, so the floor leaves room for five-digit counts:
	// 225 − 7 = 218px.
	axisPlotFloorPx = 218.0

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

var (
	fiveMinuteUnit = clockUnit(5 * time.Minute)
	hourUnit       = clockUnit(time.Hour)
	oneDayUnit     = dayUnit(1)
	oneMonthUnit   = monthUnit(1)
	oneYearUnit    = monthUnit(12)
)

// labelUnits are the candidate spacings for labelled ticks, finest
// first.
var labelUnits = []tickUnit{
	fiveMinuteUnit, clockUnit(10 * time.Minute), clockUnit(15 * time.Minute),
	clockUnit(30 * time.Minute), hourUnit, clockUnit(2 * time.Hour),
	clockUnit(3 * time.Hour), clockUnit(6 * time.Hour), clockUnit(12 * time.Hour),
	oneDayUnit, dayUnit(2), weekUnit, oneMonthUnit, monthUnit(3), monthUnit(6), oneYearUnit,
}

// minorScale is the minor mark unit for a span and the grain its edge
// labels read at (decision 7: 5 minutes on 1H, an hour on 1D, a day on
// 7D and 14D, a week on 30D; All by the same rule on its span).
type minorScale struct {
	upTo time.Duration // the longest span this scale serves
	unit tickUnit
	edge string // the time layout both edge labels use
}

const (
	day  = 24 * time.Hour
	year = 365 * day
)

var minorScales = []minorScale{
	{2 * time.Hour, fiveMinuteUnit, "15:04"},
	{2 * day, hourUnit, "15:04"},
	{21 * day, oneDayUnit, "Jan 2"},
	{92 * day, weekUnit, "Jan 2"},
	{year, oneMonthUnit, "Jan 2"},
	{3 * year, oneMonthUnit, "Jan 2006"},
}

var widestScale = minorScale{unit: oneYearUnit, edge: "Jan 2006"}

func scaleFor(span time.Duration) minorScale {
	for _, s := range minorScales {
		if span <= s.upTo {
			return s
		}
	}
	return widestScale
}

// clockLabel reads a sub-day tick as a clock time, except that local
// midnight names the day it opens.
func clockLabel(t time.Time) string {
	if t.Hour() == 0 && t.Minute() == 0 {
		return t.Format("Jan 2")
	}
	return t.Format("15:04")
}

// LayoutAxis lays out rep's window in loc's calendar: minor marks on
// the span's unit, both edges labelled — the start's moment, and "Now"
// or the cursor's moment by end — and between them the finest calendar
// unit whose labels fit at axisPlotFloorPx without colliding with one
// another or the edges. Each import in the window becomes a link.
func LayoutAxis(rep job.Report, loc *time.Location, end WindowEnd) Axis {
	var a Axis
	since, until := rep.Window.Since.In(loc), rep.Window.Until.In(loc)
	sc := newTimeScale(rep.Window)
	if until.After(since) {
		scale := scaleFor(until.Sub(since))
		for _, t := range unitTicks(scale.unit, since, until, -1) {
			if t.Before(until) {
				a.Minor = append(a.Minor, fmtPct(sc.frac(t)*100))
			}
		}
		endLabel := nowLabel
		if end == EndsAtCursor {
			endLabel = until.Format(scale.edge)
		}
		first := Tick{X: "0%", Label: since.Format(scale.edge), Anchor: AnchorStart}
		last := Tick{X: "100%", Label: endLabel, Anchor: AnchorEnd}
		a.Ticks = append(append([]Tick{first}, interiorTicks(sc, since, until, first, last)...), last)
	}
	for _, m := range rep.Imports {
		if m.At.Before(rep.Window.Since) || m.At.After(rep.Window.Until) {
			continue
		}
		label := "Imported " + m.Title
		if m.Source != "" {
			label += " from " + m.Source
		}
		a.Imports = append(a.Imports, ImportTick{
			X: fmtPct(sc.frac(m.At) * 100), Href: "/tasks/" + m.TaskID, Label: label,
		})
	}
	return a
}

// interiorTicks picks the finest label unit whose boundaries strictly
// inside the window — less any that would collide with an edge label —
// number at most maxTicks and sit clear of one another. None fits only
// on a window too narrow for any interior label.
func interiorTicks(sc timeScale, since, until time.Time, first, last Tick) []Tick {
	for _, u := range labelUnits {
		times := unitTicks(u, since, until, maxTicks*4)
		if times == nil {
			continue
		}
		var cand []Tick
		for _, t := range times {
			if !t.Before(until) {
				continue
			}
			tk := Tick{X: fmtPct(sc.frac(t) * 100), Label: u.label(t), Anchor: AnchorMiddle}
			if clearOf(first, tk) && clearOf(tk, last) {
				cand = append(cand, tk)
			}
		}
		if len(cand) <= maxTicks && ticksFit(cand) {
			return cand
		}
	}
	return nil
}

// unitTicks lists u's boundaries inside (since, until]. With limit
// ≥ 0 it gives up — returning nil — once there are more than limit.
func unitTicks(u tickUnit, since, until time.Time, limit int) []time.Time {
	var out []time.Time
	for t := u.floor(since); !t.After(until); t = u.next(t) {
		if !t.After(since) {
			continue
		}
		out = append(out, t)
		if limit >= 0 && len(out) > limit {
			return nil
		}
	}
	return out
}

// ticksFit reports whether ticks' labels sit clear of one another.
// Ticks are ordered left to right, so only adjacent pairs can overlap.
func ticksFit(ticks []Tick) bool {
	for i := 1; i < len(ticks); i++ {
		if !clearOf(ticks[i-1], ticks[i]) {
			return false
		}
	}
	return true
}

// clearOf reports whether left's label ends at least axisLabelGapPx
// before right's begins, rendered at axisPlotFloorPx — the narrowest
// plot this axis must support (axisPlotFloorPx's doc comment).
func clearOf(left, right Tick) bool {
	_, hi := labelSpanPx(left)
	lo, _ := labelSpanPx(right)
	return lo >= hi+axisLabelGapPx
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
