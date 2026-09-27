package handlers

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The hover is progressive enhancement (decision 9): the server ships
// the crosshair, the tooltips and the live region hidden, and the
// script only positions and fills them. These hooks are the contract
// with chart-panel-hover.mjs.

func TestChartPanelTemplate_ShipsTheCrosshairInBothCharts(t *testing.T) {
	out := renderPanel(t, livePanel())
	if n := strings.Count(out, `class="c-chart-cross`); n != 2 {
		t.Errorf("crosshair lines = %d, want one per chart:\n%s", n, out)
	}
	mustHave(t, out, `<line class="c-chart-cross c-chart-cross--burnup"`, `<line class="c-chart-cross c-chart-cross--activity"`)
}

func TestChartPanelTemplate_ShipsATooltipPerChart(t *testing.T) {
	out := renderPanel(t, livePanel())
	if n := strings.Count(out, `<div class="c-chart-tip"`); n != 2 {
		t.Errorf("tooltips = %d, want one per chart:\n%s", n, out)
	}
	mustHave(t, out, `class="c-chart-tip__when"`, `class="c-chart-tip__events"`, `class="c-chart-tip__state"`)
	// The tooltip repeats what the live region says; assistive tech
	// hears it once, from there.
	mustHave(t, out, `<div class="c-chart-tip" data-side="end" aria-hidden="true">`)
}

func TestChartPanelTemplate_ShipsAPoliteLiveRegion(t *testing.T) {
	mustHave(t, renderPanel(t, livePanel()), `<p class="sr-only c-chart-panel__live" aria-live="polite"></p>`)
}

// The script rewrites each end-label block and legend count in place,
// so each needs a handle naming which it is.
func TestChartPanelTemplate_EndLabelsAndLegendCountsHaveHandles(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out,
		`<g class="c-burnup-ends__block" data-end="created">`,
		`<g class="c-burnup-ends__block" data-end="done">`,
		`<span class="c-activity-legend__count" data-kind="created">20</span> created`,
		`<span class="c-activity-legend__count" data-kind="claimed">3</span> claimed`,
		`<span class="c-activity-legend__count" data-kind="done">12</span> done`,
		`<span class="c-activity-legend__count" data-kind="blocked">1</span> blocked`,
	)
}

// A state reachable only through the script is declared by setting the
// attribute the script sets: data-hover-at (the moment, Unix ms) and
// data-hover-chart (which chart the pointer is over).
func TestChartPanelTemplate_HoverAtIsTheScriptsAttribute(t *testing.T) {
	p := livePanel()
	if out := renderPanel(t, p); strings.Contains(out, `data-hover-at`) || strings.Contains(out, `data-hover-chart`) {
		t.Errorf("a resting panel declares a hover:\n%s", out)
	}
	p.HoverAt = p.Data.Trace[1].T
	p.HoverChart = ChartActivity
	out := renderPanel(t, p)
	want := regexp.MustCompile(`<chart-panel [^>]*data-hover-at="` + strconv.FormatInt(p.HoverAt, 10) + `" data-hover-chart="activity"`)
	if !want.MatchString(out) {
		t.Errorf("host lacks data-hover-at/data-hover-chart:\n%s", firstLine(out))
	}
}

func TestChartPanelTemplate_SlidingIsTheScriptsAttribute(t *testing.T) {
	p := livePanel()
	if out := renderPanel(t, p); strings.Contains(out, `data-chart-anim`) {
		t.Errorf("a resting panel declares a slide:\n%s", firstLine(out))
	}
	p.Sliding = true
	mustHave(t, renderPanel(t, p), `data-chart-anim="slide"`)
}

func firstLine(s string) string {
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return before
	}
	return s
}
