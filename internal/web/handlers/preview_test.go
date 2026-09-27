package handlers_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/handlers"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// previewDeps has no database: the catalog is zero-config and renders
// from constructed view-model payloads alone.
func previewDeps(t *testing.T) handlers.Deps {
	t.Helper()
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	return handlers.Deps{Templates: e}
}

func fetchPreview(t *testing.T, component, state string) (int, string) {
	t.Helper()
	path := "/preview"
	if component != "" {
		path += "/" + component
	}
	if state != "" {
		path += "/" + state
	}
	req := httptest.NewRequest("GET", path, nil)
	req.SetPathValue("component", component)
	req.SetPathValue("state", state)
	w := httptest.NewRecorder()
	handlers.Preview(previewDeps(t)).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

var chartPanelStates = []string{"empty", "hour", "single-day", "imports", "one-sample", "reopen-dip", "fitted-week", "flat", "crowded", "mostly-canceled", "parked", "fetching", "error"}

func TestPreviewIndex_ListsTheChartPanel(t *testing.T) {
	code, body := fetchPreview(t, "", "")
	if code != 200 {
		t.Fatalf("GET /preview: status %d\n%s", code, body)
	}
	mustContainAll(t, body, `href="/preview/chart-panel"`, "Chart panel")
	for _, s := range chartPanelStates {
		mustContain(t, body, `href="/preview/chart-panel/`+s+`"`)
	}
}

// The component page stacks every state under its note, through the
// real page shell.
func TestPreviewComponent_StacksEveryStateWithItsNote(t *testing.T) {
	code, body := fetchPreview(t, "chart-panel", "")
	if code != 200 {
		t.Fatalf("status %d\n%s", code, body)
	}
	mustContainAll(t, body, "<!doctype html>", `class="c-header`, "internal/web/templates/html/partials/chart_panel.html.tmpl")
	if n := strings.Count(body, "<chart-panel"); n != len(chartPanelStates) {
		t.Errorf("rendered %d panels, want %d", n, len(chartPanelStates))
	}
	for _, e := range handlers.PreviewIndex() {
		for _, s := range e.States {
			if s.Note == "" {
				t.Errorf("state %s/%s has no note", e.Component, s.Slug)
			}
			mustContain(t, body, s.Note)
		}
	}
}

func TestPreviewState_RendersOneStateWhole(t *testing.T) {
	code, body := fetchPreview(t, "chart-panel", "error")
	if code != 200 {
		t.Fatalf("status %d\n%s", code, body)
	}
	if n := strings.Count(body, "<chart-panel"); n != 1 {
		t.Errorf("rendered %d panels, want 1", n)
	}
	mustContain(t, body, "database is locked")
}

func TestPreviewState_CrowdedHistoryDrawsImports(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "crowded")
	if n := strings.Count(body, `class="c-chart-axis__import"`); n < 10 {
		t.Errorf("crowded state draws %d import ticks, want many", n)
	}
}

// The imports state puts several plans in one day, two of them close
// together, each a link the peek sheet opens.
func TestPreviewState_ImportsAreLinks(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "imports")
	if n := strings.Count(body, `class="c-chart-axis__import-link"`); n < 6 {
		t.Errorf("imports state draws %d import links, want at least three on each axis", n)
	}
	mustContain(t, body, `data-peek aria-label="Imported `)
}

// The parked state is the panel rendered for ?at=: the axis ends at the
// cursor's moment, not "Now", and the range tabs keep the cursor.
func TestPreviewState_ParkedEndsAtTheCursor(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "parked")
	mustContainAll(t, body, `c-chart-axis__label--end`, `at=`)
	if strings.Contains(body, `>Now</text>`) {
		t.Errorf("parked state labels its end Now")
	}
}

// The catalog's reports carry a trace as BuildReport's do, so the
// burn-up is drawn from it in preview exactly as on Home: a week at
// half-hour steps, not 28 six-hour points.
func TestPreviewState_BurnupIsDrawnFromATrace(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "fitted-week")
	d := panelIsland(t, body)
	if n := len(d.Trace); n < 300 {
		t.Errorf("fitted-week trace = %d samples, want a sample every 30 minutes (~337)", n)
	}
	if d.Trace[0].T != d.Since || d.Trace[len(d.Trace)-1].T != d.Until {
		t.Errorf("trace runs %d..%d, want Since %d..Until %d", d.Trace[0].T, d.Trace[len(d.Trace)-1].T, d.Since, d.Until)
	}
}

// Decision 6: mostly-canceled cancels mid-window, so its band starts
// at zero at Since and grows.
func TestPreviewState_MostlyCanceledDrawsTheBand(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "mostly-canceled")
	mustContain(t, body, `class="c-burnup__canceled"`)
	d := panelIsland(t, body)
	if first, last := d.Trace[0].CanceledInWindow, d.Trace[len(d.Trace)-1].CanceledInWindow; first != 0 || last < 40 {
		t.Errorf("canceled in window runs %d → %d, want 0 at Since growing past 40", first, last)
	}
}

// Decision 10: the histogram counts what the end labels count, so in
// every drawn state the buckets' created and done sum to the window's
// figures — the fixtures derive one from the other, as a real report
// does (crowded once read "+1,193 created" beside "1161 created").
func TestPreviewStates_HistogramSumsToTheWindowFigures(t *testing.T) {
	for _, s := range chartPanelStates {
		_, body := fetchPreview(t, "chart-panel", s)
		if !strings.Contains(body, "c-chart-panel__data") {
			continue // empty and error draw no charts
		}
		d := panelIsland(t, body)
		created, done := 0, 0
		for _, b := range d.Buckets {
			created += b.Created
			done += b.Done
		}
		if created != d.Window.Created || done != d.Window.Done {
			t.Errorf("%s: buckets sum to %d created / %d done, window figures %d / %d",
				s, created, done, d.Window.Created, d.Window.Done)
		}
	}
}

func TestPreviewState_FetchingIsBusy(t *testing.T) {
	_, body := fetchPreview(t, "chart-panel", "fetching")
	mustContain(t, body, `aria-busy="true"`)
}

func TestPreview_UnknownComponentOrStateIs404(t *testing.T) {
	if code, _ := fetchPreview(t, "nope", ""); code != 404 {
		t.Errorf("unknown component: status %d, want 404", code)
	}
	if code, _ := fetchPreview(t, "chart-panel", "nope"); code != 404 {
		t.Errorf("unknown state: status %d, want 404", code)
	}
}

// PreviewIndex is the --list --json payload: every state with its URL.
func TestPreviewIndex_NamesEveryStateURL(t *testing.T) {
	idx := handlers.PreviewIndex()
	if len(idx) == 0 {
		t.Fatal("empty catalog")
	}
	var got []string
	for _, e := range idx {
		if e.Component == "chart-panel" {
			for _, s := range e.States {
				got = append(got, s.Slug)
				if s.URL != "/preview/chart-panel/"+s.Slug {
					t.Errorf("state %s URL = %q", s.Slug, s.URL)
				}
			}
		}
	}
	if strings.Join(got, ",") != strings.Join(chartPanelStates, ",") {
		t.Errorf("chart-panel states = %v, want %v", got, chartPanelStates)
	}
}
