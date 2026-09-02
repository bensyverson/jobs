package signals

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// graph.go owns the in-memory task world (graphTask / graphWorld /
// loadGraphWorld) and the focal-selection helpers (pickFocals,
// preorderAll, globalNext) shared across signal builders. The
// public Subway model is assembled in subway.go.

// ------------------------------------------------------------------
// In-memory world
// ------------------------------------------------------------------

type graphTask struct {
	id       int64
	shortID  string
	title    string
	status   string
	actor    string
	parentID *int64
	sortKey  string
	parent   *graphTask
	children []*graphTask
	// openBlockers counts upstream blocker tasks that are not yet
	// done or canceled. When > 0 the task renders as blocked even if
	// its own status is "available".
	openBlockers int
	// blockerIDs is the full list of upstream blocker task IDs
	// (regardless of resolution status) — the graph only emits an
	// edge for pairs where both endpoints are rendered.
	blockerIDs []int64
}

type graphWorld struct {
	byID  map[int64]*graphTask
	roots []*graphTask
}

func loadGraphWorld(ctx context.Context, db *sql.DB) (*graphWorld, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, short_id, title, status,
		       COALESCE(claimed_by, ''),
		       parent_id, sort_key
		FROM tasks
		WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	w := &graphWorld{byID: make(map[int64]*graphTask)}
	for rows.Next() {
		t := &graphTask{}
		if err := rows.Scan(&t.id, &t.shortID, &t.title, &t.status,
			&t.actor, &t.parentID, &t.sortKey); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		w.byID[t.id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range w.byID {
		if t.parentID == nil {
			w.roots = append(w.roots, t)
			continue
		}
		if p, ok := w.byID[*t.parentID]; ok {
			t.parent = p
			p.children = append(p.children, t)
		}
	}
	sortBySortKey(w.roots)
	for _, t := range w.byID {
		sortBySortKey(t.children)
	}

	// Blocker edges. A blocker is "open" when its own status is not
	// done/canceled; that flag drives the blocked visual state.
	blockRows, err := db.QueryContext(ctx, `
		SELECT b.blocker_id, b.blocked_id, t.status
		FROM blocks b
		JOIN tasks t ON t.id = b.blocker_id
		WHERE t.deleted_at IS NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("query blocks: %w", err)
	}
	defer blockRows.Close()
	for blockRows.Next() {
		var blockerID, blockedID int64
		var blockerStatus string
		if err := blockRows.Scan(&blockerID, &blockedID, &blockerStatus); err != nil {
			return nil, fmt.Errorf("scan block: %w", err)
		}
		blocked := w.byID[blockedID]
		if blocked == nil {
			continue
		}
		blocked.blockerIDs = append(blocked.blockerIDs, blockerID)
		if blockerStatus != "done" && blockerStatus != "canceled" {
			blocked.openBlockers++
		}
	}
	if err := blockRows.Err(); err != nil {
		return nil, err
	}
	return w, nil
}

func sortBySortKey(ts []*graphTask) {
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].sortKey != ts[j].sortKey {
			return ts[i].sortKey < ts[j].sortKey
		}
		return ts[i].id < ts[j].id
	})
}

// ------------------------------------------------------------------
// Focal selection
// ------------------------------------------------------------------

// pickFocals returns the tasks each lane should be centered on. When
// any tasks are claimed, every claim becomes its own lane; otherwise
// the lane is anchored on the globally-next available leaf.
//
// Focals are leaves only (project/2026-04-27-graph-row-merging.md,
// invariant 1). The leaf-frontier rule normally prevents parent
// claims, but legacy data and non-CLI clients can still produce
// them; the len(t.children) > 0 guard is the safety net so the
// multi-focal mode never tries to render a parent as a focal stop.
func pickFocals(w *graphWorld) []*graphTask {
	var active []*graphTask
	for _, t := range w.byID {
		if t.status != "claimed" {
			continue
		}
		if len(t.children) > 0 {
			continue
		}
		active = append(active, t)
	}
	if len(active) > 0 {
		// Stable order: preorder position in the project tree so
		// visually-close lanes sit next to each other.
		preorder := preorderAll(w)
		pos := make(map[int64]int, len(preorder))
		for i, t := range preorder {
			pos[t.id] = i
		}
		sort.SliceStable(active, func(i, j int) bool {
			return pos[active[i].id] < pos[active[j].id]
		})
		return active
	}
	if next := globalNext(w); next != nil {
		return []*graphTask{next}
	}
	return nil
}

// preorderAll returns every task in DFS-preorder, rooted at the
// project roots sorted by declaration order. Used for stable lane
// ordering.
func preorderAll(w *graphWorld) []*graphTask {
	var out []*graphTask
	var visit func(t *graphTask)
	visit = func(t *graphTask) {
		out = append(out, t)
		for _, c := range t.children {
			visit(c)
		}
	}
	for _, r := range w.roots {
		visit(r)
	}
	return out
}

// globalNext mirrors the `Next:` computation in job status: the
// first preorder available leaf with no open blockers.
func globalNext(w *graphWorld) *graphTask {
	for _, t := range preorderAll(w) {
		if t.status != "available" {
			continue
		}
		if len(t.children) > 0 {
			continue
		}
		if t.openBlockers > 0 {
			continue
		}
		return t
	}
	return nil
}

// siblingList returns the ordered sibling list t belongs to (either
// its parent's children or the root list).
func siblingList(w *graphWorld, t *graphTask) []*graphTask {
	if t.parent != nil {
		return t.parent.children
	}
	return w.roots
}
