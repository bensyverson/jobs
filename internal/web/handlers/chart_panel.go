package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/chart"
)

// PanelState is which of its three faces the chart panel shows.
type PanelState string

const (
	// PanelChart draws the burn-up and the histogram.
	PanelChart PanelState = "chart"
	// PanelEmpty says nothing has been in scope in the window yet.
	PanelEmpty PanelState = "empty"
	// PanelError says the report could not be built, and why.
	PanelError PanelState = "error"
)

// ChartView is which chart the panel draws: one at a time, chosen by
// `?chart=`.
type ChartView string

const (
	// ChartBurnup is the default and is spelled by omitting `?chart=`.
	ChartBurnup ChartView = "burnup"
	// ChartActivity is the stacked activity histogram.
	ChartActivity ChartView = "activity"
)

// chartParam is the query parameter that carries a non-default
// ChartView. Mirrored by PANEL_PARAMS in assets/js/chart-panel-url.mjs.
const chartParam = "chart"

// chartViews is the toggle's options, in order.
var chartViews = []struct {
	View  ChartView
	Label string
}{
	{ChartBurnup, "Burn-up"},
	{ChartActivity, "Activity"},
}

// parseChartView normalizes a raw `?chart=` value (trimmed,
// case-insensitive); anything but a known view is the burn-up, the
// same quiet fallback as `?range=`.
func parseChartView(raw string) ChartView {
	if v := ChartView(strings.ToLower(strings.TrimSpace(raw))); v == ChartActivity {
		return v
	}
	return ChartBurnup
}

// IsActivity reports whether the panel draws the histogram rather
// than the burn-up.
func (v ChartView) IsActivity() bool { return v == ChartActivity }

// panelNav is the panel's header navigation for one request: the range
// and chart it shows and the links that change either. Each set of
// links keeps the other's parameter and `?at=`.
type panelNav struct {
	Range     job.RangeKey
	View      ChartView
	RangeTabs []RangeTab
	ViewTabs  []RangeTab
}

// homePanelNav reads `?range=` and `?chart=` off Home's query and
// builds both sets of links back to Home.
func homePanelNav(q url.Values) panelNav {
	nav := panelNav{
		Range: parseRangeKey(q.Get("range"), homeRanges),
		View:  parseChartView(q.Get(chartParam)),
	}
	nav.RangeTabs = buildRangeTabs("/", q, nav.Range, homeRanges)
	for _, opt := range chartViews {
		value := string(opt.View)
		if opt.View == ChartBurnup {
			value = ""
		}
		nav.ViewTabs = append(nav.ViewTabs, RangeTab{Label: opt.Label, URL: withParam("/", q, chartParam, value), Active: opt.View == nav.View})
	}
	return nav
}

// ChartPanel is the Home view's chart panel: the burn-up or the
// activity histogram over one range, with the chart toggle and the
// range selector. It is built from a job.Report and nothing else, so
// the page, the scrubber's fragment fetch and the preview catalog all
// render the same thing.
type ChartPanel struct {
	// ID prefixes the element ids the panel's accessible names point
	// at, so two panels can share a page (the preview catalog).
	ID      string
	State   PanelState
	Message string
	View    ChartView
	// ViewTabs is the Burn-up · Activity toggle; RangeTabs the range
	// selector. Both are plain links.
	ViewTabs    []RangeTab
	RangeTabs   []RangeTab
	Range       job.RangeKey
	RangePhrase string
	// Pending marks a fetch in flight; the element's script sets the
	// same aria-busy attribute while it swaps the panel.
	Pending  bool
	Burnup   chart.Burnup
	Activity chart.Activity
	Axis     chart.Axis
}

// rangePhrases names each window in words, for the charts' titles and
// the empty state. Under ?at= the window ends at the cursor, and "the
// last 7 days" still reads true from there.
var rangePhrases = map[job.RangeKey]string{
	job.RangeHour: "the last hour",
	job.RangeDay:  "the last day",
	job.Range7D:   "the last 7 days",
	job.Range14D:  "the last 14 days",
	job.Range30D:  "the last 30 days",
	job.RangeAll:  "all of this store's history",
}

// buildChartPanel shapes a report — or the error that stopped one —
// into the panel. loc is the calendar the axis and tables read in.
func buildChartPanel(id string, rep job.Report, repErr error, nav panelNav, loc *time.Location) ChartPanel {
	p := ChartPanel{
		ID: id, View: nav.View, ViewTabs: nav.ViewTabs, RangeTabs: nav.RangeTabs,
		Range: nav.Range, RangePhrase: rangePhrases[nav.Range],
	}
	switch {
	case repErr != nil:
		p.State = PanelError
		p.Message = "Couldn’t build the progress chart: " + repErr.Error()
		return p
	}
	p.Burnup = chart.LayoutBurnup(rep, loc)
	if p.Burnup.Empty {
		p.State = PanelEmpty
		p.Message = "Nothing has been in scope in " + p.RangePhrase + "."
		return p
	}
	p.State = PanelChart
	p.Activity = chart.LayoutActivity(rep, loc)
	p.Axis = chart.LayoutAxis(rep, loc)
	return p
}

// loadChartPanel builds Home's panel for a request's `?range=`,
// `?chart=` and `?at=`: one report whose window ends at the range
// anchor — now when live, the cursor's moment when parked in history
// (decision 10).
func loadChartPanel(ctx context.Context, deps Deps, q url.Values, now time.Time) (ChartPanel, error) {
	at, _ := parseAtParam(q)
	anchor, err := rangeAnchor(ctx, deps.DB, at, now)
	if err != nil {
		return ChartPanel{}, err
	}
	nav := homePanelNav(q)
	rg := job.NewRange(nav.Range, anchor)
	var since time.Time
	if rg.Bounded() {
		since = time.Unix(rg.Cutoff, 0)
	}
	rep, repErr := job.BuildReport(deps.DB, job.ReportQuery{Since: since, Until: anchor, Location: time.Local})
	return buildChartPanel("home", rep, repErr, nav, time.Local), nil
}

// HomePanel serves the chart panel alone — the fragment the panel's
// script swaps in when the range or chart changes or the scrubber
// moves. The
// counting stays on the server (decision 12); the script only fetches.
func HomePanel(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, invalid := parseAtParam(r.URL.Query()); invalid {
			http.Error(w, "?at must be a log position (<ts>-<replica>-<seq>)", http.StatusBadRequest)
			return
		}
		panel, err := loadChartPanel(r.Context(), deps, r.URL.Query(), time.Now())
		if err != nil {
			InternalError(deps, w, "home panel", err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := deps.Templates.RenderFragment(w, "home", "chart_panel", panel); err != nil {
			InternalError(deps, w, "render chart_panel", err)
		}
	})
}
