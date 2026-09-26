package chart

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// Burnup is the burn-up chart's geometry: the scope and done lines,
// the open-work gap between them with its blocked share, sparse
// gridlines, the two end labels, and the series as table rows for
// assistive tech. Paths are in a ViewW × BurnupViewH viewBox.
type Burnup struct {
	// Empty is true when nothing was ever in scope, done or canceled in
	// the window; the panel then says so instead of drawing flat lines.
	Empty bool

	ScopePath string
	DonePath  string
	// GapPath is the open work: the area between scope and done.
	GapPath string
	// BlockedPath is the blocked share of the gap, stacked directly on
	// the done line and never above scope.
	BlockedPath string

	// ScopeDot and DoneDot mark each line's last sample, in percent of
	// the plot, so even a one-sample series shows.
	ScopeDot Point
	DoneDot  Point

	Gridlines []Gridline
	Scope     EndLabel
	Done      EndLabel

	// Open, Blocked and Canceled are the state at the window's end.
	Open     int
	Blocked  int
	Canceled int

	// Summary restates the end state in a sentence, for the SVG's
	// <desc>.
	Summary string
	Rows    []BurnupRow

	top float64 // the value at the top of the plot
}

// Gridline is one horizontal rule, at Y percent of the plot height.
type Gridline struct {
	Y     string
	Label string
}

// Point is a position in percent of the plot.
type Point struct {
	X string
	Y string
}

// EndLabel is a line's final value, placed at Y percent of the plot
// height beside the line's end.
type EndLabel struct {
	Value int
	Text  string
	Y     string
}

// BurnupRow is one sample, for the chart's data table.
type BurnupRow struct {
	When     string
	Scope    int
	Done     int
	Open     int
	Blocked  int
	Canceled int
}

const (
	// headroom lifts the plot's top above the tallest scope so the
	// line never runs along the panel's edge.
	headroom = 1.08
	// gridTarget is how many intervals the gridlines aim to cut the
	// plot into: sparse, a reference rather than a lattice.
	gridTarget = 3
	// minEndLabelGapPct keeps the two end labels (a large number over a
	// small-caps word) from overlapping when the lines end close
	// together. Percent of the plot height, which CSS fixes.
	minEndLabelGapPct = 20.0
	// endLabelTopPct and endLabelBottomPct keep the end labels inside
	// the plot's box.
	endLabelTopPct    = 8.0
	endLabelBottomPct = 86.0
)

// y maps a count to a viewBox y coordinate.
func (b Burnup) y(v int) float64 {
	return BurnupViewH - float64(v)/b.top*BurnupViewH
}

// LayoutBurnup lays out rep's series. loc is the calendar the table's
// times read in.
func LayoutBurnup(rep job.Report, loc *time.Location) Burnup {
	var b Burnup
	if isEmptySeries(rep.Series) {
		b.Empty = true
		return b
	}
	maxScope := 0
	for _, s := range rep.Series {
		maxScope = max(maxScope, s.Scope)
	}
	step := niceStep(float64(maxScope) / gridTarget)
	b.top = max(float64(maxScope)*headroom, 1)
	for v := step; v <= float64(maxScope); v += step {
		b.Gridlines = append(b.Gridlines, Gridline{
			Y:     fmtPct(b.y(int(v)) / BurnupViewH * 100),
			Label: Count(int(v)),
		})
	}

	sc := newTimeScale(rep.Window)
	n := len(rep.Series)
	xs := make([]float64, n)
	scope := make([]float64, n)
	done := make([]float64, n)
	blockedTop := make([]float64, n)
	for i, s := range rep.Series {
		xs[i] = sc.x(s.End)
		scope[i] = b.y(s.Scope)
		done[i] = b.y(s.Done)
		blockedTop[i] = b.y(min(s.Done+s.Blocked, s.Scope))
		b.Rows = append(b.Rows, BurnupRow{
			When:  s.End.In(loc).Format(tableTimeLayout),
			Scope: s.Scope, Done: s.Done, Open: s.Open, Blocked: s.Blocked, Canceled: s.Canceled,
		})
	}
	b.ScopePath = linePath(xs, scope)
	b.DonePath = linePath(xs, done)
	b.GapPath = areaPath(xs, scope, done)
	b.BlockedPath = areaPath(xs, blockedTop, done)

	endX := fmtPct(xs[n-1] / ViewW * 100)
	b.ScopeDot = Point{X: endX, Y: fmtPct(scope[n-1] / BurnupViewH * 100)}
	b.DoneDot = Point{X: endX, Y: fmtPct(done[n-1] / BurnupViewH * 100)}

	last := rep.Series[n-1]
	b.Open, b.Blocked, b.Canceled = last.Open, last.Blocked, last.Canceled
	scopeY, doneY := endLabelPositions(scope[n-1]/BurnupViewH*100, done[n-1]/BurnupViewH*100)
	b.Scope = EndLabel{Value: last.Scope, Text: Count(last.Scope), Y: fmtPct(scopeY)}
	b.Done = EndLabel{Value: last.Done, Text: Count(last.Done), Y: fmtPct(doneY)}
	b.Summary = burnupSummary(last)
	return b
}

func isEmptySeries(series []job.Sample) bool {
	for _, s := range series {
		if s.Scope != 0 || s.Done != 0 || s.Canceled != 0 {
			return false
		}
	}
	return true
}

// niceStep rounds raw to the nearest 1, 2, 2.5 or 5 × 10ⁿ, never
// below 1: counts are whole leaves.
func niceStep(raw float64) float64 {
	if raw <= 1 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	best := mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if math.Abs(m*mag-raw) < math.Abs(best-raw) {
			best = m * mag
		}
	}
	if best < 10 {
		best = math.Round(best) // 2.5 is not a whole leaf
	}
	return max(best, 1)
}

// linePath joins the points. A single point becomes a zero-length
// segment, which round line caps draw as a dot.
func linePath(xs, ys []float64) string {
	var b strings.Builder
	for i := range xs {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&b, "%s%s %s", cmd, fmtNum(xs[i]), fmtNum(ys[i]))
	}
	if len(xs) == 1 {
		fmt.Fprintf(&b, "L%s %s", fmtNum(xs[0]), fmtNum(ys[0]))
	}
	return b.String()
}

// areaPath closes the region between an upper edge (forward) and a
// lower edge (back).
func areaPath(xs, upper, lower []float64) string {
	var b strings.Builder
	for i := range xs {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&b, "%s%s %s", cmd, fmtNum(xs[i]), fmtNum(upper[i]))
	}
	for i, x := range slices.Backward(xs) {
		fmt.Fprintf(&b, "L%s %s", fmtNum(x), fmtNum(lower[i]))
	}
	b.WriteString("Z")
	return b.String()
}

// endLabelPositions nudges the two labels apart around their midpoint
// when the lines end closer than minEndLabelGapPct, then keeps both
// inside the plot. Scope is never below done, so scope's label is the
// upper one.
func endLabelPositions(scopeY, doneY float64) (float64, float64) {
	if doneY-scopeY < minEndLabelGapPct {
		mid := (scopeY + doneY) / 2
		scopeY, doneY = mid-minEndLabelGapPct/2, mid+minEndLabelGapPct/2
	}
	if scopeY < endLabelTopPct {
		doneY += endLabelTopPct - scopeY
		scopeY = endLabelTopPct
	}
	if doneY > endLabelBottomPct {
		scopeY -= doneY - endLabelBottomPct
		doneY = endLabelBottomPct
	}
	return clamp(scopeY, endLabelTopPct, endLabelBottomPct), clamp(doneY, endLabelTopPct, endLabelBottomPct)
}

func burnupSummary(s job.Sample) string {
	return fmt.Sprintf("%s in scope, %s done, %s open of which %s blocked, %s canceled.",
		Count(s.Scope), Count(s.Done), Count(s.Open), Count(s.Blocked), Count(s.Canceled))
}

// Caption is the open work at the window's end in words — the gap and
// its blocked share are drawn, so they are also said. Canceled work is
// named only when there is some (decision 3: it leaves scope and is
// reported alongside).
func (b Burnup) Caption() string {
	s := Count(b.Open) + " open · " + Count(b.Blocked) + " blocked"
	if b.Canceled > 0 {
		s += " · " + Count(b.Canceled) + " canceled"
	}
	return s
}
