package chart

import job "github.com/bensyverson/jobs/internal/job"

// PanelData is what the chart panel's enhancement script needs to
// redraw and hover both charts (chart panel revision, decision 9),
// shipped in the fragment as a JSON island. It carries server-counted
// samples and the domain the server drew with, so the script re-derives
// no count (reporting decision 12). Times are Unix milliseconds, which
// JavaScript's Date takes as is.
//
// This struct is the wire contract: the script reads these names, and
// DESIGN.md's chart panel spec documents them.
type PanelData struct {
	// Since and Until are the window's edges: x runs Since → Until.
	Since int64 `json:"since"`
	Until int64 `json:"until"`
	// Zone is the IANA name of the calendar the axis and tables read
	// in (the report window's Timezone), so the tooltip names moments
	// as the axis does rather than in the browser's zone.
	Zone string `json:"zone"`
	// Y is the burn-up's vertical domain as drawn: Lo on the baseline,
	// Hi at the top edge.
	Y YDomain `json:"y"`
	// Trace is the burn-up at drawing resolution, the first sample at
	// Since and the last at Until.
	Trace []TracePoint `json:"trace"`
	// Buckets are the histogram's bars, oldest first, and Peak the
	// busiest one's total — the bars' full height.
	Buckets []BucketPoint `json:"buckets"`
	Peak    int           `json:"peak"`
	// Imports are the imported plans in the window, as the axis links
	// them.
	Imports []ImportPoint `json:"imports"`
	// Window is the window's transitions — the end labels' figures and
	// the caption's canceled count.
	Window WindowFigures `json:"window"`
}

// YDomain is a vertical extent in leaves.
type YDomain struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// TracePoint is the state as of T. CanceledInWindow is leaves canceled
// since the window began — the canceled band's height above Scope — not
// the store's running canceled total.
type TracePoint struct {
	T                int64 `json:"t"`
	Scope            int   `json:"scope"`
	Done             int   `json:"done"`
	Open             int   `json:"open"`
	Blocked          int   `json:"blocked"`
	CanceledInWindow int   `json:"canceledInWindow"`
}

// BucketPoint is one histogram bucket [Start, End) and its events.
type BucketPoint struct {
	Start   int64 `json:"start"`
	End     int64 `json:"end"`
	Created int   `json:"created"`
	Claimed int   `json:"claimed"`
	Done    int   `json:"done"`
	Blocked int   `json:"blocked"`
}

// ImportPoint is one imported plan: when, its task page, and its name.
type ImportPoint struct {
	T     int64  `json:"t"`
	Href  string `json:"href"`
	Label string `json:"label"`
}

// WindowFigures are leaves created, done and canceled in the window.
type WindowFigures struct {
	Created  int `json:"created"`
	Done     int `json:"done"`
	Canceled int `json:"canceled"`
}

// LayoutPanelData gathers the island from rep and the two charts laid
// out from it, so the numbers the script draws are the ones the server
// drew. Lists are never nil, so they encode as [].
func LayoutPanelData(rep job.Report, b Burnup, a Activity) PanelData {
	d := PanelData{
		Since:   rep.Window.Since.UnixMilli(),
		Until:   rep.Window.Until.UnixMilli(),
		Zone:    rep.Window.Timezone,
		Y:       YDomain{Lo: b.dom.lo, Hi: b.dom.hi},
		Trace:   make([]TracePoint, 0, len(b.trace)),
		Buckets: make([]BucketPoint, 0, len(rep.Activity)),
		Peak:    a.Peak,
		Imports: []ImportPoint{},
		Window:  WindowFigures{Created: rep.Leaves.Created, Done: rep.Leaves.Done, Canceled: rep.Leaves.Canceled},
	}
	for _, s := range b.trace {
		d.Trace = append(d.Trace, TracePoint{
			T: s.End.UnixMilli(), Scope: s.Scope, Done: s.Done, Open: s.Open, Blocked: s.Blocked,
			CanceledInWindow: s.canceledInWindow,
		})
	}
	for _, c := range rep.Activity {
		d.Buckets = append(d.Buckets, BucketPoint{
			Start: c.Start.UnixMilli(), End: c.End.UnixMilli(),
			Created: c.Created, Claimed: c.Claimed, Done: c.Done, Blocked: c.Blocked,
		})
	}
	for _, m := range importsIn(rep) {
		d.Imports = append(d.Imports, ImportPoint{T: m.at.UnixMilli(), Href: m.href, Label: m.label})
	}
	return d
}
