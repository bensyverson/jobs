package handlers

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
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

// panelNav is the panel's header navigation for one request: the range
// it shows and the links that change it. Each link keeps `?at=`.
type panelNav struct {
	Range     job.RangeKey
	RangeTabs []RangeTab
}

// homePanelNav reads `?range=` off Home's query and builds the range
// links back to Home.
func homePanelNav(q url.Values) panelNav {
	nav := panelNav{Range: parseRangeKey(q.Get("range"), homeRanges)}
	nav.RangeTabs = buildRangeTabs("/", q, nav.Range, homeRanges)
	return nav
}

// ChartPanel is the Home view's chart panel: the burn-up and the
// activity histogram side by side over one range, each over its own
// time axis, under the range selector. It is built from a job.Report
// and nothing else, so the page, the scrubber's fragment fetch and the
// preview catalog all render the same thing.
type ChartPanel struct {
	// ID prefixes the element ids the panel's accessible names point
	// at, so two panels can share a page (the preview catalog).
	ID      string
	State   PanelState
	Message string
	// RangeTabs is the range selector: plain links.
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

// ChartKind names one of the panel's two charts. Its value is the CSS
// modifier each chart's column and axis take.
type ChartKind string

const (
	// ChartBurnup is the burn-up, first in reading order.
	ChartBurnup ChartKind = "burnup"
	// ChartActivity is the stacked activity histogram.
	ChartActivity ChartKind = "activity"
)

// PanelAxis is one chart's copy of the panel's time axis: both charts
// share a window, so the geometry is the same.
type PanelAxis struct {
	chart.Axis
	Chart ChartKind
}

// Reachable reports whether assistive tech and the keyboard reach this
// copy's import links. Only the burn-up's are, so each imported plan is
// announced and tabbed to once; the histogram's copy is for the pointer.
func (a PanelAxis) Reachable() bool { return a.Chart == ChartBurnup }

// BurnupAxis is the axis under the burn-up.
func (p ChartPanel) BurnupAxis() PanelAxis { return PanelAxis{Axis: p.Axis, Chart: ChartBurnup} }

// ActivityAxis is the axis under the histogram.
func (p ChartPanel) ActivityAxis() PanelAxis {
	return PanelAxis{Axis: p.Axis, Chart: ChartActivity}
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

// canceledPhrases scope the caption's canceled count to the window, so
// it is not read as a running total. All's window is the whole store.
var canceledPhrases = map[job.RangeKey]string{
	job.RangeHour: "in the last hour",
	job.RangeDay:  "in the last day",
	job.Range7D:   "in the last 7 days",
	job.Range14D:  "in the last 14 days",
	job.Range30D:  "in the last 30 days",
	job.RangeAll:  "overall",
}

// Caption is the header's one line: the open work at the window's end
// — the gap and its blocked share are drawn, so they are also said —
// and, when there is some, the leaves canceled in the window
// (decision 4), worded as the window's.
func (p ChartPanel) Caption() string {
	b := p.Burnup
	s := chart.Count(b.Open) + " open · " + chart.Count(b.Blocked) + " blocked"
	if b.Canceled > 0 {
		s += " · " + chart.Count(b.Canceled) + " canceled " + canceledPhrases[p.Range]
	}
	return s
}

// buildChartPanel shapes a report — or the error that stopped one —
// into the panel. end is what the window's right edge is, which only
// the caller knows (live, or parked at the scrubber's cursor); loc is
// the calendar the axis and tables read in.
func buildChartPanel(id string, rep job.Report, repErr error, nav panelNav, end chart.WindowEnd, loc *time.Location) ChartPanel {
	p := ChartPanel{
		ID: id, RangeTabs: nav.RangeTabs,
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
	p.Axis = chart.LayoutAxis(rep, loc, end)
	return p
}

// loadChartPanel builds Home's panel for a request's `?range=` and
// `?at=`: one report whose window ends at the range anchor — now when
// live, the cursor's moment when parked in history (decision 10).
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
	end := chart.EndsNow
	if at != (eventlog.Position{}) {
		end = chart.EndsAtCursor
	}
	return buildChartPanel("home", rep, repErr, nav, end, time.Local), nil
}

// HomePanel serves the chart panel alone — the fragment the panel's
// script swaps in when the range changes or the scrubber moves. The
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
