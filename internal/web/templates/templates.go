// Package templates owns the html/template pipeline for the dashboard.
// It embeds the layout, partials, and per-view page templates, wires
// the asset manifest into the FuncMap as {{asset}}, and exposes an
// [Engine] that handlers use to render pages.
//
// Organization under html/:
//
//	layout.html.tmpl         the page frame (<html>, <head>, <main>)
//	partials/*.html.tmpl     chrome shared by every view (header, footer)
//	pages/*.html.tmpl        one per view, defining the "content" block
package templates

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
)

//go:embed all:html
var files embed.FS

// Chrome carries the fields every page needs for the shared header
// and footer. Page-specific data structs embed it so templates can
// read {{.ActiveTab}} uniformly.
type Chrome struct {
	// ActiveTab marks which top-nav tab renders as c-tab--active.
	// Valid values: "home", "plan", "issues", "actors", "log", or
	// empty for views that aren't a top-level tab (e.g. /tasks/<id>).
	ActiveTab string
	// IssuesOpen is the number of open tasks under every issue-tree
	// root, rendered as a small count after the Issues tab's label.
	// Zero renders no suffix — a standing "0" is the tracker chrome
	// the design notes warn against. Reflects the page load, not the
	// live stream: the header is not swapped by the live modules.
	IssuesOpen int
	// InitialFrameJSON is the head-frame snapshot the time-travel
	// scrubber's JS bootstrap reads from a <script type="application/
	// json" id="initial-frame"> island in the layout. Already encoded
	// HTML-safe by internal/web/initial.LoadJSON; the layout pastes
	// it through with template.JS to skip html/template's auto-
	// escaping inside the <script> context. Empty / nil means "no
	// island" — the layout omits the script tag entirely. Fragment
	// renders (e.g. /tasks/{id}/peek) leave it nil.
	InitialFrameJSON template.JS
	// Footer carries the four always-now metric values rendered in
	// the footer strip. Stays current under time travel — the
	// scrubber reshapes the views above the footer, but the footer
	// is a system status bar showing the present.
	Footer FooterMetrics
}

// FooterMetrics holds the four numeric values rendered in the footer
// strip: distinct holders of currently-live claims, count of open
// tasks, rolling-60-minute events-per-minute, and done-events-in-the-
// last-hour. All values reflect "now" — never the time-travel cursor.
type FooterMetrics struct {
	Actors       int
	WIP          int
	EventsPerMin int
	TasksPerHour int
}

// Engine holds one compiled *template.Template per page. Each is a
// clone of a shared base (layout + partials) with the page's content
// parsed on top, so pages never collide on the "content" block name
// and the base is parsed once.
type Engine struct {
	pages map[string]*template.Template
}

// New parses every page under html/pages/ with layout and partials,
// wiring the asset manifest into the FuncMap so templates can write
// {{asset "css/dashboard.css"}} and get a fingerprinted URL.
func New(manifest *assets.Manifest) (*Engine, error) {
	funcs := buildFuncMap(manifest)

	base, err := template.New("base").Funcs(funcs).ParseFS(files,
		"html/layout.html.tmpl",
		"html/partials/*.html.tmpl",
	)
	if err != nil {
		return nil, fmt.Errorf("parse layout + partials: %w", err)
	}

	pageFiles, err := fs.Glob(files, "html/pages/*.html.tmpl")
	if err != nil {
		return nil, fmt.Errorf("glob pages: %w", err)
	}
	if len(pageFiles) == 0 {
		return nil, fmt.Errorf("no page templates found under html/pages/")
	}

	pages := make(map[string]*template.Template, len(pageFiles))
	for _, pf := range pageFiles {
		name := strings.TrimSuffix(path.Base(pf), ".html.tmpl")
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone base for %s: %w", name, err)
		}
		t, err := clone.ParseFS(files, pf)
		if err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		pages[name] = t
	}
	return &Engine{pages: pages}, nil
}

// Render writes the named page through the shared layout. Errors if
// the page is unknown — callers should surface that as a 500 so a
// typo in a route doesn't silently 404.
func (e *Engine) Render(w io.Writer, pageName string, data any) error {
	t, ok := e.pages[pageName]
	if !ok {
		return fmt.Errorf("templates: unknown page %q", pageName)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// RenderFragment writes a single named template directly, skipping
// the layout wrap. Used by fragment endpoints (e.g. /tasks/<id>/peek)
// that return HTML scoped to a target rather than a full page. The
// blockName is the template's defined block name (e.g. "peek"); errors
// when the page is unknown.
func (e *Engine) RenderFragment(w io.Writer, pageName, blockName string, data any) error {
	t, ok := e.pages[pageName]
	if !ok {
		return fmt.Errorf("templates: unknown page %q", pageName)
	}
	return t.ExecuteTemplate(w, blockName, data)
}

// buildFuncMap returns the shared template functions. `asset` resolves
// a logical path to its fingerprinted URL; it panics on miss so that a
// template typo surfaces loudly in tests rather than emitting a broken
// <link> at runtime.
func buildFuncMap(manifest *assets.Manifest) template.FuncMap {
	return template.FuncMap{
		// prose renders a description or note body as escaped HTML blocks.
		// The body is markdown prose (see internal/job/prose.go); no raw
		// HTML in the source survives, so the result is safe to mark.
		"prose": func(text string) template.HTML {
			return template.HTML(job.RenderProseHTML(text))
		},
		"asset": func(logicalPath string) (string, error) {
			u := manifest.URL(logicalPath)
			if u == "" {
				return "", fmt.Errorf("asset %q not in manifest", logicalPath)
			}
			return u, nil
		},
	}
}
