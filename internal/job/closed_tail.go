package job

import (
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ParentInfo carries the breadcrumb data needed to label a closed-tail row
// with its parent context. Resolved once per render via
// LoadParentBreadcrumbs and passed to RenderClosedTail.
type ParentInfo struct {
	ShortID string
	Title   string
}

// LoadParentBreadcrumbs returns a map keyed by task numeric ID -> parent
// short-id + title, for the parents of the given closed-tail rows. Rows
// whose task has no parent (root) are skipped. Used by `ls` to label the
// "Recently closed" footer with each row's parent.
func LoadParentBreadcrumbs(db *sql.DB, rows []ClosedTailRow) (map[int64]ParentInfo, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	parentIDs := make(map[int64]bool)
	for _, r := range rows {
		if r.Task.ParentID != nil {
			parentIDs[*r.Task.ParentID] = true
		}
	}
	if len(parentIDs) == 0 {
		return map[int64]ParentInfo{}, nil
	}

	ids := make([]any, 0, len(parentIDs))
	placeholders := make([]string, 0, len(parentIDs))
	for id := range parentIDs {
		ids = append(ids, id)
		placeholders = append(placeholders, "?")
	}
	q := "SELECT id, short_id, title FROM tasks WHERE id IN (" + strings.Join(placeholders, ",") + ")"
	dbRows, err := db.Query(q, ids...)
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()
	out := make(map[int64]ParentInfo, len(parentIDs))
	for dbRows.Next() {
		var id int64
		var info ParentInfo
		if err := dbRows.Scan(&id, &info.ShortID, &info.Title); err != nil {
			return nil, err
		}
		out[id] = info
	}
	return out, dbRows.Err()
}

// RenderClosedTail writes the "Recently closed (N of M)" footer section.
// Section is omitted when len(rows) == 0. Each row carries a parent
// breadcrumb when omitBreadcrumb is false and the row's parent appears in
// parents (root tasks render without a breadcrumb).
func RenderClosedTail(w io.Writer, rows []ClosedTailRow, total int, parents map[int64]ParentInfo, omitBreadcrumb bool) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(w, "\nRecently closed (%d of %d):\n", len(rows), total)
	for _, row := range rows {
		glyph := "[x]"
		if row.Task.Status == "canceled" {
			glyph = "[-]"
		}
		fmt.Fprintf(w, "- %s `%s` %s", glyph, row.Task.ShortID, row.Task.Title)
		if !omitBreadcrumb && row.Task.ParentID != nil {
			if info, ok := parents[*row.Task.ParentID]; ok {
				fmt.Fprintf(w, " (in `%s` %s)", info.ShortID, info.Title)
			}
		}
		fmt.Fprintln(w)
	}
}

// RunListWithTail returns the open tree plus a closed-tail set in a single
// call. The open tree is hybrid in `--all` mode: live work renders as
// today, plus closed direct children of open parents render inline so
// local "what just got done here" stays in context. Closed tasks whose
// parent is also closed (or has no parent) belong in the flat
// "Recently closed" footer, exposed via ClosedTail. ClosedTotal is the
// unbounded total of footer-eligible closed tasks in scope so renderers
// can emit "N of M" headers.
func RunListWithTail(db *sql.DB, f ListFilter) (*ListResult, error) {
	if err := expireStaleClaims(db, f.Actor); err != nil {
		return nil, err
	}

	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}
	tree := buildTree(tasks)

	// IssuesOpen is computed against the full, unscoped forest — before
	// ParentID narrows to a subtree and before KindScope drops the other
	// kind's roots — so the trailer's backlog size never shifts with the
	// filter that happened to produce this result.
	issuesOpen := issuesOpenCount(tree)

	scoped := tree
	if f.ParentID != "" {
		parent := findNodeByShortID(tree, f.ParentID)
		if parent == nil {
			return nil, fmt.Errorf("task %q not found", f.ParentID)
		}
		scoped = parent.Children
	} else {
		scoped = filterRootsByKind(tree, f.KindScope)
	}

	blockedIDs, err := getBlockedTaskIDs(db)
	if err != nil {
		return nil, err
	}

	effectiveShowAll := f.ShowAll || f.ClaimedByActor != "" || f.Status != ""
	var open []*TaskNode
	if effectiveShowAll {
		open = filterTreeHybrid(scoped)
	} else {
		open = filterTree(scoped, false, blockedIDs)
	}
	if f.ClaimedByActor != "" {
		open = filterByClaimedActor(open, f.ClaimedByActor)
	}
	if f.Status != "" {
		open = filterByStatus(open, f.Status)
	}
	if f.Label != "" {
		labeledIDs, err := taskIDsWithLabel(db, f.Label)
		if err != nil {
			return nil, err
		}
		open = filterByLabel(open, labeledIDs)
	}
	if f.GrepPattern != "" {
		open = filterByGrep(open, f.GrepPattern)
	}

	tail, _, err := collectClosedTail(db, f)
	if err != nil {
		return nil, err
	}
	// Hybrid rule: closed tasks already rendered as inline children of an
	// open parent should not duplicate into the footer. Drop them from the
	// tail before applying the cap so the M count is "leftover closed", not
	// "all closed".
	inTree := collectTaskIDs(open)
	tail = filterClosedTailExclude(tail, inTree)

	// A closed-tail row is rarely a root itself, so KindScope can't be
	// tested on the row's own Task the way filterRootsByKind tests a root —
	// it has to walk up to the row's root ancestor instead. Scoping here,
	// before total/cap are computed, keeps the "N of M" footer counting the
	// kind being shown rather than both kinds combined.
	if f.ParentID == "" && f.KindScope != ListKindScopeAny {
		rootKind := rootKindByTask(tasks)
		wantIssue := f.KindScope == ListKindScopeIssues
		tail = filterClosedTailByKind(tail, rootKind, wantIssue)
	}

	total := len(tail)
	cap := f.ClosedTailCap
	if cap == 0 {
		cap = DefaultClosedTailCap
	}
	if cap > 0 && len(tail) > cap {
		tail = tail[:cap]
	}

	return &ListResult{Open: open, ClosedTail: tail, ClosedTotal: total, IssuesOpen: issuesOpen}, nil
}

// issuesOpenCount reports the open (not done, not canceled) task count
// across every issue-tree root in tree, root included, or nil if tree has
// no issue-tree root. tree must be the full, unscoped forest — see the
// ListResult.IssuesOpen doc comment for why this is independent of any
// other ListFilter narrowing.
func issuesOpenCount(tree []*TaskNode) *int {
	hasIssueRoots := false
	open := 0
	for _, root := range tree {
		if !root.Task.Kind.IsIssue() {
			continue
		}
		hasIssueRoots = true
		open += countOpenInSubtree(root)
	}
	if !hasIssueRoots {
		return nil
	}
	return &open
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

// rootKindByTask maps every task ID to the tree kind of its root ancestor,
// walking the parent chain (kind is root-only, see kind.go). tasks must
// already be loaded (RunListWithTail has it in hand), so this needs no
// database round-trip of its own.
func rootKindByTask(tasks []*Task) map[int64]TreeKind {
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
	return out
}

// filterClosedTailByKind keeps only rows whose root ancestor's kind matches
// wantIssue.
func filterClosedTailByKind(rows []ClosedTailRow, rootKind map[int64]TreeKind, wantIssue bool) []ClosedTailRow {
	out := rows[:0:0]
	for _, r := range rows {
		if rootKind[r.Task.ID].IsIssue() == wantIssue {
			out = append(out, r)
		}
	}
	return out
}

// filterTreeHybrid is the open-tree filter used when ShowAll is in effect.
// It keeps every node that is open (status not done/canceled) plus every
// closed direct child of a kept open node, so local "what just got done
// here" stays visible in context. Closed nodes are dropped unless they
// have a kept descendant (e.g. a reopened task under a since-closed
// parent). Blocked and claimed tasks pass through — `--all` shows them.
func filterTreeHybrid(nodes []*TaskNode) []*TaskNode {
	var out []*TaskNode
	for _, n := range nodes {
		closed := n.Task.Status == "done" || n.Task.Status == "canceled"
		keptChildren := filterTreeHybrid(n.Children)
		if closed {
			if len(keptChildren) > 0 {
				out = append(out, &TaskNode{Task: n.Task, Children: keptChildren})
			}
			continue
		}
		merged := keptChildren
		keptIDs := make(map[int64]bool, len(keptChildren))
		for _, kc := range keptChildren {
			keptIDs[kc.Task.ID] = true
		}
		for _, child := range n.Children {
			cClosed := child.Task.Status == "done" || child.Task.Status == "canceled"
			if cClosed && !keptIDs[child.Task.ID] {
				merged = append(merged, &TaskNode{Task: child.Task})
			}
		}
		out = append(out, &TaskNode{Task: n.Task, Children: merged})
	}
	return out
}

// collectTaskIDs walks a forest and returns the set of task IDs reachable.
func collectTaskIDs(nodes []*TaskNode) map[int64]bool {
	out := make(map[int64]bool)
	var walk func([]*TaskNode)
	walk = func(ns []*TaskNode) {
		for _, n := range ns {
			out[n.Task.ID] = true
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// filterClosedTailExclude drops rows whose task ID is present in the
// excluded set. Order is preserved.
func filterClosedTailExclude(rows []ClosedTailRow, excluded map[int64]bool) []ClosedTailRow {
	if len(excluded) == 0 {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if !excluded[r.Task.ID] {
			out = append(out, r)
		}
	}
	return out
}

// collectClosedTail returns the closed (done or canceled) tasks in scope of
// f, sorted closed-at descending. No cap is applied here — the caller
// (RunListWithTail) excludes closed tasks already rendered in the open
// tree before applying ListFilter.ClosedTailCap, so M reflects leftover
// closed rather than all closed.
func collectClosedTail(db *sql.DB, f ListFilter) ([]ClosedTailRow, int, error) {
	// Resolve subtree-scope task IDs first, if a parent is given.
	var subtreeIDs map[int64]bool
	if f.ParentID != "" {
		ids, err := subtreeTaskIDs(db, f.ParentID)
		if err != nil {
			return nil, 0, err
		}
		subtreeIDs = ids
	}

	// Status filter: closed tail is implicitly status IN (done, canceled).
	// If the caller passes Status=done or Status=canceled, narrow further.
	// Status=open / available / claimed produces an empty tail by definition.
	statusClause := "t.status IN ('done', 'canceled')"
	if f.Status != "" {
		switch f.Status {
		case "done":
			statusClause = "t.status = 'done'"
		case "canceled":
			statusClause = "t.status = 'canceled'"
		default:
			return nil, 0, nil
		}
	}

	q := `
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status,
		       t.sort_key, t.claimed_by, t.claim_expires_at, t.completion_note,
		       t.created_at, t.updated_at, t.deleted_at, t.kind,
		       e.created_at AS closed_at, e.actor AS closer
		FROM tasks t
		JOIN events e ON e.task_id = t.id
		WHERE t.deleted_at IS NULL
		  AND ` + statusClause + `
		  AND e.event_type IN ('done', 'canceled')
		  AND e.id = (
		    SELECT MAX(e2.id) FROM events e2
		    WHERE e2.task_id = t.id
		      AND e2.event_type IN ('done', 'canceled')
		  )
		ORDER BY e.created_at DESC, e.id DESC
	`
	rows, err := db.Query(q)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var labeledIDs map[int64]bool
	if f.Label != "" {
		labeledIDs, err = taskIDsWithLabel(db, f.Label)
		if err != nil {
			return nil, 0, err
		}
	}

	grepLower := strings.ToLower(f.GrepPattern)

	var collected []ClosedTailRow
	for rows.Next() {
		t := &Task{}
		var parentID sql.NullInt64
		var claimedBy sql.NullString
		var claimExpiresAt sql.NullInt64
		var completionNote sql.NullString
		var deletedAt sql.NullInt64
		var closedAt int64
		var closer string
		if err := rows.Scan(
			&t.ID, &t.ShortID, &parentID, &t.Title, &t.Description,
			&t.Status, &t.SortKey, &claimedBy, &claimExpiresAt,
			&completionNote, &t.CreatedAt, &t.UpdatedAt, &deletedAt, &t.Kind,
			&closedAt, &closer,
		); err != nil {
			return nil, 0, err
		}
		if parentID.Valid {
			pid := parentID.Int64
			t.ParentID = &pid
		}
		if claimedBy.Valid {
			cb := claimedBy.String
			t.ClaimedBy = &cb
		}
		if claimExpiresAt.Valid {
			ce := claimExpiresAt.Int64
			t.ClaimExpiresAt = &ce
		}
		if completionNote.Valid {
			cn := completionNote.String
			t.CompletionNote = &cn
		}
		if deletedAt.Valid {
			da := deletedAt.Int64
			t.DeletedAt = &da
		}

		if subtreeIDs != nil && !subtreeIDs[t.ID] {
			continue
		}
		if labeledIDs != nil && !labeledIDs[t.ID] {
			continue
		}
		if grepLower != "" && !strings.Contains(strings.ToLower(t.Title), grepLower) {
			continue
		}
		if f.ClaimedByActor != "" && closer != f.ClaimedByActor {
			continue
		}
		if f.ClosedTailSinceUnix != 0 && closedAt < f.ClosedTailSinceUnix {
			continue
		}

		collected = append(collected, ClosedTailRow{Task: t, ClosedAt: closedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// Already ORDER BY closed_at DESC from SQL, but stable-sort defensively
	// in case the DB ever returns out-of-order rows.
	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].ClosedAt > collected[j].ClosedAt
	})

	return collected, len(collected), nil
}

// subtreeTaskIDs returns the set of task IDs in the subtree rooted at the
// given short id, including the root itself.
func subtreeTaskIDs(db *sql.DB, parentShortID string) (map[int64]bool, error) {
	root, err := GetTaskByShortID(db, parentShortID)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return nil, fmt.Errorf("task %q not found", parentShortID)
	}
	ids := map[int64]bool{root.ID: true}
	frontier := []int64{root.ID}
	for len(frontier) > 0 {
		next := frontier
		frontier = nil
		// Build a query with a placeholder per id.
		placeholders := strings.Repeat("?,", len(next))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(next))
		for i, id := range next {
			args[i] = id
		}
		rows, err := db.Query(
			"SELECT id FROM tasks WHERE deleted_at IS NULL AND parent_id IN ("+placeholders+")",
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !ids[id] {
				ids[id] = true
				frontier = append(frontier, id)
			}
		}
		rows.Close()
	}
	return ids, nil
}
