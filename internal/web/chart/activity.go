package chart

import (
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// ActivityKind is one of the histogram's four stacked event kinds. Its
// value is the CSS modifier the segment and its legend swatch take.
type ActivityKind string

const (
	KindDone    ActivityKind = "done"
	KindClaimed ActivityKind = "claimed"
	KindCreated ActivityKind = "created"
	KindBlocked ActivityKind = "blocked"
)

// Activity is the histogram's geometry: one stacked bar per bucket that
// saw events, in a ViewW × ActivityViewH viewBox on the burn-up's time
// scale, plus per-kind totals for the legend.
type Activity struct {
	// Empty is true when no bucket in the window saw an event.
	Empty bool
	Bars  []Bar

	Created int
	Claimed int
	Done    int
	Blocked int
	Total   int
	// Peak is the busiest bucket's total: the bars' full height.
	Peak int

	Rows []ActivityRow
}

// Bar is one bucket's stack. Segments run from the baseline up.
type Bar struct {
	X        float64
	W        float64
	Segments []Segment
}

// Segment is one kind's share of a bar.
type Segment struct {
	Y    float64
	H    float64
	Kind ActivityKind
}

// ActivityRow is one bucket, for the histogram's data table.
type ActivityRow struct {
	When    string
	Created int
	Claimed int
	Done    int
	Blocked int
}

// barFill is the share of its bucket a bar occupies; the rest is the
// gap between bars.
const barFill = 0.8

// LayoutActivity lays out rep's per-bucket activity counts. loc is the
// calendar the table's times read in.
func LayoutActivity(rep job.Report, loc *time.Location) Activity {
	var a Activity
	busiest := 0
	for _, c := range rep.Activity {
		busiest = max(busiest, activityTotal(c))
		a.Created += c.Created
		a.Claimed += c.Claimed
		a.Done += c.Done
		a.Blocked += c.Blocked
		a.Rows = append(a.Rows, ActivityRow{
			When:    c.Start.In(loc).Format(tableTimeLayout),
			Created: c.Created, Claimed: c.Claimed, Done: c.Done, Blocked: c.Blocked,
		})
	}
	a.Total = a.Created + a.Claimed + a.Done + a.Blocked
	a.Peak = busiest
	if busiest == 0 {
		a.Empty = true
		return a
	}

	sc := newTimeScale(rep.Window)
	for _, c := range rep.Activity {
		total := activityTotal(c)
		if total == 0 {
			continue
		}
		x0, x1 := sc.x(c.Start), sc.x(c.End)
		slot := x1 - x0
		bar := Bar{X: x0 + slot*(1-barFill)/2, W: slot * barFill}
		cum := 0
		for _, part := range []struct {
			n    int
			kind ActivityKind
		}{
			{c.Done, KindDone}, {c.Claimed, KindClaimed}, {c.Created, KindCreated}, {c.Blocked, KindBlocked},
		} {
			if part.n == 0 {
				continue
			}
			below := ActivityViewH * (1 - float64(cum)/float64(busiest))
			cum += part.n
			top := ActivityViewH * (1 - float64(cum)/float64(busiest))
			bar.Segments = append(bar.Segments, Segment{Y: top, H: below - top, Kind: part.kind})
		}
		a.Bars = append(a.Bars, bar)
	}
	return a
}

func activityTotal(c job.ActivityCount) int {
	return c.Created + c.Claimed + c.Done + c.Blocked
}

// Left and Width are the bar's x attributes in viewBox units.
func (b Bar) Left() string  { return fmtNum(b.X) }
func (b Bar) Width() string { return fmtNum(b.W) }

// Top and Height are the segment's y attributes in viewBox units.
func (s Segment) Top() string    { return fmtNum(s.Y) }
func (s Segment) Height() string { return fmtNum(s.H) }
