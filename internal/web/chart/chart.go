// Package chart lays out the Home view's chart panel — the burn-up,
// the activity histogram and their shared time axis — from a
// job.Report. It holds geometry only: every count comes from the
// report, and nothing here re-derives a counting rule
// (project/2026-09-26-reporting.md, decision 12).
//
// The plots are drawn in fixed viewBoxes stretched to the panel with
// preserveAspectRatio="none" and non-scaling strokes, so their shapes
// fill any width. Anything that must not stretch — gridline labels,
// end labels, axis dates — is positioned in percent of the plot
// instead, which SVG accepts on x/y attributes without scaling glyphs.
package chart

import (
	"strconv"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

const (
	// ViewW is the width of every plot's viewBox; x runs since → until.
	ViewW = 1000.0
	// BurnupViewH is the burn-up viewBox height; y grows downward.
	BurnupViewH = 1000.0
	// ActivityViewH is the histogram viewBox height.
	ActivityViewH = 100.0
)

// tableTimeLayout is how a bucket's moment reads in the charts' data
// tables: unambiguous across every bucket width.
const tableTimeLayout = "2006-01-02 15:04"

// timeScale maps a moment onto the shared since → until x axis.
type timeScale struct {
	since time.Time
	span  float64 // seconds; never zero
}

func newTimeScale(w job.ReportWindow) timeScale {
	span := w.Until.Sub(w.Since).Seconds()
	if span <= 0 {
		span = 1
	}
	return timeScale{since: w.Since, span: span}
}

// frac is t's position along the window, 0 at since and 1 at until.
func (s timeScale) frac(t time.Time) float64 {
	return t.Sub(s.since).Seconds() / s.span
}

// x is t's position in viewBox units.
func (s timeScale) x(t time.Time) float64 { return s.frac(t) * ViewW }

// fmtNum formats a viewBox coordinate compactly: two decimals at
// most, trailing zeros dropped.
func fmtNum(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "-0" {
		return "0"
	}
	return s
}

// fmtPct formats a 0..100 value as an SVG percentage attribute.
func fmtPct(p float64) string { return fmtNum(p) + "%" }

// pct parses a percentage attribute back to its number; -1 when it
// is not one. Tests and label collision checks read positions back.
func pct(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || !strings.HasSuffix(s, "%") {
		return -1
	}
	return v
}

// Count formats a non-negative count with thousands separators:
// 1155 → "1,155".
func Count(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clamp(v, lo, hi float64) float64 {
	return max(lo, min(hi, v))
}
