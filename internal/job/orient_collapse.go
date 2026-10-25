package job

import "slices"

// collapseClosedWork sets the elided view's shapes on an assembled tree,
// after the completion-note breadcrumb is placed. A closed leaf becomes one
// line. A closed container whose whole subtree is closed becomes its desc
// and leaf counts, keeping only the child rows on the path to the
// breadcrumb. Finished work stays visible as a count without costing a row
// per leaf. The target, and any subtree holding it, is never collapsed.
func collapseClosedWork(n *OrientNode) {
	if !n.Target && isClosedStatus(n.Task.Status) {
		if len(n.Children) == 0 {
			if n.CompletionNote == "" {
				n.Shape = OrientShapeLine
				n.Labels, n.Blocks = nil, nil
			}
			return
		}
		if subtreeSettled(n) {
			n.Shape = OrientShapeCollapsed
			n.DoneLeaves, n.CanceledLeaves = countClosedLeaves(n)
			var kept []*OrientNode
			for _, c := range n.Children {
				if carriesBreadcrumb(c) {
					kept = append(kept, c)
				}
			}
			n.Children = kept
		}
	}
	for _, c := range n.Children {
		collapseClosedWork(c)
	}
}

// subtreeSettled reports whether every descendant of n is closed and none is
// the target.
func subtreeSettled(n *OrientNode) bool {
	for _, c := range n.Children {
		if c.Target || !isClosedStatus(c.Task.Status) || !subtreeSettled(c) {
			return false
		}
	}
	return true
}

// countClosedLeaves counts the done and canceled leaves under n.
func countClosedLeaves(n *OrientNode) (done, canceled int) {
	for _, c := range n.Children {
		if len(c.Children) > 0 {
			d, k := countClosedLeaves(c)
			done, canceled = done+d, canceled+k
			continue
		}
		switch c.Task.Status {
		case "done":
			done++
		case "canceled":
			canceled++
		}
	}
	return done, canceled
}

// carriesBreadcrumb reports whether n or a descendant holds the completion
// note.
func carriesBreadcrumb(n *OrientNode) bool {
	if n.CompletionNote != "" {
		return true
	}
	return slices.ContainsFunc(n.Children, carriesBreadcrumb)
}
