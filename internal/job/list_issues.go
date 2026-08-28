package job

import "database/sql"

// IssueOpenCount reports whether the database has any issue-tree roots and,
// if so, how many tasks within those trees (the root itself included) carry
// an open status (not done, not canceled). It is independent of any `ls`
// filter — the `ls` trailer names the unfiltered issue backlog so the
// pointer at `job ls --issues` stays a stable count, not one that shifts
// with `--label` or `--mine` on the invocation that printed it.
func IssueOpenCount(db *sql.DB) (hasIssueRoots bool, open int, err error) {
	tasks, err := loadAllTasks(db)
	if err != nil {
		return false, 0, err
	}
	for _, root := range buildTree(tasks) {
		if !root.Task.Kind.IsIssue() {
			continue
		}
		hasIssueRoots = true
		open += countOpenInSubtree(root)
	}
	return hasIssueRoots, open, nil
}

func countOpenInSubtree(node *TaskNode) int {
	n := 0
	if node.Task.Status != "done" && node.Task.Status != "canceled" {
		n++
	}
	for _, c := range node.Children {
		n += countOpenInSubtree(c)
	}
	return n
}

// rootKindByTaskID maps every non-deleted task ID to the tree kind of its
// root ancestor. Kind is a root-only property (see kind.go), so a leaf's
// kind is whatever its root was last set to; this walks the parent chain to
// find it. Used to scope a flat list (the closed-tail footer) to the same
// task-tree/issue-tree split `ls` applies to the open forest, since a
// closed-tail row rarely is a root itself.
func rootKindByTaskID(db *sql.DB) (map[int64]TreeKind, error) {
	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	out := make(map[int64]TreeKind, len(tasks))
	for _, t := range tasks {
		cur := t
		for cur.ParentID != nil {
			parent, ok := byID[*cur.ParentID]
			if !ok {
				break
			}
			cur = parent
		}
		out[t.ID] = cur.Kind
	}
	return out, nil
}

// ScopeListResult narrows an already-computed ListResult to one tree kind,
// matching the split `ls` draws between the default forest (task-trees) and
// `ls --issues` (issue-trees). It is meaningful only for the unscoped
// forest — callers must not use it once a parent has pinned the scope to a
// single subtree, since every node there already shares one kind.
//
// The open forest is a top-level filter only: kind is root-only, so any
// node surviving RunListWithTail's other filters already carries the
// answer directly on its own Task. The closed tail needs rootKindByTaskID
// because a closed row is rarely a root itself. requestedCap re-applies the
// cap the caller actually asked for (0 meaning DefaultClosedTailCap, -1
// meaning no cap) now that the other kind's rows are gone, so the "N of M"
// footer stays accurate for the half being shown rather than inheriting a
// count that included both kinds.
func ScopeListResult(db *sql.DB, result *ListResult, issues bool, requestedCap int) error {
	openOut := result.Open[:0:0]
	for _, n := range result.Open {
		if n.Task.Kind.IsIssue() == issues {
			openOut = append(openOut, n)
		}
	}
	result.Open = openOut

	if len(result.ClosedTail) == 0 {
		return nil
	}
	rootKind, err := rootKindByTaskID(db)
	if err != nil {
		return err
	}
	tail := result.ClosedTail[:0:0]
	for _, r := range result.ClosedTail {
		if rootKind[r.Task.ID].IsIssue() == issues {
			tail = append(tail, r)
		}
	}
	result.ClosedTotal = len(tail)
	cap := requestedCap
	if cap == 0 {
		cap = DefaultClosedTailCap
	}
	if cap > 0 && len(tail) > cap {
		tail = tail[:cap]
	}
	result.ClosedTail = tail
	return nil
}
