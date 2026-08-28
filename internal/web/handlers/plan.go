package handlers

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
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

// PlanShowTab is one of the Active/Archived/All tabs. URL preserves
// the current label selection; Active reflects the current ?show=.
type PlanShowTab struct {
	Label  string
	URL    string
	Active bool
}

// showMode is the ?show= value, normalized. "active" is the default.
const (
	showActive   = "active"
	showArchived = "archived"
	showAll      = "all"
)

// PlanLabelChip is one label pill in the plan filter bar. URL is the
// toggle URL — clicking adds the label if absent, removes if present.
// Active reflects whether the label is in the current selection.
// Color is the deterministic per-label HSL string (render.LabelColor)
// emitted as inline --label-color so the pill paints correctly on
// first frame, before colors.js runs. Typed as template.CSS so
// html/template doesn't substitute it with the ZgotmplZ stub when
// interpolated into a style attribute.
type PlanLabelChip struct {
	Name   string
	URL    string
	Active bool
	Color  template.CSS
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
		roots, err := job.RunListFiltered(deps.DB, job.ListFilter{ShowAll: true})
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

// filterRootsByKind keeps the roots belonging to one view. A root
// carries its kind; anything that isn't an issue tree is a task tree,
// so the task view is the complement of the issue view and no root
// falls through the gap.
func filterRootsByKind(roots []*job.TaskNode, kind job.TreeKind) []*job.TaskNode {
	out := make([]*job.TaskNode, 0, len(roots))
	for _, r := range roots {
		if r.Task.Kind.IsIssue() == kind.IsIssue() {
			out = append(out, r)
		}
	}
	return out
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

// parseShowParam normalizes the ?show= value. Unknown or empty inputs
// collapse to the default (active).
func parseShowParam(raw string) string {
	switch strings.TrimSpace(raw) {
	case showArchived:
		return showArchived
	case showAll:
		return showAll
	default:
		return showActive
	}
}

// isArchivedSubtree is true when a task and every descendant are
// closed (done or canceled). Used as the archive classifier at the
// root level — a partially-done tree is still "active" because it
// carries open work.
func isArchivedSubtree(n *job.TaskNode) bool {
	if n.Task.Status != "done" && n.Task.Status != "canceled" {
		return false
	}
	for _, c := range n.Children {
		if !isArchivedSubtree(c) {
			return false
		}
	}
	return true
}

// filterRootsByShow partitions roots into active/archived per the
// ?show= mode. archive classification is per-root: a whole subtree
// is archived iff every node in it is closed. Non-root nodes stay
// in view because a root's subtree is either shown or hidden as a
// unit — partial closure is normal within an active subtree.
func filterRootsByShow(roots []*job.TaskNode, show string) []*job.TaskNode {
	if show == showAll {
		return roots
	}
	out := make([]*job.TaskNode, 0, len(roots))
	for _, r := range roots {
		archived := isArchivedSubtree(r)
		if show == showArchived {
			if archived {
				out = append(out, r)
			}
		} else { // active
			if !archived {
				out = append(out, r)
			}
		}
	}
	return out
}

// buildShowTabs returns the three Active/Archived/All tabs for the
// filter row. Each tab's URL preserves the current label selection
// so switching archive mode doesn't drop the user's label filter.
func buildShowTabs(base string, selected []string, show string) []PlanShowTab {
	return []PlanShowTab{
		{Label: "Active", URL: planURL(base, selected, showActive), Active: show == showActive},
		{Label: "Archived", URL: planURL(base, selected, showArchived), Active: show == showArchived},
		{Label: "All", URL: planURL(base, selected, showAll), Active: show == showAll},
	}
}

// parseLabelParam splits a comma-separated ?label= value into a sorted,
// deduped slice. Empty/whitespace inputs collapse to nil. Sorting is
// canonical so toggling the same set produces the same URL regardless
// of the order chips were clicked in.
func parseLabelParam(raw string) []string {
	if raw == "" {
		return nil
	}
	seen := make(map[string]struct{})
	for part := range strings.SplitSeq(raw, ",") {
		s := strings.TrimSpace(part)
		if s == "" {
			continue
		}
		seen[s] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// labelFreqsByView counts label occurrences restricted to the tasks
// that match the current ?show= mode. For active view, labels on open
// tasks. For archived, labels on done/canceled tasks in archived
// subtrees. For all, labels on any task. The filter strip then reads
// "labels you can pick right now" in the current view.
func labelFreqsByView(roots []*job.TaskNode, labels map[int64][]string, show string) map[string]int {
	out := make(map[string]int)
	include := func(n *job.TaskNode) bool {
		switch show {
		case showArchived:
			return n.Task.Status == "done" || n.Task.Status == "canceled"
		case showAll:
			return true
		default:
			return n.Task.Status != "done" && n.Task.Status != "canceled"
		}
	}
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			if include(n) {
				for _, name := range labels[n.Task.ID] {
					out[name]++
				}
			}
			walk(n.Children)
		}
	}
	walk(roots)
	return out
}

// pickStripLabels returns the labels that appear in the filter strip:
// top-N most frequent labels in the current view, plus any currently-
// selected labels not in the top-N (so a selection never orphans).
// Top-N first (frequency desc, name asc tiebreak), then extras in
// name order.
func pickStripLabels(roots []*job.TaskNode, labels map[int64][]string, selected []string, show string, n int) []string {
	freqs := labelFreqsByView(roots, labels, show)
	type entry struct {
		name  string
		count int
	}
	all := make([]entry, 0, len(freqs))
	for name, c := range freqs {
		all = append(all, entry{name, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].name < all[j].name
	})
	top := make([]string, 0, n)
	inTop := make(map[string]bool)
	for i := 0; i < len(all) && len(top) < n; i++ {
		top = append(top, all[i].name)
		inTop[all[i].name] = true
	}
	// Append selected labels not already in the top-N.
	extras := make([]string, 0)
	for _, s := range selected {
		if !inTop[s] {
			extras = append(extras, s)
			inTop[s] = true
		}
	}
	sort.Strings(extras)
	return append(top, extras...)
}

// filterForestByLabels applies a multi-select label filter in memory.
// OR semantic: a task is kept if it carries any selected label OR has
// a descendant that does (ancestor chain preserved for context).
// Mirrors job.filterByLabel but operates on the pre-loaded labels map
// and a label set instead of a single name.
func filterForestByLabels(nodes []*job.TaskNode, labels map[int64][]string, selected []string) []*job.TaskNode {
	if len(selected) == 0 {
		return nodes
	}
	wanted := make(map[string]struct{}, len(selected))
	for _, s := range selected {
		wanted[s] = struct{}{}
	}
	matches := make(map[int64]bool)
	for id, ls := range labels {
		for _, n := range ls {
			if _, ok := wanted[n]; ok {
				matches[id] = true
				break
			}
		}
	}
	var walk func([]*job.TaskNode) []*job.TaskNode
	walk = func(ns []*job.TaskNode) []*job.TaskNode {
		var out []*job.TaskNode
		for _, n := range ns {
			kept := walk(n.Children)
			if matches[n.Task.ID] || len(kept) > 0 {
				out = append(out, &job.TaskNode{Task: n.Task, Children: kept})
			}
		}
		return out
	}
	return walk(nodes)
}

// buildPlanLabelChips renders the strip pills. URL toggles the label
// in/out of the current selection and preserves the show mode.
func buildPlanLabelChips(base string, stripNames []string, selected []string, show string) []PlanLabelChip {
	selSet := make(map[string]struct{}, len(selected))
	for _, s := range selected {
		selSet[s] = struct{}{}
	}
	out := make([]PlanLabelChip, 0, len(stripNames))
	for _, name := range stripNames {
		_, isSel := selSet[name]
		out = append(out, PlanLabelChip{
			Name:   name,
			URL:    planURL(base, toggleLabel(selected, name), show),
			Active: isSel,
			Color:  template.CSS(render.LabelColor(name)),
		})
	}
	return out
}

// buildAddLabelURLs maps each label name encountered in the forest to
// its enable-URL — the URL that adds the label to the current
// selection (no-op if already present). Preserves the show mode so
// inline pill clicks don't reset the archive tab.
func buildAddLabelURLs(base string, roots []*job.TaskNode, labels map[int64][]string, selected []string, show string) map[string]string {
	out := make(map[string]string)
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			for _, name := range labels[n.Task.ID] {
				if _, ok := out[name]; ok {
					continue
				}
				out[name] = planURL(base, addLabel(selected, name), show)
			}
			walk(n.Children)
		}
	}
	walk(roots)
	return out
}

// toggleLabel returns selected with name added if absent, removed if
// present. Returns a sorted slice so URLs are canonical.
func toggleLabel(selected []string, name string) []string {
	out := make([]string, 0, len(selected)+1)
	found := false
	for _, s := range selected {
		if s == name {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// addLabel returns selected with name added if absent (a no-op if
// already present). Returns a sorted slice so URLs are canonical.
func addLabel(selected []string, name string) []string {
	if slices.Contains(selected, name) {
		return append([]string(nil), selected...)
	}
	out := append([]string{name}, selected...)
	sort.Strings(out)
	return out
}

// planURL composes a Plan-shaped URL against base ("/plan",
// "/issues", or a scoped "/issues/<root>") for a given label selection
// and show mode. Each label is QueryEscape'd individually so exotic
// label names survive a round-trip, but the joining commas stay raw —
// they're URL-safe in query values and a literal comma is what
// parseLabelParam splits on. show=active is the default and is
// omitted from the URL to keep the default view's URL clean.
func planURL(base string, selected []string, show string) string {
	params := url.Values{}
	if len(selected) > 0 {
		parts := make([]string, len(selected))
		for i, s := range selected {
			parts[i] = url.QueryEscape(s)
		}
		// Set via raw join — url.Values.Set would double-escape commas.
		params["label"] = []string{strings.Join(parts, ",")}
	}
	if show != "" && show != showActive {
		params.Set("show", show)
	}
	if len(params) == 0 {
		return base
	}
	// Serialize manually so the pre-joined label value isn't re-escaped.
	q := planURLParams(params)
	return base + "?" + q
}

// planURLParams serializes url.Values with keys in alphabetical order
// so emitted URLs are canonical across calls. label is a pre-escaped
// comma-joined string; other keys are plain and get standard escaping.
func planURLParams(v url.Values) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("&")
		}
		b.WriteString(url.QueryEscape(k))
		b.WriteString("=")
		if k == "label" {
			// Already escaped per-segment; preserve the raw commas.
			b.WriteString(v[k][0])
		} else {
			b.WriteString(url.QueryEscape(v[k][0]))
		}
	}
	return b.String()
}

// collectTaskIDs walks a task forest in pre-order and returns every
// task id. Single pass so we can batch the follow-up lookups.
func collectTaskIDs(nodes []*job.TaskNode) []int64 {
	var ids []int64
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			ids = append(ids, n.Task.ID)
			walk(n.Children)
		}
	}
	walk(nodes)
	return ids
}

// collectTitlesByShortID indexes the forest by short id so blocker
// refs can carry the blocker's title for the hover tooltip without a
// second DB round-trip.
func collectTitlesByShortID(nodes []*job.TaskNode) map[string]string {
	out := make(map[string]string)
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			out[n.Task.ShortID] = n.Task.Title
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// buildPlanNodes maps the domain forest into template-ready PlanNodes.
// Post-order so children are built first; a node is collapsed only
// when every descendant (including itself) has a closed status, which
// matches "auto-collapse fully-done subtrees" from the task spec.
func buildPlanNodes(
	nodes []*job.TaskNode,
	labels map[int64][]string,
	blockers map[int64][]string,
	notes map[int64][]PlanNote,
	actors map[int64]string,
	titlesByShortID map[string]string,
	addLabelURLs map[string]string,
	now time.Time,
	depth int,
) []*PlanNode {
	out := make([]*PlanNode, 0, len(nodes))
	for _, n := range nodes {
		children := buildPlanNodes(n.Children, labels, blockers, notes, actors, titlesByShortID, addLabelURLs, now, depth+1)

		taskBlockers := blockers[n.Task.ID]
		displayStatus := DisplayStatus(n.Task.Status, len(taskBlockers) > 0)

		subtreeHasOpen := isOpenStatus(displayStatus)
		for _, c := range children {
			if !c.Collapsed || isOpenStatus(c.DisplayStatus) {
				subtreeHasOpen = true
				break
			}
		}

		// Rollup: a still-open branch whose subtree contains active
		// (claimed) work shows as active itself, so the tree glows
		// where something is actually in progress. Done and canceled
		// parents keep their own status — a closed branch stays closed
		// even if a reopened descendant has picked up life again.
		if isOpenStatus(displayStatus) {
			for _, c := range children {
				if c.DisplayStatus == "active" {
					displayStatus = "active"
					break
				}
			}
		}

		ts := time.Unix(n.Task.UpdatedAt, 0)
		hasChildren := len(children) > 0
		hasDesc := strings.TrimSpace(n.Task.Description) != ""
		out = append(out, &PlanNode{
			ShortID:       n.Task.ShortID,
			URL:           "/tasks/" + n.Task.ShortID,
			Title:         n.Task.Title,
			Description:   n.Task.Description,
			DisplayStatus: displayStatus,
			Actor:         actors[n.Task.ID],
			Labels:        buildRowLabels(labels[n.Task.ID], addLabelURLs),
			RelTime:       render.RelativeTime(now, ts),
			ISOTime:       ts.UTC().Format(time.RFC3339),
			BlockedBy:     buildBlockerRefs(taskBlockers, titlesByShortID),
			Notes:         markNotesStatus(notes[n.Task.ID], displayStatus),
			Children:      children,
			Depth:         depth,
			HasChildren:   hasChildren,
			Collapsible:   hasChildren || hasDesc,
			Collapsed:     !subtreeHasOpen,
		})
	}
	return out
}

// isOpenStatus is true for any status that still warrants attention.
// Done and canceled subtrees can collapse; everything else expands.
func isOpenStatus(displayStatus string) bool {
	return displayStatus != "done" && displayStatus != "canceled"
}

// buildRowLabels turns a row's plain label-name list into PlanRowLabels
// with their pre-computed enable-URLs.
func buildRowLabels(names []string, addLabelURLs map[string]string) []PlanRowLabel {
	if len(names) == 0 {
		return nil
	}
	out := make([]PlanRowLabel, len(names))
	for i, n := range names {
		out[i] = PlanRowLabel{Name: n, URL: addLabelURLs[n], Color: template.CSS(render.LabelColor(n))}
	}
	return out
}

func buildBlockerRefs(shortIDs []string, titlesByShortID map[string]string) []PlanBlockerRef {
	if len(shortIDs) == 0 {
		return nil
	}
	out := make([]PlanBlockerRef, len(shortIDs))
	for i, s := range shortIDs {
		out[i] = PlanBlockerRef{
			ShortID: s,
			URL:     "#task-" + s,
			Title:   titlesByShortID[s],
		}
	}
	return out
}

// markNotesStatus copies the task's display status onto each of its
// notes so the c-plan-note row can pick up the same tint as its
// parent task (muted when done, live when active, etc.).
func markNotesStatus(notes []PlanNote, displayStatus string) []PlanNote {
	if len(notes) == 0 {
		return nil
	}
	out := make([]PlanNote, len(notes))
	for i, n := range notes {
		n.DisplayStatus = displayStatus
		out[i] = n
	}
	return out
}

// loadPlanActors returns the display-actor for each task id: the most
// recent actor who claimed, completed, or canceled it. Tasks that
// have no such event (brand-new, never claimed) are absent from the
// map so the template renders an empty avatar slot. One query.
func loadPlanActors(db *sql.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string)
	if len(ids) == 0 {
		return out, nil
	}
	q, args := inClause(
		`SELECT task_id, actor FROM events
		 WHERE event_type IN ('claimed','done','canceled') AND task_id IN `,
		ids)
	q += ` ORDER BY created_at ASC, id ASC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var actor string
		if err := rows.Scan(&taskID, &actor); err != nil {
			return nil, err
		}
		out[taskID] = actor // last write wins → latest relevant event
	}
	return out, rows.Err()
}

// loadPlanNotes returns all 'noted' events grouped by task id, in
// chronological order. The note body is the `text` field of the
// JSON detail payload emitted by RunNote.
func loadPlanNotes(db *sql.DB, ids []int64) (map[int64][]PlanNote, error) {
	out := make(map[int64][]PlanNote)
	if len(ids) == 0 {
		return out, nil
	}
	q, args := inClause(
		`SELECT task_id, actor, detail, created_at FROM events
		 WHERE event_type = 'noted' AND task_id IN `,
		ids)
	q += ` ORDER BY created_at ASC, id ASC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var taskID int64
		var actor, detail string
		var createdAt int64
		if err := rows.Scan(&taskID, &actor, &detail, &createdAt); err != nil {
			return nil, err
		}
		text := noteTextFromDetail(detail)
		if text == "" {
			continue
		}
		ts := time.Unix(createdAt, 0)
		out[taskID] = append(out[taskID], PlanNote{
			Actor:   actor,
			RelTime: render.RelativeTime(now, ts),
			ISOTime: ts.UTC().Format(time.RFC3339),
			Text:    text,
		})
	}
	return out, rows.Err()
}

// noteTextFromDetail extracts the body text from a 'noted' event's
// detail blob. Returns empty string if the JSON is malformed or the
// text field is missing/empty — a silent skip is kinder than a panic
// on a hand-edited DB row.
func noteTextFromDetail(detail string) string {
	if detail == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return ""
	}
	s, _ := payload["text"].(string)
	return strings.TrimSpace(s)
}

// inClause builds `prefix (?,?,?,…)` for a fixed-length id slice and
// returns the bound args. Callers append their own ORDER BY / LIMIT.
// Kept local to plan.go because the log view's equivalent is trivial
// enough to live inline and the two diverge in shape.
func inClause(prefix string, ids []int64) (string, []any) {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("(")
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
		args[i] = id
	}
	b.WriteString(")")
	return b.String(), args
}
