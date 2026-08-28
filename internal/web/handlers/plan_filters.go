package handlers

import (
	"html/template"
	"net/url"
	"slices"
	"sort"
	"strings"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
)

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
