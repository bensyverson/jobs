package handlers

import (
	"html/template"
	"net/http"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// PlanPageData is the payload rendered by the plan template.
type PlanPageData struct {
	templates.Chrome
	Roots     []*PlanNode
	HasTasks  bool
	Labels    []PlanLabelChip
	AllURL    string
	AllActive bool
	// ShowTabs carries the three Active/Archived/All tabs with their
	// href + active state already computed; template just iterates.
	ShowTabs []PlanShowTab
	// View carries everything that differs between /plan and /issues:
	// the section's accessible name, the kind marker the client-side
	// modules read, the base path every filter URL composes against,
	// and the empty-state copy.
	View planView
	// Meta is the view's one-line period stat, rendered beside the
	// Active/Archived/All tabs. Empty on Plan; on Issues it reads
	// "N open · M closed in 7d". Always reflects now, like the footer
	// metrics — the history scrubber reshapes the tree below it, not
	// this line.
	Meta string
}

// planView is the per-view configuration the Plan page template and
// the Plan client modules are parameterised by. One page skeleton,
// two kinds: /plan renders task-tree roots, /issues renders issue-tree
// roots (project/2026-08-28-issues-ux.md, decision 4 and 7).
//
// BasePath is the path every filter URL composes against, so the show
// tabs and label pills of a scoped view stay inside that scope.
type planView struct {
	Kind        job.TreeKind
	ActiveTab   string
	BasePath    string
	SectionName string
	FilterName  string
	EmptyText   string
}

// planTaskView is /plan: the planned work, issue roots excluded.
var planTaskView = planView{
	Kind:        job.KindTask,
	ActiveTab:   "plan",
	BasePath:    "/plan",
	SectionName: "Plan",
	FilterName:  "Plan filter",
	EmptyText:   "No active tasks.",
}

// PlanRowLabel is one label pill rendered inline on a task row. URL
// is an enable-URL: clicking adds the label to the current selection
// (no-op if already selected). Inline pills don't deselect — that's
// the strip's job. Color follows PlanLabelChip.Color.
type PlanRowLabel struct {
	Name  string
	URL   string
	Color template.CSS
}

// PlanNode is one node in the rendered plan tree. All fields are
// preformatted so the template stays decision-free.
type PlanNode struct {
	ShortID       string
	URL           string
	Title         string
	Description   string
	DisplayStatus string
	Actor         string
	Labels        []PlanRowLabel
	RelTime       string
	ISOTime       string
	BlockedBy     []PlanBlockerRef
	Notes         []PlanNote
	Children      []*PlanNode
	// Depth is 0 for root tasks, 1 for their direct children, etc. The
	// template uses it to pick heading weight (root → lg, depth 1 → md).
	Depth int
	// HasChildren controls whether the following .c-plan-subtree wrapper
	// renders — a template convenience, not a collapsibility signal.
	HasChildren bool
	// Collapsible is true when the row has anything to hide: children,
	// a description, or (future) a rollup metric. Drives the disclosure
	// button's presence and the data-collapsed attribute. A bare leaf
	// row carries neither and stays chevron-free.
	Collapsible bool
	// Collapsed is true when the node's subtree is fully done/canceled;
	// CSS hides the description, blocked-by, notes, and subtree on
	// collapsed rows. Later phases attach a JS toggle.
	Collapsed bool
	// Links is the page's prose resolver, the same map on every node:
	// the row template is recursive, so the page's own data is out of
	// scope by the time a description renders.
	Links job.ProseLinks
}

// PlanNote is one note entry rendered under a task as a c-plan-note row.
type PlanNote struct {
	Actor         string
	RelTime       string
	ISOTime       string
	Text          string
	DisplayStatus string
}

// PlanBlockerRef is one blocker link shown in the "Blocked by" row.
// The URL is an in-page anchor; the title is the blocker's own title,
// rendered as the pill's hover tooltip so a reader can understand
// "Blocked by <id>" without jumping, even if the blocker is inside a
// currently-collapsed subtree.
type PlanBlockerRef struct {
	ShortID string
	URL     string
	Title   string
}

// Plan renders the document-mode tree view over the task-tree roots.
// Issue roots live at /issues; see planHandler.
func Plan(deps Deps) http.Handler { return planHandler(deps, planTaskView) }

// planHandler is the shared Plan/Issues view. view.Kind picks which
// roots the forest is built from; everything below it — the archive
// tabs, the label strip, the tree, the collapse state — is identical.
//
// A non-empty {id} path value scopes the view to that single root
// (/plan/{id}, /issues/{root-id}); a root that doesn't exist, or whose
// kind belongs to the other view, is a 404 rather than an empty page,
// because the URL names something this view cannot show.
func planHandler(deps Deps, view planView) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selected := parseLabelParam(r.URL.Query().Get("label"))
		show := parseShowParam(r.URL.Query().Get("show"))
		scopeID := r.PathValue("id")
		if scopeID != "" {
			view.BasePath = view.BasePath + "/" + scopeID
		}

		// Load the unfiltered forest first so the labels strip reflects
		// what's actually present in the document; the strip needs to
		// stay stable across label switches, so we can't derive it from
		// a label-filtered forest. The label filter then applies in
		// memory using the already-batched labels map.
		roots, err := job.RunListFiltered(deps.DB, job.ListFilter{ShowAll: true, Actor: sweepActor(deps)})
		if err != nil {
			InternalError(deps, w, "plan list", err)
			return
		}
		roots = filterRootsByKind(roots, view.Kind)
		if scopeID != "" {
			roots = scopeRootsTo(roots, scopeID)
			if len(roots) == 0 {
				NotFound(deps).ServeHTTP(w, r)
				return
			}
		}

		ids := collectTaskIDs(roots)
		titlesByShortID := collectTitlesByShortID(roots)

		labels, err := job.GetLabelsForTaskIDs(deps.DB, ids)
		if err != nil {
			InternalError(deps, w, "plan labels", err)
			return
		}

		// Apply the archive filter first so subsequent strip and label
		// calculations reflect what's actually in view.
		roots = filterRootsByShow(roots, show)

		stripNames := pickStripLabels(roots, labels, selected, show, 5)
		if len(selected) > 0 {
			roots = filterForestByLabels(roots, labels, selected)
		}
		// Recompute ids after the in-memory filter so the follow-up
		// blockers / notes / actors lookups stay scoped to what we'll
		// actually render.
		ids = collectTaskIDs(roots)
		blockers, err := job.GetBlockersForTaskIDs(deps.DB, ids)
		if err != nil {
			InternalError(deps, w, "plan blockers", err)
			return
		}
		notes, err := loadPlanNotes(deps.DB, ids)
		if err != nil {
			InternalError(deps, w, "plan notes", err)
			return
		}
		actors, err := loadPlanActors(deps.DB, ids)
		if err != nil {
			InternalError(deps, w, "plan actors", err)
			return
		}

		now := time.Now()
		addLabelURLs := buildAddLabelURLs(view.BasePath, roots, labels, selected, show)
		planRoots := buildPlanNodes(roots, labels, blockers, notes, actors, titlesByShortID, addLabelURLs, now, 0)
		if err := attachProseLinks(deps.DB, planRoots); err != nil {
			InternalError(deps, w, "prose links", err)
			return
		}

		chrome, err := newChrome(r.Context(), deps, view.ActiveTab, now)
		if err != nil {
			InternalError(deps, w, "plan initial frame", err)
			return
		}
		meta, err := viewMeta(deps, view, now)
		if err != nil {
			InternalError(deps, w, "plan meta", err)
			return
		}
		data := PlanPageData{
			Chrome:    chrome,
			Roots:     planRoots,
			HasTasks:  len(planRoots) > 0,
			Labels:    buildPlanLabelChips(view.BasePath, stripNames, selected, show),
			AllURL:    planURL(view.BasePath, nil, show),
			AllActive: len(selected) == 0,
			ShowTabs:  buildShowTabs(view.BasePath, selected, show),
			View:      view,
			Meta:      meta,
		}
		renderPage(deps, w, "plan", data)
	})
}

// scopeRootsTo narrows the forest to the single root with shortID,
// or returns nil when this view holds no such root.
func scopeRootsTo(roots []*job.TaskNode, shortID string) []*job.TaskNode {
	for _, r := range roots {
		if r.Task.ShortID == shortID {
			return []*job.TaskNode{r}
		}
	}
	return nil
}
