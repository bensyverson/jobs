package handlers

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// Home offers every range key, shortest first: 1H keeps the old
// one-minute live histogram reachable (decision 10).
func TestHomeRanges_OfferEveryKey(t *testing.T) {
	wantKeys := job.RangeKeys()
	wantLabels := []string{"1H", "1D", "7D", "14D", "30D", "All"}
	if len(homeRanges) != len(wantKeys) {
		t.Fatalf("homeRanges has %d options, want %d", len(homeRanges), len(wantKeys))
	}
	for i, opt := range homeRanges {
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
	return job.Report{
		Schema: job.ReportSchema,
		Window: job.ReportWindow{Since: since, Until: panelUntil, Bucket: job.BucketDay, Timezone: "UTC"},
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
}

func TestBuildChartPanel_OtherErrorsNameTheCause(t *testing.T) {
	p := buildChartPanel("home", job.Report{}, errors.New("disk on fire"), job.Range7D, nil, time.UTC)
	if p.State != PanelError || !strings.Contains(p.Message, "disk on fire") {
		t.Errorf("State %q Message %q, want the error state naming the cause", p.State, p.Message)
	}
}

func TestBuildChartPanel_EmptyReportIsTheEmptyState(t *testing.T) {
	p := buildChartPanel("home", job.Report{Window: panelReport().Window}, nil, job.Range7D, nil, time.UTC)
	if p.State != PanelEmpty {
		t.Fatalf("State = %q, want %q", p.State, PanelEmpty)
	}
	if !strings.Contains(p.Message, "last 7 days") {
		t.Errorf("Message = %q, want it to name the range", p.Message)
	}
}

func TestBuildChartPanel_LaysOutTheReport(t *testing.T) {
	p := buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC)
	if p.State != PanelChart {
		t.Fatalf("State = %q, want %q", p.State, PanelChart)
	}
	if p.Burnup.Scope.Text != "1,155" || p.Activity.Total != 36 || len(p.Axis.Imports) != 1 {
		t.Errorf("panel = scope %q, activity %d, imports %d", p.Burnup.Scope.Text, p.Activity.Total, len(p.Axis.Imports))
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
	tabs := buildRangeTabs("/", nil, job.Range7D, homeRanges)
	out := renderPanel(t, buildChartPanel("home", panelReport(), nil, job.Range7D, tabs, time.UTC))
	mustHave(t, out,
		`<chart-panel`, `data-home-panel`,
		`role="img"`, `aria-labelledby="home-burnup-title home-burnup-desc"`,
		`<title id="home-burnup-title">`, `<desc id="home-burnup-desc">`,
		`1,155 in scope, 1,080 done`,
		`class="c-burnup__scope"`, `class="c-burnup__done"`, `class="c-burnup__gap"`, `class="c-burnup__blocked"`,
		`vector-effect="non-scaling-stroke"`,
		`>1,155<`, `>scope<`, `>1,080<`, `>done<`,
		`75 open`, `12 blocked`, `4 canceled`,
	)
}

func TestChartPanelTemplate_ShipsDataTablesForAssistiveTech(t *testing.T) {
	out := renderPanel(t, buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC))
	tables := regexp.MustCompile(`<table class="sr-only"`).FindAllStringIndex(out, -1)
	if len(tables) != 2 {
		t.Fatalf("sr-only tables = %d, want 2 (burn-up and activity)", len(tables))
	}
	mustHave(t, out, `<td>2026-09-26 17:00</td>`, `<td>1155</td>`)
}

func TestChartPanelTemplate_ImportTicksCarryTheirPlan(t *testing.T) {
	out := renderPanel(t, buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC))
	mustHave(t, out, `class="c-chart-axis__import"`, `<title>Imported Reporting from reporting.md</title>`, `1 imported</li>`)
}

func TestChartPanelTemplate_HistogramStacksByKind(t *testing.T) {
	out := renderPanel(t, buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC))
	mustHave(t, out,
		`class="c-activity__seg c-activity__seg--done"`,
		`class="c-activity__seg c-activity__seg--created"`,
		`20 created`, `3 claimed`, `12 done`, `1 blocked`,
	)
}

// The web rules forbid inline style attributes; the panel positions
// everything with SVG attributes and classes.
func TestChartPanelTemplate_HasNoInlineStyles(t *testing.T) {
	for _, p := range []ChartPanel{
		buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC),
		buildChartPanel("home", job.Report{}, errors.New("disk on fire"), job.Range7D, nil, time.UTC),
		buildChartPanel("home", job.Report{}, nil, job.Range7D, nil, time.UTC),
	} {
		if out := renderPanel(t, p); strings.Contains(out, "style=") {
			t.Errorf("state %q renders an inline style:\n%s", p.State, out)
		}
	}
}

func TestChartPanelTemplate_ErrorStateKeepsTheSelector(t *testing.T) {
	tabs := buildRangeTabs("/", nil, job.RangeDay, homeRanges)
	out := renderPanel(t, buildChartPanel("home", job.Report{}, errors.New("disk on fire"), job.RangeDay, tabs, time.UTC))
	mustHave(t, out, `disk on fire`, `>1H<`, `>All<`, `aria-current="true">1D<`)
	if strings.Contains(out, `role="img"`) {
		t.Errorf("error state draws a chart:\n%s", out)
	}
}

func TestChartPanelTemplate_PendingSetsAriaBusy(t *testing.T) {
	p := buildChartPanel("home", panelReport(), nil, job.Range7D, nil, time.UTC)
	p.Pending = true
	mustHave(t, renderPanel(t, p), `aria-busy="true"`)
	p.Pending = false
	if out := renderPanel(t, p); strings.Contains(out, `aria-busy`) {
		t.Errorf("settled panel carries aria-busy:\n%s", out)
	}
}
