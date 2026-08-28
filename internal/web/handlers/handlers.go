// Package handlers holds one file per view in the Jobs web dashboard.
// Each file exports a constructor that takes shared Deps and returns an
// http.Handler. Keeping views in sibling files (home.go, plan.go, …)
// avoids one giant file as the dashboard grows.
package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"net/http"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/broadcast"
	"github.com/bensyverson/jobs/internal/web/initial"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// Deps is the shared bundle every handler constructor accepts. Add new
// fields here (clock, …) as they arrive; handlers should depend on
// this struct rather than package globals.
type Deps struct {
	DB          *sql.DB
	Templates   *templates.Engine
	Broadcaster *broadcast.Broadcaster
}

// renderPage is the common path for a view that renders its page
// template through the shared chrome. Sets Content-Type; on template
// failure, surfaces a styled 500 page rather than a naked plaintext
// error so the user lands somewhere that matches the rest of the UI.
func renderPage(deps Deps, w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := deps.Templates.Render(w, page, data); err != nil {
		InternalError(deps, w, "render "+page, err)
	}
}

// newChrome builds a Chrome bag for a layout-rendering page. Loads
// the JSON island from the DB so the time-travel scrubber's JS can
// hydrate without an extra fetch, and the always-now footer metric
// values from current state. Fragment endpoints that don't use the
// layout (e.g. /tasks/{id}/peek) construct a Chrome literal directly
// without calling this — they don't need the island or the footer.
func newChrome(ctx context.Context, deps Deps, activeTab string, now time.Time) (templates.Chrome, error) {
	raw, err := initial.LoadJSON(ctx, deps.DB)
	if err != nil {
		return templates.Chrome{}, err
	}
	metrics, err := LoadFooterMetrics(ctx, deps.DB, now)
	if err != nil {
		return templates.Chrome{}, err
	}
	issues, err := job.CountIssueTasks(deps.DB, now.Add(-metaWindow))
	if err != nil {
		return templates.Chrome{}, err
	}
	return templates.Chrome{
		ActiveTab:        activeTab,
		IssuesOpen:       issues.Open,
		InitialFrameJSON: template.JS(raw),
		Footer:           metrics,
	}, nil
}
