package handlers

import (
	"bytes"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/chart"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// Home offers every range key, shortest first: 1H keeps the old
// one-minute live histogram reachable (decision 10).
func TestHomeRanges_OfferEveryKey(t *testing.T) {
	wantKeys := job.RangeKeys()
	wantLabels := []string{"1H", "1D", "7D", "14D", "30D", "All"}
	if len(homeRanges.Options) != len(wantKeys) {
		t.Fatalf("homeRanges has %d options, want %d", len(homeRanges.Options), len(wantKeys))
	}
	for i, opt := range homeRanges.Options {
		if opt.Key != wantKeys[i] || opt.Label != wantLabels[i] {
			t.Errorf("option %d = %q/%q, want %q/%q", i, opt.Key, opt.Label, wantKeys[i], wantLabels[i])
		}
	}
	if got := parseRangeKey("1h", homeRanges); got != job.RangeHour {
		t.Errorf("parseRangeKey(1h, homeRanges) = %q, want 1h", got)
	}
}

var panelUntil = time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)

func panelReport() job.Report {
	since := panelUntil.Add(-72 * time.Hour)
	rep := job.Report{
		Schema: job.ReportSchema,
		Window: job.ReportWindow{Since: since, Until: panelUntil, Bucket: job.BucketDay, Timezone: "UTC"},
		Leaves: job.LeafFigures{Created: 20, Done: 12, Canceled: 3, Open: 75, Blocked: 12},
		Series: []job.Sample{
			{End: since.Add(24 * time.Hour), Scope: 12, Done: 2, Open: 10, Blocked: 1},
			{End: since.Add(48 * time.Hour), Scope: 20, Done: 9, Open: 11, Blocked: 3},
			{End: panelUntil, Scope: 1155, Done: 1080, Open: 75, Blocked: 12, Canceled: 4},
		},
		Activity: []job.ActivityCount{
			{Start: since, End: since.Add(24 * time.Hour), Created: 12, Claimed: 3, Done: 2, Blocked: 1},
			{Start: since.Add(24 * time.Hour), End: since.Add(48 * time.Hour), Created: 8, Done: 7},
			{Start: since.Add(48 * time.Hour), End: panelUntil, Done: 3},
		},
		Imports: []job.ImportMarker{{At: since.Add(30 * time.Hour), TaskID: "abc12", Title: "Reporting", Source: "reporting.md"}},
	}
	// The trace starts with the state as of Since, then runs through
	// the series' instants; three leaves were canceled in the window.
	rep.Trace = append([]job.Sample{{End: since, Scope: 8, Done: 1, Open: 7, Canceled: 1}}, rep.Series...)
	return rep
}

// livePanel is the panel over panelReport at 7D, live.
func livePanel() ChartPanel {
	return buildChartPanel("home", panelReport(), nil, navAt(job.Range7D), chart.EndsNow, time.UTC)
}

func TestBuildChartPanel_OtherErrorsNameTheCause(t *testing.T) {
	p := buildChartPanel("home", job.Report{}, errors.New("disk on fire"), navAt(job.Range7D), chart.EndsNow, time.UTC)
	if p.State != PanelError || !strings.Contains(p.Message, "disk on fire") {
		t.Errorf("State %q Message %q, want the error state naming the cause", p.State, p.Message)
	}
}

func TestBuildChartPanel_EmptyReportIsTheEmptyState(t *testing.T) {
	p := buildChartPanel("home", job.Report{Window: panelReport().Window}, nil, navAt(job.Range7D), chart.EndsNow, time.UTC)
	if p.State != PanelEmpty {
		t.Fatalf("State = %q, want %q", p.State, PanelEmpty)
	}
	if !strings.Contains(p.Message, "last 7 days") {
		t.Errorf("Message = %q, want it to name the range", p.Message)
	}
}

func TestBuildChartPanel_LaysOutTheReport(t *testing.T) {
	p := livePanel()
	if p.State != PanelChart {
		t.Fatalf("State = %q, want %q", p.State, PanelChart)
	}
	if p.Burnup.Created.Text != "+20" || p.Activity.Total != 36 || len(p.Axis.Imports) != 1 {
		t.Errorf("panel = created %q, activity %d, imports %d", p.Burnup.Created.Text, p.Activity.Total, len(p.Axis.Imports))
	}
}

// The axis's right edge is the handler's to say: "Now" live, the
// cursor's moment parked under the scrubber.
func TestBuildChartPanel_AxisEndFollowsTheCursor(t *testing.T) {
	last := func(p ChartPanel) string { return p.Axis.Ticks[len(p.Axis.Ticks)-1].Label }
	if got := last(livePanel()); got != "Now" {
		t.Errorf("live end label = %q, want Now", got)
	}
	parked := buildChartPanel("home", panelReport(), nil, navAt(job.Range7D), chart.EndsAtCursor, time.UTC)
	if got := last(parked); got != "Sep 26" {
		t.Errorf("parked end label = %q, want the cursor's day, Sep 26", got)
	}
}

// Decision 4: the caption's canceled count is the window's, and says
// so; open and blocked are the state at the window's end.
func TestChartPanel_CaptionNamesTheWindowsCanceled(t *testing.T) {
	if got := livePanel().Caption(); got != "75 open · 12 blocked · 3 canceled in the last 7 days" {
		t.Errorf("caption = %q", got)
	}
	all := buildChartPanel("home", panelReport(), nil, navAt(job.RangeAll), chart.EndsNow, time.UTC)
	if got := all.Caption(); got != "75 open · 12 blocked · 3 canceled overall" {
		t.Errorf("All caption = %q", got)
	}
	quiet := panelReport()
	quiet.Leaves.Canceled = 0
	p := buildChartPanel("home", quiet, nil, navAt(job.Range7D), chart.EndsNow, time.UTC)
	if got := p.Caption(); got != "75 open · 12 blocked" {
		t.Errorf("caption with nothing canceled = %q, want no canceled clause", got)
	}
}

func renderPanel(t *testing.T, p ChartPanel) string {
	t.Helper()
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	var buf bytes.Buffer
	if err := e.RenderFragment(&buf, "home", "chart_panel", p); err != nil {
		t.Fatalf("render chart_panel: %v", err)
	}
	return buf.String()
}

func mustHave(t *testing.T, body string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(body, n) {
			t.Errorf("missing %q in\n%s", n, body)
		}
	}
}

func TestChartPanelTemplate_BurnupIsAnAccessibleImage(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out,
		`<chart-panel`, `data-home-panel`,
		`role="img"`, `aria-labelledby="home-burnup-title home-burnup-desc"`,
		`<title id="home-burnup-title">`, `<desc id="home-burnup-desc">`,
		`In this window, 20 created, 12 done and 3 canceled`, `1,155 in scope, 1,080 done`,
		`class="c-burnup__scope"`, `class="c-burnup__done"`, `class="c-burnup__gap"`, `class="c-burnup__blocked"`,
		`vector-effect="non-scaling-stroke"`,
		`75 open`, `12 blocked`, `3 canceled in the last 7 days`,
	)
}

// Decision 4: "+N" over "created" and "+N" over "done", each with the
// absolute total at the window's end beneath — under created, the top
// of the canceled band it sits beside (scope 1,155 plus 3 canceled in
// the window).
func TestChartPanelTemplate_EndLabelsAreWindowFigures(t *testing.T) {
	out := renderPanel(t, livePanel())
	// html/template escapes "+" in text as &#43;; the browser reads "+20".
	mustHave(t, out, `>&#43;20<`, `>created<`, `>of 1,158<`, `>&#43;12<`, `>done<`, `>of 1,080<`)
	if strings.Contains(out, `>scope<`) {
		t.Errorf("end labels still read scope:\n%s", out)
	}
}

// Decision 3: no toggle — the burn-up and the histogram render side by
// side in every chart state, each with its own axis and data table.
func TestChartPanelTemplate_DrawsBothChartsSideBySide(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out,
		`class="c-chart-panel__chart c-chart-panel__chart--burnup"`, `class="c-chart-panel__chart c-chart-panel__chart--activity"`,
		`class="c-burnup"`, `class="c-burnup-ends"`, `class="c-activity"`, `class="c-activity-legend"`,
	)
	if n := strings.Count(out, `<svg class="c-chart-axis c-chart-axis--`); n != 2 {
		t.Errorf("axes = %d, want one under each chart", n)
	}
	if strings.Index(out, `c-chart-panel__chart--burnup`) > strings.Index(out, `c-chart-panel__chart--activity`) {
		t.Errorf("the histogram precedes the burn-up; the burn-up comes first (and stacks first)")
	}
	for _, gone := range []string{`c-chart-panel__views`, `aria-label="Chart"`, `chart=`} {
		if strings.Contains(out, gone) {
			t.Errorf("panel still renders the chart toggle (%q)", gone)
		}
	}
}

func TestChartPanelTemplate_ShipsDataTablesForAssistiveTech(t *testing.T) {
	out := renderPanel(t, livePanel())
	// Each table sits inside a visually hidden div rather than carrying
	// sr-only itself: a table grows to its content whatever its width,
	// and at 721px the burn-up's pushed the page 80px past the viewport.
	tables := regexp.MustCompile(`<div class="sr-only">\s*<table>`).FindAllStringIndex(out, -1)
	if len(tables) != 2 {
		t.Fatalf("sr-only tables = %d, want 2 (one per chart)", len(tables))
	}
	mustHave(t, out,
		`<td>2026-09-26 17:00</td>`, `<td>1155</td>`, `<caption>Leaf tasks created, claimed, done and blocked per bucket`,
		`in this window 20 created, 12 done and 3 canceled`,
	)
}

// Decision 8: an import tick is a link to the imported task, which the
// peek sheet opens in place, named for the plan it brought in.
func TestChartPanelTemplate_ImportTicksAreLinks(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out,
		`<a class="c-chart-axis__import-link" href="/tasks/abc12" data-peek aria-label="Imported Reporting from reporting.md">`,
		`<title>Imported Reporting from reporting.md</title>`,
		`class="c-chart-axis__import-hit"`, `class="c-chart-axis__import"`,
		`1 imported</li>`,
	)
}

// The import links are reachable once: the burn-up's axis carries them
// for assistive tech and the keyboard; the histogram's copy is for the
// pointer only, hidden and out of the tab order.
func TestChartPanelTemplate_ImportLinksAreFocusableOnce(t *testing.T) {
	out := renderPanel(t, livePanel())
	if n := strings.Count(out, `href="/tasks/abc12"`); n != 2 {
		t.Fatalf("import links = %d, want one on each axis", n)
	}
	if n := strings.Count(out, `tabindex="-1"`); n != 1 {
		t.Errorf("unfocusable import links = %d, want the histogram's one", n)
	}
	mustHave(t, out,
		`<svg class="c-chart-axis c-chart-axis--burnup">`,
		`<svg class="c-chart-axis c-chart-axis--activity" aria-hidden="true">`,
		`<g class="c-chart-axis__scale" aria-hidden="true">`,
	)
}

// Decision 7: each axis draws its minor marks and labels both edges.
func TestChartPanelTemplate_AxisDrawsMinorTicksAndEdges(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out, `class="c-chart-axis__minor"`, `>Now</text>`, `>Sep 23</text>`)
}

func TestChartPanelTemplate_HistogramStacksByKind(t *testing.T) {
	out := renderPanel(t, livePanel())
	mustHave(t, out,
		`class="c-activity__seg c-activity__seg--done"`,
		`class="c-activity__seg c-activity__seg--created"`,
		`>20</span> created`, `>3</span> claimed`, `>12</span> done`, `>1</span> blocked`,
	)
}

// The web rules forbid inline style attributes; the panel positions
// everything with SVG attributes and classes.
func TestChartPanelTemplate_HasNoInlineStyles(t *testing.T) {
	for _, p := range []ChartPanel{
		livePanel(),
		buildChartPanel("home", job.Report{}, errors.New("disk on fire"), navAt(job.Range7D), chart.EndsNow, time.UTC),
		buildChartPanel("home", job.Report{}, nil, navAt(job.Range7D), chart.EndsNow, time.UTC),
	} {
		if out := renderPanel(t, p); strings.Contains(out, "style=") {
			t.Errorf("state %q renders an inline style:\n%s", p.State, out)
		}
	}
}

func TestChartPanelTemplate_ErrorStateKeepsTheSelector(t *testing.T) {
	out := renderPanel(t, buildChartPanel("home", job.Report{}, errors.New("disk on fire"), homePanelNav(nil), chart.EndsNow, time.UTC))
	mustHave(t, out, `disk on fire`, `>1H<`, `>All<`, `aria-current="true">1D<`)
	if strings.Contains(out, `role="img"`) {
		t.Errorf("error state draws a chart:\n%s", out)
	}
}

func TestChartPanelTemplate_PendingSetsAriaBusy(t *testing.T) {
	p := livePanel()
	p.Pending = true
	mustHave(t, renderPanel(t, p), `aria-busy="true"`)
	p.Pending = false
	if out := renderPanel(t, p); strings.Contains(out, `aria-busy`) {
		t.Errorf("settled panel carries aria-busy:\n%s", out)
	}
}

// navAt is the panel's navigation at a range with no other parameters.
func navAt(key job.RangeKey) panelNav {
	q := url.Values{}
	if key != homeRanges.Default {
		q.Set("range", string(key))
	}
	return homePanelNav(q)
}

func tabURLs(tabs []RangeTab) map[string]string {
	out := map[string]string{}
	for _, tab := range tabs {
		out[tab.Label] = tab.URL
	}
	return out
}

// The range tabs keep ?at=, so switching range stays parked in history.
func TestHomePanelNav_RangeTabsKeepTheCursor(t *testing.T) {
	nav := homePanelNav(url.Values{"range": {"7d"}, "at": {"1-a-2"}})
	if nav.Range != job.Range7D {
		t.Errorf("range = %q, want 7d", nav.Range)
	}
	urls := tabURLs(nav.RangeTabs)
	if urls["1D"] != "/?at=1-a-2" || urls["All"] != "/?at=1-a-2&range=all" {
		t.Errorf("range tab URLs = %v, want each to keep at=", urls)
	}
}
