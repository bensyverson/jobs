package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/web/handlers"
)

// TestLabels_RedirectsToScopedPlanFilter is the regression test for
// /labels/{name} rendering the whole forest and ignoring the label
// (job 1v6uN): the route should behave as /plan?label=<name>, and the
// simplest correct way to do that is a redirect to the query-based
// filter that already works.
func TestLabels_RedirectsToScopedPlanFilter(t *testing.T) {
	db := setupPlanTestDB(t)
	labelled := mustAdd(t, db, "claude", "Labelled task", nil, []string{"foo"})
	unlabelled := mustAdd(t, db, "claude", "Unlabelled task", nil, nil)

	deps := newPlanDeps(t, db)

	req := httptest.NewRequest("GET", "/labels/foo", nil)
	req.SetPathValue("name", "foo")
	w := httptest.NewRecorder()
	handlers.LabelRedirect(deps).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("GET /labels/foo: status %d, want %d", w.Code, http.StatusFound)
	}
	loc := w.Header().Get("Location")
	if loc != "/plan?label=foo" {
		t.Fatalf("Location = %q, want %q", loc, "/plan?label=foo")
	}

	// Follow the redirect target and confirm it's scoped to the label:
	// only the labelled task is visible, the unlabelled one is not.
	body := fetchPlan(t, deps, "label=foo")
	if !containsShortID(body, labelled) {
		t.Errorf("expected labelled task %q visible at redirect target", labelled)
	}
	if containsShortID(body, unlabelled) {
		t.Errorf("expected unlabelled task %q absent at redirect target", unlabelled)
	}
}

// TestLabels_EscapesNameInRedirect confirms a label name with query-
// unsafe characters survives the round-trip the same way planURL
// already escapes selected labels.
func TestLabels_EscapesNameInRedirect(t *testing.T) {
	db := setupPlanTestDB(t)
	deps := newPlanDeps(t, db)

	req := httptest.NewRequest("GET", "/labels/a%20b", nil)
	req.SetPathValue("name", "a b")
	w := httptest.NewRecorder()
	handlers.LabelRedirect(deps).ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/plan?label=a+b" {
		t.Fatalf("Location = %q, want %q", loc, "/plan?label=a+b")
	}
}

// TestLabels_EmptyNameIs404 guards the degenerate path-value case; a
// bare /labels/ shouldn't redirect into an unfiltered /plan.
func TestLabels_EmptyNameIs404(t *testing.T) {
	db := setupPlanTestDB(t)
	deps := newPlanDeps(t, db)

	req := httptest.NewRequest("GET", "/labels/", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()
	handlers.LabelRedirect(deps).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want %d", w.Code, http.StatusNotFound)
	}
}

func containsShortID(body, shortID string) bool {
	return strings.Contains(body, `id="task-`+shortID+`"`)
}
