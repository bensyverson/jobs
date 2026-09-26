package job

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

// The burn-up's cells. Shades rather than colours, so done, blocked and
// open stay distinct in a monochrome terminal, a log file or a paste.
const (
	glyphDone    = "█"
	glyphBlocked = "▒"
	glyphOpen    = "░"
)

const (
	// chartHeight is the burn-up's plot rows. Eight keeps the whole
	// report on one screen while still resolving a 1-in-8 change.
	chartHeight = 8
	// minChartWidth is the narrowest the burn-up draws; below it the
	// axis labels would crowd out the plot.
	minChartWidth = 24
	// maxColumnWidth caps how wide one sample draws when there are few,
	// so a four-bucket report stays a chart rather than four slabs.
	maxColumnWidth = 6
)

const chartLabel = "Burn-up"

// writeBurnUp draws r.Series as a column per sample: done from the
// floor, the blocked share above it, then open up to scope. When there
// are more samples than columns, each column shows the last sample of
// its span — a sample is state as of an instant, so the latest is the
// span's truth.
func writeBurnUp(b *strings.Builder, r Report, width int, loc *time.Location) {
	if width <= 0 {
		width = DefaultReportWidth
	}
	width = max(width, minChartWidth)

	peak := 0
	for _, s := range r.Series {
		peak = max(peak, s.Scope)
	}
	if peak == 0 {
		headline(b, chartLabel, "nothing in scope in this window")
		return
	}

	legend := fmt.Sprintf("%s done  %s blocked  %s open", glyphDone, glyphBlocked, glyphOpen)
	if full := fmt.Sprintf("%-*s%s", len(headlineIndent), chartLabel, legend); utf8.RuneCountInString(full) <= width {
		b.WriteString(full + "\n")
	} else {
		b.WriteString(chartLabel + "\n")
		fmt.Fprintf(b, "%s done %s blocked %s open\n", glyphDone, glyphBlocked, glyphOpen)
	}

	top := commas(peak)
	gutter := len(top) + 2 // label, space, axis
	plotWidth := width - gutter
	samples, colWidth := fitColumns(r.Series, plotWidth)

	for row := chartHeight - 1; row >= 0; row-- {
		label, axis := "", "│"
		if row == chartHeight-1 {
			label, axis = top, "┤"
		}
		var line strings.Builder
		fmt.Fprintf(&line, "%*s %s", len(top), label, axis)
		for _, s := range samples {
			line.WriteString(strings.Repeat(cellFor(s, row, peak), colWidth))
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}
	drawn := len(samples) * colWidth
	fmt.Fprintf(b, "%*s └%s\n", len(top), "0", strings.Repeat("─", drawn))
	b.WriteString(dateAxis(samples, r.Window.Bucket, loc, gutter, drawn) + "\n")
}

// fitColumns picks the samples to draw and how wide each is.
func fitColumns(series []Sample, plotWidth int) ([]Sample, int) {
	n := len(series)
	if n <= plotWidth {
		return series, min(plotWidth/n, maxColumnWidth)
	}
	picked := make([]Sample, plotWidth)
	for c := range plotWidth {
		picked[c] = series[(c+1)*n/plotWidth-1]
	}
	return picked, 1
}

// cellFor is the glyph at row (0 is the floor) of s's column. Each
// band's top is its cumulative total rounded to a row, so a band
// smaller than half a row vanishes rather than being inflated to one —
// except scope, which keeps one cell so a non-empty column is visible.
func cellFor(s Sample, row, peak int) string {
	rows := func(v int) int { return int(math.Round(float64(v) * chartHeight / float64(peak))) }
	scope := rows(s.Scope)
	if s.Scope > 0 && scope == 0 {
		scope = 1
	}
	done := min(rows(s.Done), scope)
	blocked := min(max(rows(s.Done+s.Blocked), done), scope)
	switch {
	case row < done:
		return glyphDone
	case row < blocked:
		return glyphBlocked
	case row < scope:
		return glyphOpen
	default:
		return " "
	}
}

// dateAxis labels the first column's start and the last column's end.
// When both do not fit, only the end is labelled.
func dateAxis(samples []Sample, bucket Bucket, loc *time.Location, gutter, drawn int) string {
	layout := "Jan 2"
	switch bucket {
	case BucketMinute, BucketHour:
		layout = "15:04"
	case BucketSixHours:
		layout = "Jan 2 15:04"
	}
	first := samples[0].End.In(loc).Format(layout)
	last := samples[len(samples)-1].End.In(loc).Format(layout)
	pad := strings.Repeat(" ", gutter)
	if len(samples) == 1 || len(first)+1+len(last) > drawn {
		return fmt.Sprintf("%*s", max(gutter+drawn, len(last)), last)
	}
	return pad + first + fmt.Sprintf("%*s", drawn-len(first), last)
}
