package handlers_test

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/handlers"
)

// POST /home/graph: scrubber's debounced graph refetch lands here.
// JS sends {tasks, blocks} from the cursor's frame, server runs the
// same Subway core /home runs, returns the c-mini-graph fragment.

func postHomeGraph(t *testing.T, deps handlers.Deps, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/home/graph", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handlers.HomeGraph(deps).ServeHTTP(w, req)
	return w
}

func TestHomeGraph_EmptyInputRendersEmptyState(t *testing.T) {
	deps := newLogDeps(t, setupLogTestDB(t))
	w := postHomeGraph(t, deps, `{"tasks":[],"blocks":[]}`)
	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "c-mini-graph") {
		t.Errorf("missing c-mini-graph wrapper; body=%s", body)
	}
	if !strings.Contains(body, "No active or upcoming work.") {
		t.Errorf("missing empty state; body=%s", body)
	}
	if strings.Contains(body, "c-graph-canvas") {
		t.Errorf("empty input should not render canvas; body=%s", body)
	}
}

func TestHomeGraph_ClaimedTaskRendersCanvas(t *testing.T) {
	deps := newLogDeps(t, setupLogTestDB(t))
	// Minimal graph: parent ph3 with three children, st2 claimed by alice.
	body := `{
		"tasks":[
			{"shortId":"ph3","title":"Phase 3","status":"available","sortKey":"000002"},
			{"shortId":"st1","title":"Step 1","status":"done","parentShortId":"ph3","sortKey":"000001"},
			{"shortId":"st2","title":"Step 2","status":"claimed","parentShortId":"ph3","sortKey":"000002","claimedBy":"alice"},
			{"shortId":"st3","title":"Step 3","status":"available","parentShortId":"ph3","sortKey":"000003"}
		],
		"blocks":[]
	}`
	w := postHomeGraph(t, deps, body)
	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	resp := w.Body.String()
	for _, want := range []string{
		`c-graph-canvas`,
		`c-graph-node--active`,
		`data-task-id="st2"`,
		`data-actor="alice"`,
		`c-graph-edge`,
	} {
		if !strings.Contains(resp, want) {
			t.Errorf("missing %q in fragment; body=%s", want, resp)
		}
	}
}

// The graph renders inside an intrinsically-sized inner canvas
// wrapped by a frame that scales the whole content down via CSS
// when its parent is narrower than CanvasW. Two pieces have to be
// in the markup for the scaling to work: a c-graph-frame element
// carrying --canvas-w/--canvas-h custom properties, and an SVG
// sized at its intrinsic width/height (not width="100%").
func TestHomeGraph_ScalableFrameAndIntrinsicSVG(t *testing.T) {
	deps := newLogDeps(t, setupLogTestDB(t))
	body := `{
		"tasks":[
			{"shortId":"ph3","title":"Phase 3","status":"available","sortKey":"000002"},
			{"shortId":"st1","title":"Step 1","status":"done","parentShortId":"ph3","sortKey":"000001"},
			{"shortId":"st2","title":"Step 2","status":"claimed","parentShortId":"ph3","sortKey":"000002","claimedBy":"alice"},
			{"shortId":"st3","title":"Step 3","status":"available","parentShortId":"ph3","sortKey":"000003"}
		],
		"blocks":[]
	}`
	w := postHomeGraph(t, deps, body)
	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200; body=%s", w.Code, w.Body.String())
	}
	resp := w.Body.String()

	if !strings.Contains(resp, `c-graph-frame`) {
		t.Errorf("missing c-graph-frame wrapper; body=%s", resp)
	}
	if !strings.Contains(resp, `--canvas-w:`) || !strings.Contains(resp, `--canvas-h:`) {
		t.Errorf("missing --canvas-w/--canvas-h custom properties; body=%s", resp)
	}
	if strings.Contains(resp, `<svg viewBox=`) && strings.Contains(resp, `width="100%"`) {
		t.Errorf("graph SVG should use intrinsic width (CanvasW px), not width=\"100%%\"; body=%s", resp)
	}
}

func TestHomeGraph_BadJSONReturns400(t *testing.T) {
	deps := newLogDeps(t, setupLogTestDB(t))
	w := postHomeGraph(t, deps, `{not json`)
	if w.Code != 400 {
		t.Fatalf("status: got %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestHomeGraph_RejectsNonPOST(t *testing.T) {
	deps := newLogDeps(t, setupLogTestDB(t))
	req := httptest.NewRequest("GET", "/home/graph", nil)
	w := httptest.NewRecorder()
	handlers.HomeGraph(deps).ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatalf("status: got %d, want 405; body=%s", w.Code, w.Body.String())
	}
}
