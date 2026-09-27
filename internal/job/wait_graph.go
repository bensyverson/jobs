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

// Deadlock is one closed loop already sitting in the wait graph: a chain of
// open tasks that can never close because each waits on the next. block add
// and import both refuse to write an edge that would create one, but a store
// written before that check landed may already hold one — FindDeadlocks is
// how it surfaces after the fact.
type Deadlock struct {
	// Chain names the loop in the same wording circularError/blockCycle use,
	// e.g. "b blocked by a, a blocked by b".
	Chain string
	// Blocked and Blocker are one "blocked by" edge on the loop — a loop
	// always has at least one, since containment alone (a parent waiting on
	// an open child) cannot cycle on its own.
	Blocked string
	Blocker string
}

// Fix is the `job block remove` invocation that breaks this loop.
func (d Deadlock) Fix() string {
	return fmt.Sprintf("job block remove %s by %s", d.Blocked, d.Blocker)
}

// FindDeadlocks scans every open task's wait edges for a cycle: one DFS over
// the whole graph, coloring each task white/gray/black. A back edge to a
// gray ancestor closes a loop, which is read straight off the DFS stack.
// Once a node goes black its edges are never walked again, so the whole scan
// costs one pass over the open tasks and their blocks/containment edges —
// the same successors() that blockCycle uses for a single new edge, run from
// every node instead of just one.
//
// A loop found this way is reported exactly once: a specific edge is only
// ever examined during its "from" node's single visit, so the same loop
// cannot surface twice from two different starting nodes. It is still
// rotated to start at its lexicographically smallest node before rendering,
// so the wording never depends on scan order.
func FindDeadlocks(tx dbtx) ([]Deadlock, error) {
	g := newWaitGraph(tx)

	rows, err := tx.Query(`SELECT short_id FROM tasks WHERE ` + openChildFilter("") + ` ORDER BY short_id`)
	if err != nil {
		return nil, err
	}
	var nodes []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		nodes = append(nodes, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	const (
		white = iota
		gray
		black
	)
	color := make(map[string]int, len(nodes))
	var onStack []string
	var edgeStack []waitEdge // edgeStack[i] is the edge from onStack[i] to onStack[i+1]
	seen := map[string]bool{}
	var out []Deadlock

	var visit func(node string) error
	visit = func(node string) error {
		color[node] = gray
		onStack = append(onStack, node)
		edges, err := g.successors(node)
		if err != nil {
			return err
		}
		for _, e := range edges {
			switch color[e.to] {
			case white:
				edgeStack = append(edgeStack, e)
				if err := visit(e.to); err != nil {
					return err
				}
				edgeStack = edgeStack[:len(edgeStack)-1]
			case gray:
				d := renderDeadlock(onStack, edgeStack, e)
				if !seen[d.Chain] {
					seen[d.Chain] = true
					out = append(out, d)
				}
			}
		}
		onStack = onStack[:len(onStack)-1]
		color[node] = black
		return nil
	}

	for _, n := range nodes {
		if color[n] == white {
			if err := visit(n); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// renderDeadlock turns a DFS back edge into a Deadlock. closing.to is the
// gray ancestor the current top of onStack points back to; onStack[idx:]
// plus closing are the loop's nodes and edges, in stack order. The result is
// rotated to start at the loop's lexicographically smallest node so the same
// loop always renders identically regardless of which node the scan reached
// it from first.
func renderDeadlock(onStack []string, edgeStack []waitEdge, closing waitEdge) Deadlock {
	idx := len(onStack) - 1
	for onStack[idx] != closing.to {
		idx--
	}
	steps := make([]waitStep, 0, len(onStack)-idx)
	for i := idx; i < len(onStack)-1; i++ {
		steps = append(steps, waitStep{from: onStack[i], waitEdge: edgeStack[i]})
	}
	steps = append(steps, waitStep{from: onStack[len(onStack)-1], waitEdge: closing})

	minAt := 0
	for i, s := range steps {
		if s.from < steps[minAt].from {
			minAt = i
		}
	}
	rotated := make([]waitStep, len(steps))
	for i := range steps {
		rotated[i] = steps[(minAt+i)%len(steps)]
	}

	parts := make([]string, len(rotated))
	var blocked, blocker string
	for i, s := range rotated {
		verb := "blocked by"
		if s.kind == waitParentOf {
			verb = "parent of"
		}
		parts[i] = s.from + " " + verb + " " + s.to
		if blocked == "" && s.kind == waitBlockedBy {
			blocked, blocker = s.from, s.to
		}
	}
	return Deadlock{Chain: strings.Join(parts, ", "), Blocked: blocked, Blocker: blocker}
}
