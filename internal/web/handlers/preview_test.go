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
