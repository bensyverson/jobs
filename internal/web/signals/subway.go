package signals

import (
	"context"
	"database/sql"
	"sort"
	"time"
)

// Default windowing and lookahead values for the production home view.
// L was tightened from the original spec default of 2 to 1 once the
// dashboard was rendered against real data and the wider preview made
// rows feel cluttered. N stays at 2. The design doc's six reference
// scenarios are still authored against L=2 — tests that exercise those
// shapes pass 2 explicitly via buildSubwayWith.
const (
	subwayLookahead = 1
	subwayWindow    = 2
)

// BuildSubway loads the current task graph and produces the
// topological Subway model: lines, optional fork, deduplicated
// nodes, and typed edges. Snapshot-per-request; rendering and SVG
// layout live downstream in the render package.
//
// `now` is reserved for future use (e.g. filtering stale claims) and
// is currently unused, kept for signature symmetry with
// ComputeMiniGraph.
func BuildSubway(ctx context.Context, db *sql.DB, _ time.Time) (Subway, error) {
	w, err := loadGraphWorld(ctx, db)
	if err != nil {
		return Subway{}, err
	}
	return buildSubway(w), nil
}

// buildSubway is the pure, world-driven core of BuildSubway running
// at production defaults. Tests that depend on specific lookahead /
// window values call buildSubwayWith instead.
func buildSubway(w *graphWorld) Subway {
	return buildSubwayWith(w, subwayLookahead, subwayWindow)
}

// buildSubwayWith is the parameterized core. The public entry point
// (BuildSubway) and buildSubway both delegate here.
//
// Mode dispatch is per-cluster (focals grouped by project root):
// single-focal clusters render via buildSingleFocalLine (project/
// 2026-04-27-graph-row-merging.md, single-focal preorder window
// mode); multi-focal clusters render via buildMultiFocalRows (same
// doc, multi-focal tree-map mode), with rows grouped by parent
// short ID into Fork entries so the existing layout/branch-curve
// loop in render/subway_layout.go keeps working unchanged. The
// layout pivot to depth-aligned columns and retiring AncestorChain
// in favor of per-row ParentShortID lands in S3.
//
// The lookahead parameter is unused under the new modes; it stays
// on the signature so tests authored against L=2 still parameterize
// the call. The legacy collectLines/applyWindow/computeForks
// helpers remain in this file with their own unit tests until a
// follow-up retires them.
func buildSubwayWith(w *graphWorld, _ /* lookahead */, window int) Subway {
	focals := pickFocals(w)
	if len(focals) == 0 {
		return Subway{}
	}

	preorder := preorderAll(w)
	preorderPos := make(map[int64]int, len(preorder))
	for i, t := range preorder {
		preorderPos[t.id] = i
	}

	clusters := map[int64][]*graphTask{}
	var rootOrder []*graphTask
	for _, f := range focals {
		root := f
		for root.parent != nil {
			root = root.parent
		}
		if _, ok := clusters[root.id]; !ok {
			rootOrder = append(rootOrder, root)
		}
		clusters[root.id] = append(clusters[root.id], f)
	}
	sort.SliceStable(rootOrder, func(i, j int) bool {
		return preorderPos[rootOrder[i].id] < preorderPos[rootOrder[j].id]
	})

	var lines []Line
	var forks []*Fork
	for _, root := range rootOrder {
		clusterFocals := clusters[root.id]
		if len(clusterFocals) == 1 {
			lines = append(lines, buildSingleFocalLine(w, clusterFocals[0], window))
			continue
		}
		// Multi-focal cluster: render via the tree-map mode
		// (project/2026-04-27-graph-row-merging.md). Each row in
		// rows carries a ParentShortID identifying its branch
		// parent in the focal-path subgraph (empty for the
		// topmost row, whose leftmost is the cluster LCA). The
		// branch-curve loop in render/subway_layout.go reads
		// ParentShortID directly to position each row's leftmost
		// at col(parent) + 1; the per-row parent edge has
		// retired the legacy Fork.AncestorChain shim.
		baseIdx := len(lines)
		rows := buildMultiFocalRows(w, clusterFocals, window)
		if len(rows) == 0 {
			continue
		}
		lines = append(lines, rows...)
		// Group sub-rows by parent for the Fork bookkeeping that
		// drives branch-edge generation below. A Fork now carries
		// just LineIndices — the parent identity lives on each
		// line's ParentShortID.
		type forkGroup struct {
			parent  string
			indices []int
		}
		var groups []*forkGroup
		groupByParent := map[string]*forkGroup{}
		for i, row := range rows {
			if row.ParentShortID == "" {
				continue
			}
			g, ok := groupByParent[row.ParentShortID]
			if !ok {
				g = &forkGroup{parent: row.ParentShortID}
				groupByParent[row.ParentShortID] = g
				groups = append(groups, g)
			}
			g.indices = append(g.indices, baseIdx+i)
		}
		for _, g := range groups {
			forks = append(forks, &Fork{LineIndices: g.indices})
		}
	}
	if len(lines) == 0 {
		return Subway{}
	}

	byShort := indexByShortID(w)
	var nodes []SubwayNode
	seen := map[string]bool{}
	pushNode := func(t *graphTask) {
		if t == nil || seen[t.shortID] {
			return
		}
		seen[t.shortID] = true
		nodes = append(nodes, SubwayNode{
			ShortID: t.shortID,
			Title:   t.title,
			State:   subwayState(t),
			Actor:   t.actor,
			URL:     "/tasks/" + t.shortID,
		})
	}
	for _, line := range lines {
		if line.ParentShortID != "" {
			pushNode(byShort[line.ParentShortID])
		}
		pushNode(byShort[line.AnchorShortID])
		for _, item := range line.Items {
			if item.Kind == LineItemStop {
				pushNode(byShort[item.ShortID])
			}
		}
	}

	var edges []SubwayEdge
	for _, fork := range forks {
		for _, idx := range fork.LineIndices {
			line := lines[idx]
			if line.ParentShortID == "" {
				continue
			}
			anchor := byShort[line.AnchorShortID]
			if anchor == nil {
				continue
			}
			kind := SubwayEdgeBranch
			if anchor.openBlockers > 0 {
				kind = SubwayEdgeBranchClosed
			}
			edges = append(edges, SubwayEdge{
				FromShortID: line.ParentShortID,
				ToShortID:   anchor.shortID,
				Kind:        kind,
			})
		}
	}
	// Same-row stop blockage renders on the immediate ingress edge.
	// "Same row" means both stops appear on the same Line (under the
	// new single-focal preorder mode this is "same preorder window";
	// under the legacy parent-rooted model it's "same parent," which
	// happens to be a stricter version of the same predicate). The
	// nearest blocked stop's ingress edge becomes a Blocker
	// (replacing Flow); subsequent blocks by the same blocker are
	// transitive and don't earn an extra marker.
	lineByStop := map[string]int{}
	for i, line := range lines {
		for _, item := range line.Items {
			if item.Kind == LineItemStop {
				lineByStop[item.ShortID] = i
			}
		}
	}
	candidates := map[string][]*graphTask{}
	var blockerOrder []string
	anchorSet := map[string]bool{}
	for _, line := range lines {
		anchorSet[line.AnchorShortID] = true
	}
	for _, n := range nodes {
		if anchorSet[n.ShortID] {
			continue
		}
		t := byShort[n.ShortID]
		if t == nil {
			continue
		}
		for _, blockerID := range t.blockerIDs {
			blocker := w.byID[blockerID]
			if blocker == nil {
				continue
			}
			if blocker.status == "done" || blocker.status == "canceled" {
				continue
			}
			if !seen[blocker.shortID] {
				continue
			}
			bLine, bOK := lineByStop[blocker.shortID]
			tLine, tOK := lineByStop[t.shortID]
			if !bOK || !tOK || bLine != tLine {
				continue
			}
			if _, exists := candidates[blocker.shortID]; !exists {
				blockerOrder = append(blockerOrder, blocker.shortID)
			}
			candidates[blocker.shortID] = append(candidates[blocker.shortID], t)
		}
	}
	type pair struct{ from, to string }
	ingressBlocked := map[pair]bool{}
	for _, blockerSID := range blockerOrder {
		blocked := candidates[blockerSID]
		sort.SliceStable(blocked, func(i, j int) bool {
			return preorderPos[blocked[i].id] < preorderPos[blocked[j].id]
		})
		nearest := blocked[0]
		lineIdx, ok := lineByStop[nearest.shortID]
		if !ok {
			continue
		}
		line := lines[lineIdx]
		pred := line.AnchorShortID
		for _, item := range line.Items {
			if item.Kind != LineItemStop {
				continue
			}
			if item.ShortID == nearest.shortID {
				break
			}
			pred = item.ShortID
		}
		ingressBlocked[pair{pred, nearest.shortID}] = true
	}

	for _, line := range lines {
		prev := line.AnchorShortID
		for _, item := range line.Items {
			switch item.Kind {
			case LineItemStop:
				kind := SubwayEdgeFlow
				if ingressBlocked[pair{prev, item.ShortID}] {
					kind = SubwayEdgeBlocker
				}
				edges = append(edges, SubwayEdge{
					FromShortID: prev,
					ToShortID:   item.ShortID,
					Kind:        kind,
				})
				prev = item.ShortID
			}
		}
	}

	return Subway{
		Lines: lines,
		Forks: forks,
		Nodes: nodes,
		Edges: edges,
	}
}

// indexByShortID returns a lookup table from short ID to graphTask
// for every task in w. The Subway model addresses tasks by short ID,
// so this map is built once per BuildSubway call.
func indexByShortID(w *graphWorld) map[string]*graphTask {
	out := make(map[string]*graphTask, len(w.byID))
	for _, t := range w.byID {
		out[t.shortID] = t
	}
	return out
}

// subwayState maps a task's status to its rendered Subway state.
// Blocked is no longer a node state under the subway model: closure
// lives on the ingress edge (BranchClosed) or as a Blocker edge.
func subwayState(t *graphTask) SubwayNodeState {
	switch t.status {
	case "claimed":
		return SubwayNodeActive
	case "done", "canceled":
		return SubwayNodeDone
	}
	return SubwayNodeTodo
}

// Subway is the topological model of the mini-graph under the
// subway-system metaphor. See
// project/2026-04-25-graph-clarification.md for the design.
//
// It describes one or more Lines (each anchored at a parent node,
// displaying that parent's children as stops in tree order with
// elision markers for skipped stops), an optional Fork (present only
// when two or more lines branch from a common ancestor), a
// deduplicated Nodes pool, and a typed Edges list.
//
// Grid/pixel positioning and SVG routing belong to the layout step in
// the render package; nothing here knows about columns, rows, or
// stroke styles.
type Subway struct {
	Lines []Line
	// Forks holds one entry per cluster of lines that share a project
	// root. When all rendered lines share a single root the slice has
	// one Fork; with cross-project claims we emit one Fork per root so
	// each cluster's transfer station renders. A single isolated line
	// (one line, one root) leaves the slice empty — the LCA would be
	// chrome.
	Forks []*Fork
	Nodes []SubwayNode
	Edges []SubwayEdge
}

// Line is a single subway line / row: an anchor (the row's
// leftmost) followed by an ordered sequence of items (stops and
// elision markers). The anchor's semantics depend on the cluster's
// rendering mode:
//
//   - Legacy parent-rooted line: anchor is the parent task whose
//     children render as stops along this line.
//   - Single-focal preorder window mode (project/2026-04-27-graph-
//     row-merging.md): anchor is the project root; items are the
//     focal's ±N preorder neighbors within the project tree.
//   - Multi-focal tree-map mode (same doc): anchor is the row's
//     leftmost in the focal-path subgraph; items are the visible
//     stops on that row's branch with per-row ±N windowing.
//     ParentShortID identifies the row's parent in the subgraph
//     for the branch-curve geometry; it is empty for the topmost
//     row.
//
// Tree order, not claim order: stops follow their underlying
// sort_key so the layout stays stable as claims churn.
type Line struct {
	AnchorShortID string
	ParentShortID string
	Items         []LineItem
}

// LineItem is one element in a Line's sequence — a stop or one of
// the elision markers.
//
// Stop: ShortID is set; renders as a node disc on the line.
//
// Elision: drawn as small dots in the gap between two surrounding
// items on the line (anchor↔stop, stop↔stop). It does not consume a
// column slot and only carries presentational meaning.
type LineItem struct {
	Kind    LineItemKind
	ShortID string
}

// LineItemKind tags a LineItem as a stop, an in-gap elision, a
// broken-line elision (single-focal preorder mode), or a
// terminating ellipsis (single-focal preorder mode).
type LineItemKind int

const (
	// LineItemStop is a rendered stop on the line; ShortID is set.
	LineItemStop LineItemKind = iota
	// LineItemElision is an in-gap dots marker; ShortID is empty.
	// Sits between two surrounding items, takes no slot. Used by
	// the legacy parent-rooted line model.
	LineItemElision
	// LineItemElisionBroken is a broken-line elision used by the
	// single-focal preorder window mode: a short stub from the
	// preceding item, three small SVG circles in negative space, a
	// short stub into the following item. Used between leftmost and
	// the row's first content stop when the -N walk doesn't reach
	// the project root, and between two non-adjacent visible windows
	// inside the same row. Takes no slot.
	LineItemElisionBroken
	// LineItemElisionTerminating is a trailing terminating ellipsis
	// at the right edge of a single-focal preorder row: disc →
	// segment → three dots, no trailing arrow stub. Takes no slot.
	LineItemElisionTerminating
)

// Fork groups sub-rows that share a common branch parent. The
// parent identity now lives on each Line's ParentShortID
// (project/2026-04-27-graph-row-merging.md, S3); Fork retains
// LineIndices as a coarse grouping so the edge-generation pass can
// emit one branch edge per row without rescanning the lines slice.
//
// LineIndices references Subway.Lines in display order (top to
// bottom). A Subway with a single line or no branching produces no
// Forks — there is nothing to group.
type Fork struct {
	LineIndices []int
}

// SubwayNode is a single task's presentation metadata, unique per
// Subway by ShortID.
type SubwayNode struct {
	ShortID string
	Title   string
	State   SubwayNodeState
	Actor   string
	URL     string
}

// SubwayNodeState drives the stop's visual treatment.
//
// Blocked-ness is no longer a node state in the subway model: a line
// blocked by a prior phase carries a closure marker on its ingress
// edge (SubwayEdgeBranchClosed), and an explicit blocks relationship
// between two rendered nodes carries a SubwayEdgeBlocker. Stops
// themselves render as Todo, Active, or Done.
type SubwayNodeState int

const (
	// SubwayNodeTodo is an available, unclaimed stop.
	SubwayNodeTodo SubwayNodeState = iota
	// SubwayNodeActive is a currently-claimed stop.
	SubwayNodeActive
	// SubwayNodeDone is a completed stop within a visible window.
	SubwayNodeDone
)

// SubwayEdge connects two rendered short IDs.
type SubwayEdge struct {
	FromShortID string
	ToShortID   string
	Kind        SubwayEdgeKind
}

// SubwayEdgeKind classifies an edge for rendering. The cousin-hop
// concept from the previous spine model goes away under Model D —
// lines never cross parent boundaries inline, so every flow edge sits
// between siblings on the same line or between a line anchor and its
// first/last visible stop.
type SubwayEdgeKind int

const (
	// SubwayEdgeFlow connects two adjacent items along a line, or a
	// line anchor to its first/last visible stop.
	SubwayEdgeFlow SubwayEdgeKind = iota
	// SubwayEdgeBranch connects the fork's divergence node to a
	// line's anchor when the line is open (no ingress block).
	SubwayEdgeBranch
	// SubwayEdgeBranchClosed connects the fork's divergence node to
	// a line's anchor when the line is sequence-blocked (a prior
	// sibling phase isn't done yet). Renders with a `⊘` decoration.
	SubwayEdgeBranchClosed
	// SubwayEdgeBlocker is an explicit `blocks` edge between two
	// rendered nodes, distinct from the sequential-phase ingress
	// block. The exact rendering glyph is an open question
	// (different glyph, dashed style, or color).
	SubwayEdgeBlocker
)

// ------------------------------------------------------------------
// Line collection (Phase 1)
// ------------------------------------------------------------------

// lineSeed is the intermediate result of line collection: a parent
// whose subtree contains an active focal or a lookahead-touched stop.
// LCA fork computation and windowing consume seeds in tree order.
//
// focalAnchors are claimed children of parent (or, when nothing is
// claimed globally, the single globally-next leaf). They always
// anchor a ±N window in applyWindow.
//
// lookaheadAnchors are children of parent reached via the +L leaf
// walk from a focal. They anchor a window only when their index
// isn't already inside some focal's ±N window on the same parent —
// the dedup that keeps the focal's line capped at ±N around the
// focal in the common adjacent-lookahead case, while still
// preserving a disjoint window for far lookaheads.
//
// Both slices are sorted in tree order (sort_key). A child can
// appear in only one slice: if a task is both a focal and a
// lookahead target, it stays a focal.
type lineSeed struct {
	parent           *graphTask
	focalAnchors     []*graphTask
	lookaheadAnchors []*graphTask
}

// collectLines returns the line seeds that should render given the
// set of focal tasks (currently-claimed or, when no claims exist, the
// globally-next available leaf). Each focal contributes its parent's
// line; from each focal we walk +L leaves in tree-traversal order,
// and any parent of a touched stop also gets a line.
//
// Output ordering is preorder over the project tree, so visually-
// adjacent lines sit next to each other in the rendered subway.
// Within each line, anchors are sorted by sort_key (tree order).
func collectLines(w *graphWorld, focals []*graphTask, L int) []*lineSeed {
	if len(focals) == 0 {
		return nil
	}

	type lineDraft struct {
		parent     *graphTask
		focals     map[int64]*graphTask
		lookaheads map[int64]*graphTask
	}
	drafts := map[int64]*lineDraft{}

	getDraft := func(parent *graphTask) *lineDraft {
		d, ok := drafts[parent.id]
		if !ok {
			d = &lineDraft{
				parent:     parent,
				focals:     map[int64]*graphTask{},
				lookaheads: map[int64]*graphTask{},
			}
			drafts[parent.id] = d
		}
		return d
	}
	// focalParents is the set of every parent that a focal sits
	// under. A lookahead anchor whose parent is one of these — or a
	// descendant of one — is inside an already-rendered line's
	// subtree and earns no new line. Only lookaheads that *exit*
	// upward across a parent boundary (cousin or further) seed a new
	// peek-line. The fork machinery extends the LCA chain in that
	// case.
	focalParents := map[int64]bool{}
	for _, focal := range focals {
		if focal.parent != nil {
			focalParents[focal.parent.id] = true
		}
	}
	inFocalSubtree := func(stop *graphTask) bool {
		if stop == nil || stop.parent == nil {
			return false
		}
		for cur := stop.parent; cur != nil; cur = cur.parent {
			if focalParents[cur.id] {
				return true
			}
		}
		return false
	}
	addFocal := func(stop *graphTask) {
		if stop == nil || stop.parent == nil {
			return
		}
		getDraft(stop.parent).focals[stop.id] = stop
	}
	addLookahead := func(stop *graphTask) {
		if stop == nil || stop.parent == nil {
			return
		}
		if inFocalSubtree(stop) {
			return
		}
		getDraft(stop.parent).lookaheads[stop.id] = stop
	}

	for _, focal := range focals {
		addFocal(focal)
		cur := focal
		for range L {
			next := nextLeaf(w, cur)
			if next == nil {
				break
			}
			addLookahead(next)
			cur = next
		}
	}

	preorder := preorderAll(w)
	pos := make(map[int64]int, len(preorder))
	for i, t := range preorder {
		pos[t.id] = i
	}

	ordered := make([]*lineDraft, 0, len(drafts))
	for _, d := range drafts {
		ordered = append(ordered, d)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		return pos[ordered[i].parent.id] < pos[ordered[j].parent.id]
	})

	out := make([]*lineSeed, 0, len(ordered))
	for _, d := range ordered {
		// A task that is both a focal and a lookahead target stays a
		// focal — focals always anchor a window, lookaheads only when
		// not already covered.
		focals := make([]*graphTask, 0, len(d.focals))
		for _, a := range d.focals {
			focals = append(focals, a)
		}
		sort.SliceStable(focals, func(i, j int) bool {
			return focals[i].sortKey < focals[j].sortKey
		})
		lookaheads := make([]*graphTask, 0, len(d.lookaheads))
		for _, a := range d.lookaheads {
			if _, isFocal := d.focals[a.id]; isFocal {
				continue
			}
			lookaheads = append(lookaheads, a)
		}
		sort.SliceStable(lookaheads, func(i, j int) bool {
			return lookaheads[i].sortKey < lookaheads[j].sortKey
		})
		out = append(out, &lineSeed{
			parent:           d.parent,
			focalAnchors:     focals,
			lookaheadAnchors: lookaheads,
		})
	}
	return out
}

// nextLeaf returns the next preorder leaf strictly after t in the
// tree, or nil if no such leaf exists. "Leaf" means a task with no
// children: lookahead skips over internal nodes, since stops along a
// line are always its parent's direct children.
func nextLeaf(w *graphWorld, t *graphTask) *graphTask {
	cur := t
	for {
		next := successorPreorder(w, cur)
		if next == nil {
			return nil
		}
		if len(next.children) == 0 {
			return next
		}
		cur = next
	}
}

// applyWindow turns a lineSeed into a Line with ±N windowing
// applied. Each anchor (claimed or lookahead-touched stop) anchors a
// window of ±N siblings; the union of those windows becomes the set
// of visible stops. Elision markers (`…`) appear between the parent
// anchor and the first visible stop when the leading siblings are
// hidden, between two visible windows that aren't adjacent, and
// after the last visible stop when trailing siblings are hidden.
//
// Done siblings don't anchor windows of their own — they render only
// when they fall inside another anchor's window. Two close focals
// produce a single merged window (the line stays visually
// continuous); two distant focals produce two windows separated by
// a `…` (the multi-focal union case from the spec).
func applyWindow(seed *lineSeed, N int) Line {
	if seed == nil || seed.parent == nil {
		return Line{}
	}
	line := Line{AnchorShortID: seed.parent.shortID}
	children := seed.parent.children
	if len(children) == 0 {
		return line
	}

	indexOf := make(map[int64]int, len(children))
	for i, c := range children {
		indexOf[c.id] = i
	}

	visibleSet := map[int]bool{}
	addWindow := func(idx int) {
		lo := max(idx-N, 0)
		hi := min(idx+N, len(children)-1)
		for i := lo; i <= hi; i++ {
			visibleSet[i] = true
		}
	}
	for _, anchor := range seed.focalAnchors {
		idx, ok := indexOf[anchor.id]
		if !ok {
			continue
		}
		addWindow(idx)
	}
	// Lookahead anchors anchor a window only when their index isn't
	// already covered by a focal's window — adjacent lookahead stays
	// inside the focal's frame (the common single-claim case caps at
	// ±N around the focal), far lookahead opens a disjoint window
	// that the elision pass joins to the focal's span with an in-line
	// `…`.
	for _, anchor := range seed.lookaheadAnchors {
		idx, ok := indexOf[anchor.id]
		if !ok {
			continue
		}
		if visibleSet[idx] {
			continue
		}
		addWindow(idx)
	}
	if len(visibleSet) == 0 {
		return line
	}

	visible := make([]int, 0, len(visibleSet))
	for i := range visibleSet {
		visible = append(visible, i)
	}
	sort.Ints(visible)

	if visible[0] > 0 {
		line.Items = append(line.Items, LineItem{Kind: LineItemElision})
	}
	for i, idx := range visible {
		if i > 0 && idx > visible[i-1]+1 {
			line.Items = append(line.Items, LineItem{Kind: LineItemElision})
		}
		line.Items = append(line.Items, LineItem{
			Kind:    LineItemStop,
			ShortID: children[idx].shortID,
		})
	}
	if last := visible[len(visible)-1]; last < len(children)-1 {
		// Trailing siblings beyond the visible window collapse to a
		// terminating ellipsis (project/2026-04-27-graph-row-
		// merging.md).
		line.Items = append(line.Items, LineItem{Kind: LineItemElisionTerminating})
	}
	return line
}

// Legacy LCA fork helpers (computeFork / computeForks) lived here
// until S3d. They produced a Fork.AncestorChain that the layout used
// to position the LCA chain inline with one of the cluster's rows;
// the depth-aligned-leftmost rule plus per-row Line.ParentShortID
// (project/2026-04-27-graph-row-merging.md, S3) replaces both of
// those concerns, and BuildSubway hasn't called these helpers since
// S2.

// lcaPair returns the lowest common ancestor of two tasks, or nil
// when they share no ancestor (disjoint trees). When one is an
// ancestor of the other, it is returned directly.
func lcaPair(a, b *graphTask) *graphTask {
	if a == nil || b == nil {
		return nil
	}
	seen := map[int64]*graphTask{}
	for cur := a; cur != nil; cur = cur.parent {
		seen[cur.id] = cur
	}
	for cur := b; cur != nil; cur = cur.parent {
		if anc, ok := seen[cur.id]; ok {
			return anc
		}
	}
	return nil
}

// buildSingleFocalLine returns the Line for a single-focal cluster
// under the new preorder window mode (project/2026-04-27-graph-row-
// merging.md). The row's leftmost is the first stop of the ±N
// preorder window around the focal; content stops are the rest of
// the visible window in preorder.
//
// The leftmost is preorder[focalPos-N] when that position lies
// inside the project tree, or preorder[0] (the project root) when
// the -N walk reaches all the way back. Single-focal mode
// deliberately skips adding a chrome LCA — the project root is
// only structurally needed in multi-focal mode where it serves as
// the curve target for sub-rows. Trailing terminating elision
// (LineItemElisionTerminating) sits at the right edge when the +N
// walk continues past the row's last visible stop.
//
// The function never emits LineItemElision (the in-gap dots marker
// from the legacy parent-rooted line model); trailing siblings
// collapse to LineItemElisionTerminating.
func buildSingleFocalLine(_ *graphWorld, focal *graphTask, N int) Line {
	if focal == nil {
		return Line{}
	}
	root := focal
	for root.parent != nil {
		root = root.parent
	}
	preorder := preorderSubtree(root)
	focalPos := -1
	for i, t := range preorder {
		if t.id == focal.id {
			focalPos = i
			break
		}
	}
	if focalPos < 0 {
		return Line{AnchorShortID: root.shortID}
	}

	startPos := max(focalPos-N, 0)
	endPos := min(focalPos+N, len(preorder)-1)

	line := Line{AnchorShortID: preorder[startPos].shortID}
	// Content stops are the rest of the window past the leftmost.
	for i := startPos + 1; i <= endPos; i++ {
		line.Items = append(line.Items, LineItem{
			Kind:    LineItemStop,
			ShortID: preorder[i].shortID,
		})
	}
	// Trailing terminating elision when the +N walk continues past
	// the row's last visible stop. If the focal sits at or past the
	// preorder's last position, the row terminates at the focal —
	// no trailing dots.
	if endPos < len(preorder)-1 {
		line.Items = append(line.Items, LineItem{Kind: LineItemElisionTerminating})
	}
	return line
}

// focalPathSubgraph returns the LCA of focals and the IDs of every
// node on the path from the LCA down to each focal. Focals must
// share a project root; otherwise the function returns (nil, nil).
//
// The subgraph is the structural skeleton used by the multi-focal
// tree-map mode (project/2026-04-27-graph-row-merging.md): every
// rendered node in that mode comes from this set, every fork point
// lives within it, and non-focal-bearing branches off the LCA's
// subtree are excluded.
func focalPathSubgraph(focals []*graphTask) (*graphTask, map[int64]bool) {
	if len(focals) == 0 {
		return nil, nil
	}
	lca := focals[0]
	for i := 1; i < len(focals); i++ {
		lca = lcaPair(lca, focals[i])
		if lca == nil {
			return nil, nil
		}
	}
	ids := map[int64]bool{}
	for _, f := range focals {
		for cur := f; cur != nil; cur = cur.parent {
			ids[cur.id] = true
			if cur.id == lca.id {
				break
			}
		}
	}
	return lca, ids
}

// subgraphForkPoints returns the subgraph nodes that have two or
// more in-subgraph children, in tree (preorder) order. These are
// the divergence points where rows split in the multi-focal tree-
// map mode.
func subgraphForkPoints(ids map[int64]bool, lca *graphTask) []*graphTask {
	if lca == nil || !ids[lca.id] {
		return nil
	}
	var forks []*graphTask
	var visit func(t *graphTask)
	visit = func(t *graphTask) {
		if !ids[t.id] {
			return
		}
		inCount := 0
		for _, c := range t.children {
			if ids[c.id] {
				inCount++
			}
		}
		if inCount >= 2 {
			forks = append(forks, t)
		}
		for _, c := range t.children {
			visit(c)
		}
	}
	visit(lca)
	return forks
}

// buildMultiFocalRows returns the rows for a multi-focal cluster
// under the new tree-map mode (project/2026-04-27-graph-row-
// merging.md). Each row is a maximal linear chain through the
// focal-path subgraph between fork points; every row's leftmost
// is depth-aligned and rendered as the curve target for any
// incoming branch curve. Rows beyond the topmost carry a
// ParentShortID identifying the parent's rendered position so the
// layout can draw the branch curve.
//
// All focals must share a project root (cluster invariant). The
// topmost row's leftmost is the LCA of all focals; sub-rows
// branch off fork points (subgraph nodes with ≥2 in-subgraph
// children). The same-parent-siblings carve-out keeps focal
// siblings sharing a parent on one row, with ≤2 non-focal
// siblings rendered inline and ≥3 collapsed to a mid-line broken-
// line elision.
func buildMultiFocalRows(w *graphWorld, focals []*graphTask, N int) []Line {
	if len(focals) == 0 {
		return nil
	}
	lca, subgraph := focalPathSubgraph(focals)
	if lca == nil {
		return nil
	}
	focalSet := map[int64]bool{}
	for _, f := range focals {
		focalSet[f.id] = true
	}

	// LIFO stack so sub-rows emit in tree-preorder. buildOneRow
	// returns sub-rows in chain-encounter order (shallow forks first);
	// pushing them and popping from the end visits deeper sub-forks
	// before their shallower siblings, matching tree preorder.
	var rows []Line
	stack := []buildOneRowResult{{leftmost: lca, parentShort: ""}}
	for len(stack) > 0 {
		rt := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		row, subRows := buildOneRow(w, rt.leftmost, rt.parentShort, subgraph, focalSet, N)
		rows = append(rows, row)
		stack = append(stack, subRows...)
	}
	return rows
}

// buildOneRowResult is the per-row sub-row task carrying the next
// row's leftmost and parent.
type buildOneRowResult struct {
	leftmost    *graphTask
	parentShort string
}

// buildOneRow constructs a single row starting at leftmost, walking
// the subgraph along the inline child at each fork point until it
// hits a leaf or a same-parent-siblings carve-out. Returns the
// constructed Line plus the sub-row tasks for non-inline subgraph
// children encountered along the chain. Sub-rows are returned in
// tree (preorder) order so the caller can append them to the row
// queue.
func buildOneRow(
	_ *graphWorld,
	leftmost *graphTask,
	parentShort string,
	subgraph map[int64]bool,
	focalSet map[int64]bool,
	N int,
) (Line, []buildOneRowResult) {
	excluded := map[int64]bool{}
	chainIDs := map[int64]bool{leftmost.id: true}
	var subRows []buildOneRowResult
	cur := leftmost
	for {
		var inSubKids, focalLeafKids, deeperKids []*graphTask
		for _, c := range cur.children {
			if !subgraph[c.id] {
				continue
			}
			inSubKids = append(inSubKids, c)
			if focalSet[c.id] {
				focalLeafKids = append(focalLeafKids, c)
			} else {
				deeperKids = append(deeperKids, c)
			}
		}
		if len(inSubKids) == 0 {
			break
		}
		// Carve-out: ≥2 focal-leaf-children share cur's row. Deeper
		// children (if any) become sub-rows branching off cur.
		if len(focalLeafKids) >= 2 {
			for _, c := range deeperKids {
				excluded[c.id] = true
				subRows = append(subRows, buildOneRowResult{
					leftmost: c, parentShort: cur.shortID,
				})
			}
			break
		}
		// No carve-out at this node. The spine takes the first in-
		// subgraph child; remaining in-subgraph children (if any)
		// become sub-rows branching off cur. The chain continues
		// through the spine child so row 0 carries the LCA chain
		// down to a carve-out or leaf, rather than stranding the
		// LCA on its own row.
		next := inSubKids[0]
		for _, c := range inSubKids[1:] {
			excluded[c.id] = true
			subRows = append(subRows, buildOneRowResult{
				leftmost: c, parentShort: cur.shortID,
			})
		}
		chainIDs[next.id] = true
		cur = next
	}

	// Preorder walk of the row's contents. Chain nodes (the spine
	// plus any carve-out parent at its tail) descend into their
	// direct children so non-focal siblings render inline and
	// off-window siblings trigger leading/trailing elisions. Off-
	// chain children themselves don't recurse — their own subtrees
	// belong to a different cluster (or to a sub-row already carved
	// off as a separate Line) and are out of scope for this row.
	var rowPreorder []*graphTask
	var visit func(t *graphTask)
	visit = func(t *graphTask) {
		if excluded[t.id] {
			return
		}
		rowPreorder = append(rowPreorder, t)
		if !chainIDs[t.id] {
			return
		}
		for _, c := range t.children {
			visit(c)
		}
	}
	visit(leftmost)

	line := Line{AnchorShortID: leftmost.shortID, ParentShortID: parentShort}

	// Identify focal positions on this row.
	var focalPositions []int
	for i, t := range rowPreorder {
		if focalSet[t.id] {
			focalPositions = append(focalPositions, i)
		}
	}
	if len(focalPositions) == 0 {
		// Row's chain ends at a fork point with no focal of its
		// own (its focals live on sub-rows). Items list is empty —
		// only the row's leftmost (anchor) renders, serving as the
		// curve target for sub-rows.
		return line, subRows
	}

	// Compute visible window: union of ±N around each focal in
	// preorder. Then apply the same-parent-siblings carve-out
	// collapse: if there are ≥3 non-focal direct siblings between
	// two focal siblings on a carve-out parent, drop those
	// positions so they elide regardless of windowing.
	visible := map[int]bool{}
	for _, p := range focalPositions {
		lo, hi := p-N, p+N
		if lo < 0 {
			lo = 0
		}
		if hi > len(rowPreorder)-1 {
			hi = len(rowPreorder) - 1
		}
		for i := lo; i <= hi; i++ {
			visible[i] = true
		}
	}
	collapsed := carveOutCollapseRanges(rowPreorder, focalSet)
	for _, rng := range collapsed {
		for i := rng.lo; i <= rng.hi; i++ {
			delete(visible, i)
		}
	}
	var visibleIdx []int
	for i := range visible {
		visibleIdx = append(visibleIdx, i)
	}
	sort.Ints(visibleIdx)
	if len(visibleIdx) == 0 {
		return line, subRows
	}

	// Leading broken-line elision: when the first visible content
	// stop sits past position 1 (the leftmost's first child).
	if visibleIdx[0] > 1 {
		line.Items = append(line.Items, LineItem{Kind: LineItemElisionBroken})
	}
	// Walk visible indices, emitting stops and broken-line elisions
	// for internal gaps. Skip the leftmost (pos 0) — it's the
	// anchor.
	prev := -1
	for _, idx := range visibleIdx {
		if idx == 0 {
			prev = idx
			continue
		}
		if prev >= 1 && idx > prev+1 {
			line.Items = append(line.Items, LineItem{Kind: LineItemElisionBroken})
		}
		line.Items = append(line.Items, LineItem{
			Kind:    LineItemStop,
			ShortID: rowPreorder[idx].shortID,
		})
		prev = idx
	}
	// Trailing terminating elision: visible window doesn't reach
	// the row's last preorder position.
	if visibleIdx[len(visibleIdx)-1] < len(rowPreorder)-1 {
		line.Items = append(line.Items, LineItem{Kind: LineItemElisionTerminating})
	}
	return line, subRows
}

// carveOutCollapseRanges identifies non-focal sibling runs of ≥3
// between two focal-leaf siblings sharing the same parent within
// rowPreorder. The returned ranges are positions inside
// rowPreorder; the caller drops these from the visible window and
// inserts a single broken-line elision marker between the
// surrounding focal stops.
func carveOutCollapseRanges(rowPreorder []*graphTask, focalSet map[int64]bool) []struct{ lo, hi int } {
	posByID := make(map[int64]int, len(rowPreorder))
	for i, t := range rowPreorder {
		posByID[t.id] = i
	}
	// For each parent that has ≥2 focal-leaf children present in
	// rowPreorder, walk between adjacent focal siblings and look
	// for runs of ≥3 non-focal siblings.
	var out []struct{ lo, hi int }
	parentsSeen := map[int64]bool{}
	for _, t := range rowPreorder {
		p := t.parent
		if p == nil || parentsSeen[p.id] {
			continue
		}
		parentsSeen[p.id] = true
		var focalChildren []*graphTask
		for _, c := range p.children {
			if focalSet[c.id] {
				if _, ok := posByID[c.id]; ok {
					focalChildren = append(focalChildren, c)
				}
			}
		}
		if len(focalChildren) < 2 {
			continue
		}
		// Sort by sortKey (already stable in p.children order).
		sort.SliceStable(focalChildren, func(i, j int) bool {
			return focalChildren[i].sortKey < focalChildren[j].sortKey
		})
		for i := 0; i+1 < len(focalChildren); i++ {
			a, b := focalChildren[i], focalChildren[i+1]
			// Count direct non-focal siblings between a and b in
			// p.children order.
			var run []*graphTask
			between := false
			for _, c := range p.children {
				if c.id == a.id {
					between = true
					continue
				}
				if c.id == b.id {
					break
				}
				if !between {
					continue
				}
				if !focalSet[c.id] {
					run = append(run, c)
				}
			}
			if len(run) >= 3 {
				lo := posByID[run[0].id]
				hi := posByID[run[len(run)-1].id]
				out = append(out, struct{ lo, hi int }{lo, hi})
			}
		}
	}
	return out
}

// preorderSubtree returns every task rooted at root in DFS preorder.
// Used by the single-focal preorder window mode to address tasks by
// their position relative to the focal within the focal's project
// tree.
func preorderSubtree(root *graphTask) []*graphTask {
	var out []*graphTask
	var visit func(t *graphTask)
	visit = func(t *graphTask) {
		out = append(out, t)
		for _, c := range t.children {
			visit(c)
		}
	}
	visit(root)
	return out
}

// successorPreorder returns the next task in DFS preorder strictly
// after t, or nil. The walk descends into t's first child when one
// exists; otherwise it ascends until it finds an ancestor with a
// following sibling.
func successorPreorder(w *graphWorld, t *graphTask) *graphTask {
	if len(t.children) > 0 {
		return t.children[0]
	}
	for cur := t; cur != nil; cur = cur.parent {
		sibs := siblingList(w, cur)
		for i, s := range sibs {
			if s.id == cur.id && i+1 < len(sibs) {
				return sibs[i+1]
			}
		}
	}
	return nil
}
