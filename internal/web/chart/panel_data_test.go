package chart

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// panelDataReport is a four-hour window with a trace, two buckets, one
// import inside the window and one before it.
func panelDataReport() job.Report {
	rep := hourly(4, func(i int) job.Sample {
		return job.Sample{Scope: 20 + i, Done: 3 * i, Blocked: i % 2, Canceled: 5 + i}
	})
	rep.Activity = []job.ActivityCount{
		{Start: t0, End: t0.Add(2 * time.Hour), Created: 3, Claimed: 2, Done: 4, Blocked: 1},
		{Start: t0.Add(2 * time.Hour), End: t0.Add(4 * time.Hour), Created: 1, Done: 5},
	}
	rep.Imports = []job.ImportMarker{
		{At: t0.Add(-time.Hour), TaskID: "early", Title: "Before the window"},
		{At: t0.Add(90 * time.Minute), TaskID: "abc12", Title: "Reporting", Source: "reporting.md"},
	}
	rep.Leaves = job.LeafFigures{Created: 8, Done: 12, Canceled: 4, Open: 12, Blocked: 0}
	return rep
}

func TestLayoutPanelData_CarriesTheWindowInMilliseconds(t *testing.T) {
	rep := panelDataReport()
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	if d.Since != t0.UnixMilli() || d.Until != t0.Add(4*time.Hour).UnixMilli() {
		t.Errorf("since/until = %d/%d, want %d/%d", d.Since, d.Until, t0.UnixMilli(), t0.Add(4*time.Hour).UnixMilli())
	}
	if d.Window != (WindowFigures{Created: 8, Done: 12, Canceled: 4}) {
		t.Errorf("window figures = %+v", d.Window)
	}
}

// The y domain is the one the server drew with, so a client redraw
// lines up with the server's gridlines.
func TestLayoutPanelData_CarriesTheBurnupDomain(t *testing.T) {
	rep := panelDataReport()
	b := LayoutBurnup(rep, time.UTC)
	d := LayoutPanelData(rep, b, LayoutActivity(rep, time.UTC))
	if d.Y.Lo != b.dom.lo || d.Y.Hi != b.dom.hi {
		t.Errorf("Y = %+v, want the burn-up's domain %v..%v", d.Y, b.dom.lo, b.dom.hi)
	}
	// Scope 24 + 4 canceled in the window at Until.
	if d.Y.Hi != 28 {
		t.Errorf("Y.Hi = %v, want 28 (the band's top)", d.Y.Hi)
	}
}

// Each trace sample carries the state and the canceled-in-window count
// the band draws, so the client never re-derives it.
func TestLayoutPanelData_TraceCarriesCanceledInWindow(t *testing.T) {
	rep := panelDataReport()
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	if len(d.Trace) != 5 {
		t.Fatalf("trace = %d points, want 5", len(d.Trace))
	}
	want := TracePoint{T: t0.Add(3 * time.Hour).UnixMilli(), Scope: 23, Done: 9, Open: 14, Blocked: 1, CanceledInWindow: 3}
	if d.Trace[3] != want {
		t.Errorf("trace[3] = %+v, want %+v", d.Trace[3], want)
	}
	if d.Trace[0].CanceledInWindow != 0 {
		t.Errorf("trace[0] canceled in window = %d, want 0 at Since", d.Trace[0].CanceledInWindow)
	}
}

func TestLayoutPanelData_CarriesBucketsAndThePeak(t *testing.T) {
	rep := panelDataReport()
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	want := []BucketPoint{
		{Start: t0.UnixMilli(), End: t0.Add(2 * time.Hour).UnixMilli(), Created: 3, Claimed: 2, Done: 4, Blocked: 1},
		{Start: t0.Add(2 * time.Hour).UnixMilli(), End: t0.Add(4 * time.Hour).UnixMilli(), Created: 1, Done: 5},
	}
	if !reflect.DeepEqual(d.Buckets, want) {
		t.Errorf("buckets = %+v, want %+v", d.Buckets, want)
	}
	if d.Peak != 10 {
		t.Errorf("peak = %d, want the busiest bucket's 10", d.Peak)
	}
}

// Imports carry the same link and name the axis draws, and only those
// in the window.
func TestLayoutPanelData_CarriesImportsInTheWindow(t *testing.T) {
	rep := panelDataReport()
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	want := []ImportPoint{{T: t0.Add(90 * time.Minute).UnixMilli(), Href: "/tasks/abc12", Label: "Imported Reporting from reporting.md"}}
	if !reflect.DeepEqual(d.Imports, want) {
		t.Errorf("imports = %+v, want %+v", d.Imports, want)
	}
}

// The wire names are the contract with the enhancement script.
func TestPanelData_JSONNames(t *testing.T) {
	rep := panelDataReport()
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"since":`, `"until":`, `"y":{"lo":`, `"hi":`, `"trace":[{"t":`, `"scope":`, `"done":`, `"open":`,
		`"blocked":`, `"canceledInWindow":`, `"buckets":[{"start":`, `"end":`, `"created":`, `"claimed":`,
		`"peak":`, `"imports":[{"t":`, `"href":`, `"label":`, `"window":{"created":`, `"canceled":`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("JSON lacks %s:\n%s", key, raw)
		}
	}
	var back PanelData
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, d) {
		t.Errorf("round trip = %+v (err %v), want %+v", back, err, d)
	}
}

// Empty slices encode as [], not null, so the script can iterate them
// unguarded.
func TestPanelData_EmptyListsAreArrays(t *testing.T) {
	rep := hourly(2, func(i int) job.Sample { return job.Sample{Scope: 3, Done: i} })
	d := LayoutPanelData(rep, LayoutBurnup(rep, time.UTC), LayoutActivity(rep, time.UTC))
	raw, _ := json.Marshal(d)
	if !strings.Contains(string(raw), `"imports":[]`) || !strings.Contains(string(raw), `"buckets":[]`) {
		t.Errorf("JSON = %s, want empty imports and buckets as []", raw)
	}
}
