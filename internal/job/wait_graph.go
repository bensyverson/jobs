package job

import (
	"fmt"
	"strings"
)

// waitKind names why one task waits on another.
type waitKind int

const (
	// waitBlockedBy is an explicit block edge: the task is blocked by the other.
	waitBlockedBy waitKind = iota
	// waitParentOf is containment: a parent closes only when its last open
	// child does, so it waits on each open child.
	waitParentOf
)

// waitEdge is one "waits on" step out of a node.
type waitEdge struct {
	to   string
	kind waitKind
}

// waitStep is one edge of a rendered cycle.
type waitStep struct {
	from string
	waitEdge
}

// waitGraph is the "waits on" graph over the store plus edges not yet
// written. A cycle in it is a permanent deadlock: every task on it waits for
// another that cannot finish first. `block add` and `import` both refuse an
// edge that would close one, through this one check.
//
// Nodes are keyed by short ID; a task an import has not created yet has a
// key that no short ID can take, and a label for messages.
type waitGraph struct {
	tx      dbtx
	pending map[string][]waitEdge
	unsaved map[string]string
}

func newWaitGraph(tx dbtx) *waitGraph {
	return &waitGraph{tx: tx, pending: map[string][]waitEdge{}, unsaved: map[string]string{}}
}

// addUnsaved registers a task that is not in the store yet under key, shown
// as label in any cycle it is part of.
func (g *waitGraph) addUnsaved(key, label string) {
	g.unsaved[key] = label
}

// addEdge records that from waits on to, ahead of the store knowing it.
func (g *waitGraph) addEdge(from, to string, kind waitKind) {
	g.pending[from] = append(g.pending[from], waitEdge{to: to, kind: kind})
}

// blockCycle reports whether blocking blocked on blocker would close a cycle,
// and if so renders it, starting with the new edge — e.g. "A blocked by B,
// B parent of C, C blocked by A".
func (g *waitGraph) blockCycle(blocked, blocker string) (string, bool, error) {
	path, found, err := g.path(blocker, blocked)
	if err != nil || !found {
		return "", false, err
	}
	steps := append([]waitStep{{from: blocked, waitEdge: waitEdge{to: blocker, kind: waitBlockedBy}}}, path...)
	parts := make([]string, len(steps))
	for i, s := range steps {
		verb := "blocked by"
		if s.kind == waitParentOf {
			verb = "parent of"
		}
		parts[i] = g.name(s.from) + " " + verb + " " + g.name(s.to)
	}
	return strings.Join(parts, ", "), true, nil
}

func (g *waitGraph) name(key string) string {
	if label, ok := g.unsaved[key]; ok {
		return label
	}
	return key
}

// path finds a chain of waits from start to target by depth-first search. A
// start equal to target is found with an empty path.
func (g *waitGraph) path(start, target string) ([]waitStep, bool, error) {
	visited := map[string]bool{}
	var walk func(node string) ([]waitStep, bool, error)
	walk = func(node string) ([]waitStep, bool, error) {
		if node == target {
			return nil, true, nil
		}
		if visited[node] {
			return nil, false, nil
		}
		visited[node] = true
		edges, err := g.successors(node)
		if err != nil {
			return nil, false, err
		}
		for _, e := range edges {
			rest, found, err := walk(e.to)
			if err != nil {
				return nil, false, err
			}
			if found {
				return append([]waitStep{{from: node, waitEdge: e}}, rest...), true, nil
			}
		}
		return nil, false, nil
	}
	return walk(start)
}

// successors lists what node waits on: pending edges first, then the store's
// blockers, then its open children in sibling order.
func (g *waitGraph) successors(node string) ([]waitEdge, error) {
	out := append([]waitEdge(nil), g.pending[node]...)
	if _, ok := g.unsaved[node]; ok {
		return out, nil
	}
	blockers, err := g.shortIDs(`
		SELECT bt.short_id FROM blocks b
		JOIN tasks t ON t.id = b.blocked_id
		JOIN tasks bt ON bt.id = b.blocker_id
		WHERE t.short_id = ? AND bt.deleted_at IS NULL
		ORDER BY bt.short_id`, node)
	if err != nil {
		return nil, err
	}
	for _, id := range blockers {
		out = append(out, waitEdge{to: id, kind: waitBlockedBy})
	}
	children, err := g.shortIDs(`
		SELECT c.short_id FROM tasks c
		JOIN tasks p ON p.id = c.parent_id
		WHERE p.short_id = ? AND `+openChildFilter("c")+`
		ORDER BY c.sort_key`, node)
	if err != nil {
		return nil, err
	}
	for _, id := range children {
		out = append(out, waitEdge{to: id, kind: waitParentOf})
	}
	return out, nil
}

func (g *waitGraph) shortIDs(query, arg string) ([]string, error) {
	rows, err := g.tx.Query(query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// circularError is the message both callers share for a refused edge.
func circularError(prefix, chain string) error {
	return fmt.Errorf("%s: would create a circular dependency: %s", prefix, chain)
}
