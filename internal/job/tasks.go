package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type ClosedResult struct {
	ShortID             string
	Title               string
	Note                string
	CascadeClosed       []string
	AutoClosedAncestors []AutoClosedAncestor
}

// AutoClosedAncestor names an ancestor that was auto-closed by the
// leaf-frontier cascade (when its last open child closed). Walking from
// the closer upward; the first entry is the direct parent. Status is
// "done" or "canceled" — the destination the cascade chose for this
// ancestor based on its sibling mix.
type AutoClosedAncestor struct {
	ShortID string
	Title   string
	Status  string
}

// AddResult carries the outcome of RunAdd. ShortID is always set on
// success; AutoReleasedParent is set when the add triggered an auto-release
// of a claimed parent (leaf-frontier semantics — a parent with an open
// child has no executable work of its own).
type AddResult struct {
	ShortID             string
	AutoReleasedParent  string
	AutoReleasedByActor string
}

// RunAdd creates a task-kind node. Roots created this way are task-trees;
// use RunAddKind to create an issue root.
func RunAdd(db *sql.DB, parentShortID, title, desc, beforeShortID string, labels []string, actor string) (*AddResult, error) {
	return RunAddKind(db, parentShortID, title, desc, beforeShortID, labels, actor, KindTask)
}

// RunAddKind is RunAdd with the new task's tree kind explicit. Only a root
// may be created as an issue-tree — kind is a property of the root, so
// asking for one on a child is an error rather than a silent downgrade.
func RunAddKind(db *sql.DB, parentShortID, title, desc, beforeShortID string, labels []string, actor string, kind TreeKind) (*AddResult, error) {
	if kind != KindTask && kind != KindIssue {
		return nil, fmt.Errorf("invalid kind %q (want task|issue)", kind)
	}
	if kind.IsIssue() && parentShortID != "" {
		return nil, fmt.Errorf("kind %q is only valid on a root task; %q was given a parent", kind, title)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var parent *Task
	var parentID *int64
	if parentShortID != "" {
		p, err := GetTaskByShortID(tx, parentShortID)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, fmt.Errorf("task %q not found", parentShortID)
		}
		parent = p
		parentID = &p.ID
	}

	shortID, err := generateShortID(tx)
	if err != nil {
		return nil, err
	}

	var sortOrder int
	if beforeShortID != "" {
		beforeTask, err := GetTaskByShortID(tx, beforeShortID)
		if err != nil {
			return nil, err
		}
		if beforeTask == nil {
			return nil, fmt.Errorf("task %q not found", beforeShortID)
		}
		if (beforeTask.ParentID == nil) != (parentID == nil) {
			return nil, fmt.Errorf("task %q is not a sibling of the new task", beforeShortID)
		}
		if beforeTask.ParentID != nil && parentID != nil && *beforeTask.ParentID != *parentID {
			return nil, fmt.Errorf("task %q is not a sibling of the new task", beforeShortID)
		}
		sortOrder = beforeTask.SortOrder
		if parentID == nil {
			_, err = tx.Exec("UPDATE tasks SET sort_order = sort_order + 1 WHERE parent_id IS NULL AND sort_order >= ? AND deleted_at IS NULL", sortOrder)
		} else {
			_, err = tx.Exec("UPDATE tasks SET sort_order = sort_order + 1 WHERE parent_id = ? AND sort_order >= ? AND deleted_at IS NULL", *parentID, sortOrder)
		}
		if err != nil {
			return nil, err
		}
	} else {
		var maxSort sql.NullInt64
		if parentID == nil {
			err = tx.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE parent_id IS NULL AND deleted_at IS NULL").Scan(&maxSort)
		} else {
			err = tx.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE parent_id = ? AND deleted_at IS NULL", *parentID).Scan(&maxSort)
		}
		if err != nil {
			return nil, err
		}
		if maxSort.Valid {
			sortOrder = int(maxSort.Int64) + 1
		}
	}

	now := CurrentNowFunc().Unix()
	var taskID int64
	err = tx.QueryRow(`
		INSERT INTO tasks (short_id, parent_id, title, description, status, sort_order, created_at, updated_at, kind)
		VALUES (?, ?, ?, ?, 'available', ?, ?, ?, ?)
		RETURNING id
	`, shortID, parentID, title, desc, sortOrder, now, now, string(kind)).Scan(&taskID)
	if err != nil {
		return nil, err
	}

	eventParentID := ""
	if parentShortID != "" {
		eventParentID = parentShortID
	}
	createdDetail := map[string]any{
		"parent_id":   eventParentID,
		"title":       title,
		"description": desc,
		"sort_order":  sortOrder,
	}
	if kind.IsIssue() {
		createdDetail["kind"] = string(kind)
	}
	if err := recordEvent(tx, taskID, "created", actor, createdDetail); err != nil {
		return nil, err
	}

	if len(labels) > 0 {
		normalized, err := normalizeLabelNames(labels)
		if err != nil {
			return nil, err
		}
		if _, _, err := insertLabels(tx, taskID, normalized); err != nil {
			return nil, err
		}
		if err := recordEvent(tx, taskID, "labeled", actor, map[string]any{
			"names":    normalized,
			"existing": []string{},
		}); err != nil {
			return nil, err
		}
	}

	result := &AddResult{ShortID: shortID}

	// Leaf-frontier auto-release: adding an open child to a claimed parent
	// releases the parent's claim. The parent has no executable work of its
	// own — its work is in its children — so the lock has no referent.
	if parent != nil && parent.Status == "claimed" {
		prior := ""
		if parent.ClaimedBy != nil {
			prior = *parent.ClaimedBy
		}
		var priorExpires int64
		if parent.ClaimExpiresAt != nil {
			priorExpires = *parent.ClaimExpiresAt
		}
		if _, err := tx.Exec(
			"UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
			now, parent.ID,
		); err != nil {
			return nil, err
		}
		if err := recordEvent(tx, parent.ID, "released", actor, map[string]any{
			"auto_released":      true,
			"triggered_by_child": shortID,
			"was_claimed_by":     prior,
			"was_expires_at":     priorExpires,
		}); err != nil {
			return nil, err
		}
		result.AutoReleasedParent = parent.ShortID
		result.AutoReleasedByActor = prior
	}

	return result, tx.Commit()
}

// ListFilter holds all filtering parameters for RunListFiltered.
type ListFilter struct {
	ParentID       string
	Actor          string
	ShowAll        bool
	ClaimedByActor string
	Label          string
	GrepPattern    string
	// Status filters to a single task.status value, or to "open" — a meta-
	// filter that means "any status except done and canceled". When non-empty,
	// it implicitly overrides the actionable-only default (ShowAll behavior).
	Status string
	// ClosedTailCap caps the number of closed-tail rows returned by
	// RunListWithTail. 0 = default cap (DefaultClosedTailCap). Negative =
	// no cap. Ignored by RunListFiltered.
	ClosedTailCap int
	// ClosedTailSinceUnix, when non-zero, restricts closed-tail rows to
	// events at or after this unix timestamp. Ignored by RunListFiltered.
	ClosedTailSinceUnix int64
	// KindScope narrows the top-level forest by tree kind (see kind.go).
	// Meaningful only when ParentID is empty: an explicit parent already
	// pins the scope to one subtree, whose nodes all share a kind, so
	// KindScope is ignored once ParentID is set. The zero value,
	// ListKindScopeAny, applies no split — the behavior every caller other
	// than `ls` relies on.
	KindScope ListKindScope
}

// ListKindScope controls which tree kind(s) RunListFiltered and
// RunListWithTail return at the top level of an unscoped (ParentID == "")
// forest. `ls` is the only caller that sets this to anything but the zero
// value — every other caller wants the mixed forest it always got.
type ListKindScope int

const (
	// ListKindScopeAny returns roots of every kind: the default, mixed
	// forest every caller other than `ls` expects.
	ListKindScopeAny ListKindScope = iota
	// ListKindScopeTasks keeps only task-tree roots — the default `ls`
	// forest, which omits issue-tree roots.
	ListKindScopeTasks
	// ListKindScopeIssues keeps only issue-tree roots — `ls --issues`.
	ListKindScopeIssues
)

// filterRootsByKind narrows a top-level forest to one tree kind. It must
// only be applied to actual roots (ParentID == ""): kind is root-only (see
// kind.go), so applying it to a node's Children would test the wrong
// property. ListKindScopeAny is a no-op.
func filterRootsByKind(nodes []*TaskNode, scope ListKindScope) []*TaskNode {
	if scope == ListKindScopeAny {
		return nodes
	}
	wantIssue := scope == ListKindScopeIssues
	out := nodes[:0:0]
	for _, n := range nodes {
		if n.Task.Kind.IsIssue() == wantIssue {
			out = append(out, n)
		}
	}
	return out
}

// DefaultClosedTailCap is the row cap applied to RunListWithTail when the
// caller leaves ListFilter.ClosedTailCap at zero.
const DefaultClosedTailCap = 10

// ClosedTailRow names a task that closed (done or canceled) within scope,
// paired with the unix timestamp of the close event used to sort the tail.
type ClosedTailRow struct {
	Task     *Task
	ClosedAt int64
}

// ListResult bundles the open tree (today's RunListFiltered output) with a
// flat closed-tail set and the unbounded total of closed tasks in scope.
// Returned by RunListWithTail.
type ListResult struct {
	Open        []*TaskNode
	ClosedTail  []ClosedTailRow
	ClosedTotal int
	// IssuesOpen is the open (not done, not canceled) task count across
	// every issue-tree root in the database, root included, computed
	// independently of every other ListFilter field — the number `ls`'s
	// "Issues: N open" trailer reports stays a stable backlog size rather
	// than shifting with --label/--mine/etc. on the invocation that
	// printed it. Nil when the database has no issue-tree root.
	IssuesOpen *int
}

func runList(db *sql.DB, parentShortID, actor string, showAll bool) ([]*TaskNode, error) {
	return RunListFiltered(db, ListFilter{ParentID: parentShortID, Actor: actor, ShowAll: showAll})
}

func RunListFiltered(db *sql.DB, f ListFilter) ([]*TaskNode, error) {
	if err := expireStaleClaims(db, f.Actor); err != nil {
		return nil, err
	}

	tasks, err := loadAllTasks(db)
	if err != nil {
		return nil, err
	}

	tree := buildTree(tasks)

	if f.ParentID != "" {
		parent := findNodeByShortID(tree, f.ParentID)
		if parent == nil {
			return nil, fmt.Errorf("task %q not found", f.ParentID)
		}
		tree = parent.Children
	} else {
		tree = filterRootsByKind(tree, f.KindScope)
	}

	blockedIDs, err := getBlockedTaskIDs(db)
	if err != nil {
		return nil, err
	}

	effectiveShowAll := f.ShowAll || f.ClaimedByActor != "" || f.Status != ""
	filtered := filterTree(tree, effectiveShowAll, blockedIDs)
	if f.ClaimedByActor != "" {
		filtered = filterByClaimedActor(filtered, f.ClaimedByActor)
	}
	if f.Status != "" {
		filtered = filterByStatus(filtered, f.Status)
	}
	if f.Label != "" {
		labeledIDs, err := taskIDsWithLabel(db, f.Label)
		if err != nil {
			return nil, err
		}
		filtered = filterByLabel(filtered, labeledIDs)
	}
	if f.GrepPattern != "" {
		filtered = filterByGrep(filtered, f.GrepPattern)
	}
	return filtered, nil
}

// ValidateStatusFilter accepts the short status vocabulary the CLI exposes
// for filtering, returning a normalized form. "open" is a meta-status that
// matches any status except done and canceled.
func ValidateStatusFilter(raw string) (string, error) {
	switch raw {
	case "available", "claimed", "done", "canceled", "open":
		return raw, nil
	default:
		return "", fmt.Errorf("invalid --status %q (want available|claimed|done|canceled|open)", raw)
	}
}

// filterByStatus retains nodes whose status matches, OR whose subtree contains
// a match. "open" matches everything except done and canceled.
func filterByStatus(nodes []*TaskNode, status string) []*TaskNode {
	matches := func(s string) bool {
		if status == "open" {
			return s != "done" && s != "canceled"
		}
		return s == status
	}
	var out []*TaskNode
	for _, n := range nodes {
		filteredChildren := filterByStatus(n.Children, status)
		if matches(n.Task.Status) || len(filteredChildren) > 0 {
			out = append(out, &TaskNode{Task: n.Task, Children: filteredChildren})
		}
	}
	return out
}

// filterByGrep retains only nodes whose title contains pattern (case-insensitive).
// Children are also checked: a node is kept if it or any descendant matches.
func filterByGrep(nodes []*TaskNode, pattern string) []*TaskNode {
	lower := strings.ToLower(pattern)
	var out []*TaskNode
	for _, n := range nodes {
		filteredChildren := filterByGrep(n.Children, pattern)
		if strings.Contains(strings.ToLower(n.Task.Title), lower) || len(filteredChildren) > 0 {
			out = append(out, &TaskNode{Task: n.Task, Children: filteredChildren})
		}
	}
	return out
}

// taskIDsWithLabel returns the set of task ids carrying the given label.
func taskIDsWithLabel(db *sql.DB, name string) (map[int64]bool, error) {
	rows, err := db.Query("SELECT task_id FROM task_labels WHERE name = ?", name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// filterByLabel keeps only nodes whose task is in labeledIDs OR whose subtree
// contains a labeled task. Children that don't match (and have no labeled
// descendants) are pruned. This preserves the hierarchical context of the
// matching tasks rather than flattening the result.
func filterByLabel(nodes []*TaskNode, labeledIDs map[int64]bool) []*TaskNode {
	var out []*TaskNode
	for _, node := range nodes {
		filteredChildren := filterByLabel(node.Children, labeledIDs)
		if labeledIDs[node.Task.ID] || len(filteredChildren) > 0 {
			out = append(out, &TaskNode{Task: node.Task, Children: filteredChildren})
		}
	}
	return out
}

func filterByClaimedActor(nodes []*TaskNode, actor string) []*TaskNode {
	var out []*TaskNode
	for _, node := range nodes {
		filteredChildren := filterByClaimedActor(node.Children, actor)
		matched := node.Task.Status == "claimed" && node.Task.ClaimedBy != nil && *node.Task.ClaimedBy == actor
		if matched || len(filteredChildren) > 0 {
			out = append(out, &TaskNode{Task: node.Task, Children: filteredChildren})
		}
	}
	return out
}

// cascadeAutoCloseAncestors walks the ancestor chain from taskID upward,
// auto-closing each ancestor whose open children have all been closed
// (status is now "done" or "canceled"). Destination per ancestor is
// status-aware: if any sibling closed as "done", the ancestor cascades
// to "done"; if every sibling is "canceled", the ancestor cascades to
// "canceled". triggerKind labels the event ("done" or "cancel") so the
// log can distinguish the two cascade flavours. The cascade never closes an
// issue-tree root — it stops there instead, leaving the root open — since an
// issue tree is open-ended by design. Returns the ordered list of
// auto-closed ancestors, nearest-parent first.
func cascadeAutoCloseAncestors(tx dbtx, taskID int64, triggerShortID, triggerKind, actor string, now int64) ([]AutoClosedAncestor, error) {
	var result []AutoClosedAncestor
	cursorID := taskID
	for {
		var parentID *int64
		if err := tx.QueryRow(
			"SELECT parent_id FROM tasks WHERE id = ?", cursorID,
		).Scan(&parentID); err != nil {
			return nil, err
		}
		if parentID == nil {
			return result, nil
		}

		row := tx.QueryRow(`
			SELECT id, short_id, parent_id, title, description, status, sort_order,
			       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
			FROM tasks WHERE id = ?`, *parentID)
		p, err := scanTask(row)
		if err != nil {
			return nil, err
		}
		// If the parent is already done/canceled, stop the cascade — nothing
		// to do, and we shouldn't walk past it.
		if p.Status == "done" || p.Status == "canceled" {
			return result, nil
		}

		open, err := countOpenChildren(tx, p.ID)
		if err != nil {
			return nil, err
		}
		if open > 0 {
			return result, nil
		}

		// An issue-tree root is open-ended by design (see tree-kinds docs):
		// its lifetime is not bounded by the plan that surfaced it. The
		// cascade stops here without closing it — a bug filed under it later
		// must land on a still-open root. Intermediate parents inside an
		// issue tree (an issue with its own task children) are not roots and
		// still auto-close normally; only the root itself is exempt. An
		// explicit `job done`/`job cancel` on the root is unaffected — that
		// path never goes through this cascade.
		if p.ParentID == nil && p.Kind.IsIssue() {
			return result, nil
		}

		// Destination: any done sibling → "done"; otherwise "canceled".
		destination := "canceled"
		var doneSiblings int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM tasks WHERE parent_id = ? AND status = 'done' AND deleted_at IS NULL",
			p.ID,
		).Scan(&doneSiblings); err != nil {
			return nil, err
		}
		if doneSiblings > 0 {
			destination = "done"
		}

		wasStatus := p.Status
		if _, err := tx.Exec(
			"UPDATE tasks SET status = ?, updated_at = ? WHERE id = ?",
			destination, now, p.ID,
		); err != nil {
			return nil, err
		}
		// Event type mirrors destination so audit logs show the same verb
		// the status landed on. detail carries both trigger_kind and
		// cascade_status so consumers can tell apart done-triggered vs
		// cancel-triggered cascades without inspecting sibling history.
		eventDetail := map[string]any{
			"auto_closed":    true,
			"trigger_kind":   triggerKind,
			"triggered_by":   triggerShortID,
			"cascade_status": destination,
			"was_status":     wasStatus,
		}
		// Auto-closed parents are typically not claimed (adding any open
		// child auto-releases a parent's claim), but record the
		// breadcrumbs anyway in case some path skipped that release.
		if wasStatus == "claimed" {
			if p.ClaimedBy != nil {
				eventDetail["was_claimed_by"] = *p.ClaimedBy
			}
			if p.ClaimExpiresAt != nil {
				eventDetail["was_expires_at"] = *p.ClaimExpiresAt
			}
		}
		if err := recordEvent(tx, p.ID, destination, actor, eventDetail); err != nil {
			return nil, err
		}
		if destination == "done" {
			if err := recordBlocksUnblockedOn(tx, p.ID, p.ShortID, actor); err != nil {
				return nil, err
			}
		} else {
			if err := recordBlocksUnblockedOnCancel(tx, p.ID, p.ShortID, actor); err != nil {
				return nil, err
			}
		}

		// A cascade that reaches a root ends that tree: release every
		// actor's focus on it, visibly.
		if p.ParentID == nil {
			if err := releaseFocusOnRootClose(tx, p); err != nil {
				return nil, err
			}
		}

		result = append(result, AutoClosedAncestor{ShortID: p.ShortID, Title: p.Title, Status: destination})
		cursorID = p.ID
	}
}

// strictCloseTarget is the minimal pair the strict-close error formatter
// needs from each target — the operator-facing short ID plus the title used
// in the per-task header line.
type strictCloseTarget struct {
	ShortID string
	Title   string
}

// formatStrictCloseError builds the multi-line refusal returned by the
// strict-default close gate. The leading line uses a stable "cannot close:
// N pending criteria" prefix so retry-with-override automation can grep for
// it; the body lists each unmarked criterion under its task header so the
// override is informed; the trailing line names the override flag.
func formatStrictCloseError(targets []strictCloseTarget, pending map[string][]string) error {
	total := 0
	for _, labels := range pending {
		total += len(labels)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cannot close: %d pending criteria", total)
	if len(pending) > 1 {
		fmt.Fprintf(&b, " across %d tasks", len(pending))
	}
	for _, tgt := range targets {
		labels := pending[tgt.ShortID]
		if len(labels) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n  %s %q:", tgt.ShortID, tgt.Title)
		for _, l := range labels {
			fmt.Fprintf(&b, "\n    [ ] %s", l)
		}
	}
	b.WriteString("\nOverride: --force-close-with-pending")
	return fmt.Errorf("%s", b.String())
}

// RunDone closes one or more tasks atomically. If cascade is true, each target
// expands to include all open descendants. If a target has unmarked pending
// criteria and cascade is false, RunDone refuses with a "cannot close: N
// pending criteria" error unless forceCloseWithPending is true; in that case
// the unmarked labels are persisted on the done event under "criteria_waived"
// so a reviewer can see what was deferred. When bulkCriteriaState is non-
// empty, it is recorded on the done event under "criteria_bulk_state" so the
// close shape (e.g. "all marked passed via --all-passed") survives in the
// event log. Returns per-target results, a list of already-done targets that
// were skipped, or an error (all-or-nothing).
func RunDone(db *sql.DB, ids []string, cascade bool, note string, result json.RawMessage, actor string, forceCloseWithPending bool, bulkCriteriaState string) (closed []*ClosedResult, alreadyDone []string, err error) {
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("done requires at least one task id")
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, nil, err
	}

	// Phase A: validate every id and resolve to a task, partition already-done.
	type target struct {
		shortID string
		task    *Task
	}
	var targets []target
	seenExplicit := make(map[int64]bool)
	for _, id := range ids {
		if err := checkClaimOwnership(tx, id, actor); err != nil {
			return nil, nil, err
		}
		t, err := GetTaskByShortID(tx, id)
		if err != nil {
			return nil, nil, err
		}
		if t == nil {
			return nil, nil, fmt.Errorf("task %q not found", id)
		}
		if t.Status == "done" {
			alreadyDone = append(alreadyDone, id)
			continue
		}
		if seenExplicit[t.ID] {
			continue
		}
		seenExplicit[t.ID] = true
		targets = append(targets, target{shortID: id, task: t})
	}

	// Phase A.2: for each target, validate or expand via cascade.
	type plan struct {
		target        target
		cascadeTasks  []*Task
		cascadeShorts []string
	}
	var plans []plan
	seenCascade := make(map[int64]bool)
	for _, tgt := range targets {
		incomplete, err := findIncompleteDescendants(tx, tgt.task.ID)
		if err != nil {
			return nil, nil, err
		}
		if len(incomplete) > 0 && !cascade {
			var descs []string
			for _, t := range incomplete {
				descs = append(descs, fmt.Sprintf("%s (%s)", t.ShortID, t.Title))
			}
			return nil, nil, fmt.Errorf("task %s has incomplete subtasks: %s (run 'job done --cascade %s' to close all).",
				tgt.shortID, strings.Join(descs, ", "), tgt.shortID)
		}
		var cTasks []*Task
		var cShorts []string
		if cascade {
			for _, d := range incomplete {
				if seenExplicit[d.ID] || seenCascade[d.ID] {
					continue
				}
				seenCascade[d.ID] = true
				cTasks = append(cTasks, d)
				cShorts = append(cShorts, d.ShortID)
			}
		}
		plans = append(plans, plan{target: tgt, cascadeTasks: cTasks, cascadeShorts: cShorts})
	}

	// Strict-default close gate: refuse if a target has pending criteria,
	// unless --cascade is set ("children own the criteria") or the operator
	// has opted in to the override. Override callers carry the unmarked
	// labels through to the done-event detail as a recorded waiver.
	pendingByTarget := make(map[string][]string, len(targets))
	if !cascade {
		for _, tgt := range targets {
			cs, err := GetCriteria(tx, tgt.task.ID)
			if err != nil {
				return nil, nil, err
			}
			for _, c := range cs {
				if c.State == CriterionPending {
					pendingByTarget[tgt.shortID] = append(pendingByTarget[tgt.shortID], c.Label)
				}
			}
		}
		if len(pendingByTarget) > 0 && !forceCloseWithPending {
			ordered := make([]strictCloseTarget, 0, len(targets))
			for _, tgt := range targets {
				ordered = append(ordered, strictCloseTarget{ShortID: tgt.shortID, Title: tgt.task.Title})
			}
			return nil, nil, formatStrictCloseError(ordered, pendingByTarget)
		}
	}

	now := CurrentNowFunc().Unix()

	var noteVal any
	if note != "" {
		noteVal = note
	}

	var resultVal any
	if len(result) > 0 {
		var parsed any
		if err := json.Unmarshal(result, &parsed); err != nil {
			return nil, nil, fmt.Errorf("--result: invalid JSON: %s", err)
		}
		resultVal = parsed
	}

	// Phase B: execute.
	for _, p := range plans {
		// Close cascaded descendants first.
		for _, child := range p.cascadeTasks {
			childDetail := map[string]any{
				"cascade":                  true,
				"cascade_closed_by_parent": p.target.shortID,
				"was_status":               child.Status,
			}
			if child.Status == "claimed" {
				if child.ClaimedBy != nil {
					childDetail["was_claimed_by"] = *child.ClaimedBy
				}
				if child.ClaimExpiresAt != nil {
					childDetail["was_expires_at"] = *child.ClaimExpiresAt
				}
			}
			if _, err := tx.Exec(
				"UPDATE tasks SET status = 'done', updated_at = ? WHERE id = ?",
				now, child.ID,
			); err != nil {
				return nil, nil, err
			}
			if err := recordEvent(tx, child.ID, "done", actor, childDetail); err != nil {
				return nil, nil, err
			}
			if err := recordBlocksUnblockedOn(tx, child.ID, child.ShortID, actor); err != nil {
				return nil, nil, err
			}
		}

		// Close the explicit target.
		targetTask := p.target.task
		wasStatus := targetTask.Status
		if _, err := tx.Exec(
			"UPDATE tasks SET status = 'done', completion_note = ?, updated_at = ? WHERE id = ?",
			noteVal, now, targetTask.ID,
		); err != nil {
			return nil, nil, err
		}
		detail := map[string]any{
			"note":           noteVal,
			"cascade":        cascade,
			"cascade_closed": p.cascadeShorts,
			"was_status":     wasStatus,
		}
		if wasStatus == "claimed" {
			if targetTask.ClaimedBy != nil {
				detail["was_claimed_by"] = *targetTask.ClaimedBy
			}
			if targetTask.ClaimExpiresAt != nil {
				detail["was_expires_at"] = *targetTask.ClaimExpiresAt
			}
		}
		if resultVal != nil {
			detail["result"] = resultVal
		}
		if waived := pendingByTarget[p.target.shortID]; len(waived) > 0 {
			detail["criteria_waived"] = waived
		}
		if bulkCriteriaState != "" {
			detail["criteria_bulk_state"] = bulkCriteriaState
		}
		if err := recordEvent(tx, targetTask.ID, "done", actor, detail); err != nil {
			return nil, nil, err
		}
		if err := recordBlocksUnblockedOn(tx, p.target.task.ID, p.target.shortID, actor); err != nil {
			return nil, nil, err
		}

		// Closing a root directly ends that tree: release focus visibly.
		if targetTask.ParentID == nil {
			if err := releaseFocusOnRootClose(tx, targetTask); err != nil {
				return nil, nil, err
			}
		}

		// Leaf-frontier cascade: after closing this target, auto-close any
		// ancestors whose last open child has just been closed.
		autoClosed, err := cascadeAutoCloseAncestors(tx, p.target.task.ID, p.target.shortID, "done", actor, now)
		if err != nil {
			return nil, nil, err
		}

		closed = append(closed, &ClosedResult{
			ShortID:             p.target.shortID,
			Title:               p.target.task.Title,
			Note:                note,
			CascadeClosed:       p.cascadeShorts,
			AutoClosedAncestors: autoClosed,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return closed, alreadyDone, nil
}

func recordBlocksUnblockedOn(tx dbtx, blockerID int64, blockerShortID, actor string) error {
	rows, err := tx.Query("SELECT blocked_id FROM blocks WHERE blocker_id = ?", blockerID)
	if err != nil {
		return err
	}
	var unblockedIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		unblockedIDs = append(unblockedIDs, id)
	}
	rows.Close()
	if len(unblockedIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec("DELETE FROM blocks WHERE blocker_id = ?", blockerID); err != nil {
		return err
	}
	for _, id := range unblockedIDs {
		var blockedShortID string
		if err := tx.QueryRow("SELECT short_id FROM tasks WHERE id = ?", id).Scan(&blockedShortID); err != nil {
			return err
		}
		if err := recordEvent(tx, id, "unblocked", actor, map[string]any{
			"blocked_id": blockedShortID,
			"blocker_id": blockerShortID,
			"reason":     "blocker_done",
		}); err != nil {
			return err
		}
	}
	return nil
}

func RunReopen(db *sql.DB, shortID string, cascade bool, actor string) ([]string, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, err
	}
	if err := checkClaimOwnership(tx, shortID, actor); err != nil {
		return nil, err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	if task.Status != "done" && task.Status != "canceled" {
		return nil, fmt.Errorf("task %s is not done or canceled (status: %s)", shortID, task.Status)
	}
	fromStatus := task.Status

	now := CurrentNowFunc().Unix()

	var reopenedChildren []string
	if cascade {
		descendants, err := findClosedDescendants(tx, task.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range descendants {
			if _, err := tx.Exec(
				"UPDATE tasks SET status = 'available', completion_note = NULL, updated_at = ? WHERE id = ?",
				now, d.ID,
			); err != nil {
				return nil, err
			}
			if err := recordEvent(tx, d.ID, "reopened", actor, map[string]any{
				"cascade":           false,
				"reopened_children": []string{},
				"from_status":       d.Status,
			}); err != nil {
				return nil, err
			}
			reopenedChildren = append(reopenedChildren, d.ShortID)
		}
	}

	if _, err := tx.Exec(
		"UPDATE tasks SET status = 'available', completion_note = NULL, updated_at = ? WHERE id = ?",
		now, task.ID,
	); err != nil {
		return nil, err
	}

	if err := recordEvent(tx, task.ID, "reopened", actor, map[string]any{
		"cascade":           cascade,
		"reopened_children": reopenedChildren,
		"from_status":       fromStatus,
	}); err != nil {
		return nil, err
	}

	return reopenedChildren, tx.Commit()
}

func RunEdit(db *sql.DB, shortID string, newTitle, newDesc *string, actor string) error {
	if newTitle == nil && newDesc == nil {
		return fmt.Errorf("edit requires --title and/or --desc")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}
	if err := checkClaimOwnership(tx, shortID, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}

	now := CurrentNowFunc().Unix()
	detail := map[string]any{}

	if newTitle != nil && *newTitle != task.Title {
		if _, err := tx.Exec(
			"UPDATE tasks SET title = ?, updated_at = ? WHERE id = ?",
			*newTitle, now, task.ID,
		); err != nil {
			return err
		}
		detail["old_title"] = task.Title
		detail["new_title"] = *newTitle
	} else if newTitle != nil {
		detail["old_title"] = task.Title
		detail["new_title"] = *newTitle
	}

	if newDesc != nil {
		if _, err := tx.Exec(
			"UPDATE tasks SET description = ?, updated_at = ? WHERE id = ?",
			*newDesc, now, task.ID,
		); err != nil {
			return err
		}
		detail["old_desc"] = task.Description
		detail["new_desc"] = *newDesc
	}

	if err := recordEvent(tx, task.ID, "edited", actor, detail); err != nil {
		return err
	}

	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return err
	}

	return tx.Commit()
}

func RunNote(db *sql.DB, shortID, text string, result json.RawMessage, actor string) error {
	if text == "" {
		return fmt.Errorf("note text is empty")
	}

	var resultVal any
	if len(result) > 0 {
		var parsed any
		if err := json.Unmarshal(result, &parsed); err != nil {
			return fmt.Errorf("--result: invalid JSON: %s", err)
		}
		resultVal = parsed
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}
	if err := checkClaimOwnership(tx, shortID, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}

	now := CurrentNowFunc().Unix()
	if _, err := tx.Exec(
		"UPDATE tasks SET updated_at = ? WHERE id = ?",
		now, task.ID,
	); err != nil {
		return err
	}

	detail := map[string]any{"text": text}
	if resultVal != nil {
		detail["result"] = resultVal
	}
	if err := recordEvent(tx, task.ID, "noted", actor, detail); err != nil {
		return err
	}

	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return err
	}

	return tx.Commit()
}

func RunMove(db *sql.DB, shortID, direction, relativeToShortID, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}
	if err := checkClaimOwnership(tx, shortID, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}

	relative, err := GetTaskByShortID(tx, relativeToShortID)
	if err != nil {
		return err
	}
	if relative == nil {
		return fmt.Errorf("task %q not found", relativeToShortID)
	}

	if (task.ParentID == nil) != (relative.ParentID == nil) {
		return fmt.Errorf("%s and %s are not siblings (different parents)", shortID, relativeToShortID)
	}
	if task.ParentID != nil && relative.ParentID != nil && *task.ParentID != *relative.ParentID {
		return fmt.Errorf("%s and %s are not siblings (different parents)", shortID, relativeToShortID)
	}

	oldSortOrder := task.SortOrder
	var newSortOrder int

	if direction == "before" {
		newSortOrder = relative.SortOrder
		var parentFilter string
		var args []any
		if task.ParentID == nil {
			parentFilter = "parent_id IS NULL"
		} else {
			parentFilter = "parent_id = ?"
			args = append(args, *task.ParentID)
		}
		args = append(args, newSortOrder, task.ID)
		_, err = tx.Exec(
			"UPDATE tasks SET sort_order = sort_order + 1 WHERE "+parentFilter+" AND sort_order >= ? AND id != ? AND deleted_at IS NULL",
			args...,
		)
		if err != nil {
			return err
		}
	} else {
		newSortOrder = relative.SortOrder + 1
		var parentFilter string
		var args []any
		if task.ParentID == nil {
			parentFilter = "parent_id IS NULL"
		} else {
			parentFilter = "parent_id = ?"
			args = append(args, *task.ParentID)
		}
		args = append(args, relative.SortOrder, task.ID)
		_, err = tx.Exec(
			"UPDATE tasks SET sort_order = sort_order + 1 WHERE "+parentFilter+" AND sort_order > ? AND id != ? AND deleted_at IS NULL",
			args...,
		)
		if err != nil {
			return err
		}
	}

	now := CurrentNowFunc().Unix()
	if _, err := tx.Exec(
		"UPDATE tasks SET sort_order = ?, updated_at = ? WHERE id = ?",
		newSortOrder, now, task.ID,
	); err != nil {
		return err
	}

	if err := recordEvent(tx, task.ID, "moved", actor, map[string]any{
		"direction":      direction,
		"relative_to":    relativeToShortID,
		"old_sort_order": oldSortOrder,
		"new_sort_order": newSortOrder,
	}); err != nil {
		return err
	}

	return tx.Commit()
}

// SplitResult carries the outcome of RunSplit.
type SplitResult struct {
	ParentShortID       string
	ChildShortIDs       []string
	AutoReleasedParent  string
	AutoReleasedByActor string
}

// RunSplit takes a leaf task and creates N new children under it from the
// supplied titles. Errors if the parent already has children. The leaf-frontier
// rule fires the moment the first child is added, so a claimed parent
// auto-releases — that release is reported in the result.
func RunSplit(db *sql.DB, parentShortID string, titles []string, actor string) (*SplitResult, error) {
	if parentShortID == "" {
		return nil, fmt.Errorf("split requires a parent task ID")
	}
	if len(titles) == 0 {
		return nil, fmt.Errorf("split requires at least one child title")
	}

	parent, err := GetTaskByShortID(db, parentShortID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("task %q not found", parentShortID)
	}

	existing, err := getChildren(db, parent.ID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("cannot split %s: it already has %d child(ren); split only operates on leaves", parentShortID, len(existing))
	}

	res := &SplitResult{ParentShortID: parentShortID}
	for _, title := range titles {
		add, err := RunAdd(db, parentShortID, title, "", "", nil, actor)
		if err != nil {
			return res, fmt.Errorf("split: adding %q: %w", title, err)
		}
		res.ChildShortIDs = append(res.ChildShortIDs, add.ShortID)
		if add.AutoReleasedParent != "" && res.AutoReleasedParent == "" {
			res.AutoReleasedParent = add.AutoReleasedParent
			res.AutoReleasedByActor = add.AutoReleasedByActor
		}
	}
	return res, nil
}

// RunReparent moves a task to a new parent. If newParentShortID is empty,
// the task is moved to the root. If direction and relativeToShortID are
// supplied, the task is positioned before or after that sibling within the
// new parent; otherwise it is appended at the end.
func RunReparent(db *sql.DB, shortID, newParentShortID, direction, relativeToShortID, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}
	if err := checkClaimOwnership(tx, shortID, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}

	var newParentID *int64
	var newParentShortOut string
	if newParentShortID != "" {
		// An issue root that gains a parent stops being a root, and its kind
		// would silently stop meaning anything. Refuse rather than reset:
		// the reset is one explicit `job kind <id> task` away, and that
		// conversion belongs in the event log where a silent one would not be.
		if task.ParentID == nil && task.Kind.IsIssue() {
			return fmt.Errorf(
				"%s is an issue-tree root; tree kind is root-only. Run 'job kind %s task' first if you mean to fold it into another tree.",
				shortID, shortID,
			)
		}
		newParent, err := GetTaskByShortID(tx, newParentShortID)
		if err != nil {
			return err
		}
		if newParent == nil {
			return fmt.Errorf("task %q not found", newParentShortID)
		}
		if newParent.ID == task.ID {
			return fmt.Errorf("cannot reparent %s under itself", shortID)
		}
		descendants, err := findAllDescendants(tx, task.ID)
		if err != nil {
			return err
		}
		for _, d := range descendants {
			if d.ID == newParent.ID {
				return fmt.Errorf("cannot reparent %s under its own descendant %s", shortID, newParentShortID)
			}
		}
		newParentID = &newParent.ID
		newParentShortOut = newParent.ShortID
	}

	priorParentShort := ""
	if task.ParentID != nil {
		priorParent, err := getTaskByID(tx, *task.ParentID)
		if err != nil {
			return err
		}
		if priorParent != nil {
			priorParentShort = priorParent.ShortID
		}
	}

	var newSortOrder int
	if direction == "" {
		var maxSort sql.NullInt64
		if newParentID == nil {
			err = tx.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE parent_id IS NULL AND deleted_at IS NULL AND id != ?", task.ID).Scan(&maxSort)
		} else {
			err = tx.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE parent_id = ? AND deleted_at IS NULL AND id != ?", *newParentID, task.ID).Scan(&maxSort)
		}
		if err != nil {
			return err
		}
		if maxSort.Valid {
			newSortOrder = int(maxSort.Int64) + 1
		}
	} else {
		if direction != "before" && direction != "after" {
			return fmt.Errorf("direction must be 'before' or 'after', got %q", direction)
		}
		relative, err := GetTaskByShortID(tx, relativeToShortID)
		if err != nil {
			return err
		}
		if relative == nil {
			return fmt.Errorf("task %q not found", relativeToShortID)
		}
		if (relative.ParentID == nil) != (newParentID == nil) {
			return fmt.Errorf("%s is not a child of the new parent", relativeToShortID)
		}
		if relative.ParentID != nil && newParentID != nil && *relative.ParentID != *newParentID {
			return fmt.Errorf("%s is not a child of the new parent", relativeToShortID)
		}
		if direction == "before" {
			newSortOrder = relative.SortOrder
		} else {
			newSortOrder = relative.SortOrder + 1
		}
		var parentFilter string
		var args []any
		if newParentID == nil {
			parentFilter = "parent_id IS NULL"
		} else {
			parentFilter = "parent_id = ?"
			args = append(args, *newParentID)
		}
		var threshold int
		if direction == "before" {
			threshold = relative.SortOrder
			args = append(args, threshold, task.ID)
			if _, err := tx.Exec(
				"UPDATE tasks SET sort_order = sort_order + 1 WHERE "+parentFilter+" AND sort_order >= ? AND id != ? AND deleted_at IS NULL",
				args...,
			); err != nil {
				return err
			}
		} else {
			threshold = relative.SortOrder
			args = append(args, threshold, task.ID)
			if _, err := tx.Exec(
				"UPDATE tasks SET sort_order = sort_order + 1 WHERE "+parentFilter+" AND sort_order > ? AND id != ? AND deleted_at IS NULL",
				args...,
			); err != nil {
				return err
			}
		}
	}

	now := CurrentNowFunc().Unix()
	if _, err := tx.Exec(
		"UPDATE tasks SET parent_id = ?, sort_order = ?, updated_at = ? WHERE id = ?",
		newParentID, newSortOrder, now, task.ID,
	); err != nil {
		return err
	}

	detail := map[string]any{
		"prior_parent_id": priorParentShort,
		"new_parent_id":   newParentShortOut,
		"old_sort_order":  task.SortOrder,
		"new_sort_order":  newSortOrder,
	}
	if direction != "" {
		detail["direction"] = direction
		detail["relative_to"] = relativeToShortID
	}
	if err := recordEvent(tx, task.ID, "reparented", actor, detail); err != nil {
		return err
	}

	if newParentID != nil {
		newParent, err := getTaskByID(tx, *newParentID)
		if err != nil {
			return err
		}
		if newParent != nil && newParent.Status == "claimed" {
			prior := ""
			if newParent.ClaimedBy != nil {
				prior = *newParent.ClaimedBy
			}
			var priorExpires int64
			if newParent.ClaimExpiresAt != nil {
				priorExpires = *newParent.ClaimExpiresAt
			}
			if _, err := tx.Exec(
				"UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
				now, newParent.ID,
			); err != nil {
				return err
			}
			if err := recordEvent(tx, newParent.ID, "released", actor, map[string]any{
				"auto_released":      true,
				"triggered_by_child": task.ShortID,
				"was_claimed_by":     prior,
				"was_expires_at":     priorExpires,
			}); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

type DoneContext struct {
	ClosedID           string
	ClosedTitle        string
	Next               *Task
	SkippedBlocked     *Task
	SkippedBlockedBy   string
	ParentID           string
	ParentTitle        string
	ParentWasDone      bool
	ParentDoneCount    int
	ParentTotalCount   int
	ParentAutoClosed   bool
	WholeTreeComplete  bool
	WholeTreeDoneCount int
	WholeTreeRootID    string
	// SurfacedOpen lists the still-open tasks found-in the closed task, in
	// short-id order — the `done` ack's `Surfaced:` line. Closed (done or
	// canceled) and soft-deleted issues are excluded.
	SurfacedOpen []*Task
}

// ComputeDoneContext computes the trailing-context block for a done ack.
// autoClosedSet names ancestors that were auto-closed by the leaf-frontier
// cascade in this same call — they are "done" now but were not done before,
// which we need to distinguish to compute ParentWasDone correctly.
func ComputeDoneContext(db *sql.DB, closedShortID string, autoClosedSet map[string]bool) (*DoneContext, error) {
	closed, err := GetTaskByShortID(db, closedShortID)
	if err != nil {
		return nil, err
	}
	if closed == nil {
		return nil, fmt.Errorf("task %q not found", closedShortID)
	}

	ctx := &DoneContext{
		ClosedID:    closed.ShortID,
		ClosedTitle: closed.Title,
	}

	if closed.ParentID != nil {
		parent, err := getTaskByID(db, *closed.ParentID)
		if err != nil {
			return nil, err
		}
		if parent != nil {
			ctx.ParentID = parent.ShortID
			ctx.ParentTitle = parent.Title
			ctx.ParentAutoClosed = autoClosedSet[parent.ShortID]
			// ParentWasDone means "already done before this call." If the parent
			// just auto-closed in this call, it was NOT done before.
			ctx.ParentWasDone = parent.Status == "done" && !ctx.ParentAutoClosed

			siblings, err := getChildren(db, parent.ID)
			if err != nil {
				return nil, err
			}
			ctx.ParentTotalCount = len(siblings)
			for _, s := range siblings {
				if s.Status == "done" {
					ctx.ParentDoneCount++
				}
			}
		}
	}

	surfaced, err := GetSurfaced(db, closed.ShortID)
	if err != nil {
		return nil, err
	}
	for _, s := range surfaced {
		if s.Status == "done" || s.Status == "canceled" {
			continue
		}
		ctx.SurfacedOpen = append(ctx.SurfacedOpen, s)
	}

	next, skipped, skippedBy, err := findNextClaimableLeafHierarchical(db, closed)
	if err != nil {
		return nil, err
	}
	ctx.Next = next
	ctx.SkippedBlocked = skipped
	ctx.SkippedBlockedBy = skippedBy

	root, err := findTopAncestor(db, closed)
	if err != nil {
		return nil, err
	}
	if root != nil {
		allDone, doneCount, err := subtreeCompleteness(db, root.ID)
		if err != nil {
			return nil, err
		}
		if allDone {
			ctx.WholeTreeComplete = true
			ctx.WholeTreeDoneCount = doneCount
			ctx.WholeTreeRootID = root.ShortID
		}
	}

	return ctx, nil
}

// findNextClaimableLeafHierarchical implements the Next: walk used by
// the done ack. Starting at closed's parent, at each ancestor level it
// checks forward siblings (sort_order strictly greater than the
// came-from child) and then earlier siblings (strictly less), descending
// into any parent-with-open-children to surface the first claimable
// leaf. It walks up until claimable work is found or until it has
// exhausted closed's root tree, then makes one final pass over the
// root-level forest as virtual siblings. Returns (nil, nil, "", nil)
// when the entire database has no claimable work.
//
// The `skipped` / `skippedBy` return slots only fire at the immediate
// parent level: if the first forward sibling of closed was blocked and
// we skipped past it to find Next, we surface that to the user via the
// "Next sibling X is blocked on Y. Skipping to Z." hint.
func findNextClaimableLeafHierarchical(db *sql.DB, closed *Task) (*Task, *Task, string, error) {
	var skipped *Task
	var skippedBy string

	// cameFromID / cameFromSortOrder describe the child of the current
	// ancestor that is on the path from the closed task. At the first
	// iteration this is `closed` itself; each subsequent iteration
	// steps cameFrom up one level.
	cameFromID := closed.ID
	cameFromSortOrder := closed.SortOrder

	var anchorParentID *int64
	if closed.ParentID != nil {
		pid := *closed.ParentID
		anchorParentID = &pid
	}

	firstLevel := true

	// Helper: given a list of candidate siblings, pick the first
	// unblocked one with a claimable descendant and return its leaf.
	// blockedSkippedSinkFirstLevel records the first blocked candidate
	// encountered at the first level so the caller can emit the
	// "skipping to" hint.
	pickFromCandidates := func(cands []*Task, recordSkip bool) (*Task, error) {
		for _, c := range cands {
			if c.Status == "done" || c.Status == "canceled" {
				continue
			}
			blockers, err := GetBlockers(db, c.ShortID)
			if err != nil {
				return nil, err
			}
			if len(blockers) > 0 {
				if recordSkip && skipped == nil {
					skipped = c
					skippedBy = blockers[0].ShortID
				}
				continue
			}
			leaf, err := descendToClaimableLeaf(db, c)
			if err != nil {
				return nil, err
			}
			if leaf != nil {
				return leaf, nil
			}
		}
		return nil, nil
	}

	for {
		var children []*Task
		var err error
		if anchorParentID == nil {
			children, err = getRootTasks(db)
			if err != nil {
				return nil, nil, "", err
			}
			// The final pass treats the root forest as virtual siblings, and
			// it is the only level where the walk leaves the closed task's
			// own tree. Crossing into an issue-tree there would hand the
			// operator a bug report as "what's next" — `next`, `orient` and
			// `claim --next` all scope to task-trees by default, so the hint
			// does too. Work inside the closed task's own tree stays
			// reachable whatever its kind: that root is the came-from at
			// this level, and every level below it is unfiltered.
			children = taskKindRoots(children)
		} else {
			children, err = getChildren(db, *anchorParentID)
			if err != nil {
				return nil, nil, "", err
			}
		}

		// Forward candidates: sort_order strictly greater than came-from's.
		var forward, earlier []*Task
		for _, c := range children {
			if c.ID == cameFromID {
				continue
			}
			if c.SortOrder > cameFromSortOrder {
				forward = append(forward, c)
			} else if c.SortOrder < cameFromSortOrder {
				earlier = append(earlier, c)
			}
		}

		// Forward first; record the first blocked forward-sibling at the
		// immediate parent level for the "skipping to" hint.
		if leaf, err := pickFromCandidates(forward, firstLevel); err != nil {
			return nil, nil, "", err
		} else if leaf != nil {
			return leaf, skipped, skippedBy, nil
		}

		// Then earlier siblings at this level. Blocked-sibling reporting
		// only makes sense forward, so suppress it here.
		if leaf, err := pickFromCandidates(earlier, false); err != nil {
			return nil, nil, "", err
		} else if leaf != nil {
			return leaf, skipped, skippedBy, nil
		}

		// We just checked the virtual-root forest; there is nothing
		// further up.
		if anchorParentID == nil {
			return nil, skipped, skippedBy, nil
		}

		// Walk up one level: the current anchor becomes "came from" at
		// the grandparent scope.
		parent, err := getTaskByID(db, *anchorParentID)
		if err != nil {
			return nil, nil, "", err
		}
		if parent == nil {
			return nil, skipped, skippedBy, nil
		}
		cameFromID = parent.ID
		cameFromSortOrder = parent.SortOrder
		if parent.ParentID != nil {
			pid := *parent.ParentID
			anchorParentID = &pid
		} else {
			anchorParentID = nil
		}
		firstLevel = false
	}
}

func getTaskByID(db dbtx, id int64) (*Task, error) {
	row := db.QueryRow(`
		SELECT id, short_id, parent_id, title, description, status, sort_order,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE id = ? AND deleted_at IS NULL
	`, id)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// taskKindRoots drops issue-tree roots from a root-forest candidate list,
// matching the default scope of `next`, `orient` and `claim --next`.
func taskKindRoots(roots []*Task) []*Task {
	out := roots[:0:0]
	for _, r := range roots {
		if r.Kind.IsIssue() {
			continue
		}
		out = append(out, r)
	}
	return out
}

func getRootTasks(db *sql.DB) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_order,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id IS NULL AND deleted_at IS NULL
		ORDER BY sort_order
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func getChildren(db *sql.DB, parentID int64) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_order,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
		ORDER BY sort_order
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// findNextSibling returns the next open sibling after `closed` in sort order,
// skipping any sibling that is currently blocked. If the first candidate was
// blocked and we skipped over it, returns the blocked sibling and its first
// blocker via `skipped` / `skippedBy`.
func findNextSibling(db *sql.DB, siblings []*Task, closed *Task) (next *Task, skipped *Task, skippedBy string, err error) {
	var candidates []*Task
	for _, s := range siblings {
		if s.ID == closed.ID {
			continue
		}
		if s.SortOrder <= closed.SortOrder {
			continue
		}
		if s.Status == "done" {
			continue
		}
		candidates = append(candidates, s)
	}
	for i, c := range candidates {
		blockers, err := GetBlockers(db, c.ShortID)
		if err != nil {
			return nil, nil, "", err
		}
		if len(blockers) == 0 {
			if i > 0 {
				skipped = candidates[0]
				bl, bErr := GetBlockers(db, skipped.ShortID)
				if bErr == nil && len(bl) > 0 {
					skippedBy = bl[0].ShortID
				}
			}
			return c, skipped, skippedBy, nil
		}
	}
	return nil, nil, "", nil
}

func findTopAncestor(db dbtx, task *Task) (*Task, error) {
	current := task
	for current.ParentID != nil {
		parent, err := getTaskByID(db, *current.ParentID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			break
		}
		current = parent
	}
	return current, nil
}

// subtreeCompleteness returns whether every task under (and including) rootID is done, and the count of done tasks in that subtree.
func subtreeCompleteness(db *sql.DB, rootID int64) (allDone bool, doneCount int, err error) {
	rows, err := db.Query(`
		WITH RECURSIVE tree(id, status) AS (
			SELECT id, status FROM tasks WHERE id = ? AND deleted_at IS NULL
			UNION ALL
			SELECT t.id, t.status FROM tasks t JOIN tree ON t.parent_id = tree.id WHERE t.deleted_at IS NULL
		)
		SELECT status FROM tree
	`, rootID)
	if err != nil {
		return false, 0, err
	}
	defer rows.Close()
	total := 0
	allDone = true
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return false, 0, err
		}
		total++
		if status == "done" {
			doneCount++
		} else {
			allDone = false
		}
	}
	if err := rows.Err(); err != nil {
		return false, 0, err
	}
	if total == 0 {
		return false, 0, nil
	}
	return allDone, doneCount, nil
}

// descendToClaimableLeaf resolves a "Next:" candidate to an actionable leaf.
// If t has no open children, t is already a leaf and is returned unchanged.
// If t has open children, it isn't directly claimable under leaf-frontier
// semantics, so we descend into t's subtree and return the first claimable
// leaf (depth-first by sort_order). Returns nil if the subtree is entirely
// blocked or contains no available work. Passing a nil t returns nil.
func descendToClaimableLeaf(db *sql.DB, t *Task) (*Task, error) {
	if t == nil {
		return nil, nil
	}
	open, err := countOpenChildren(db, t.ID)
	if err != nil {
		return nil, err
	}
	if open == 0 {
		return t, nil
	}
	leaves, err := queryAvailableLeafFrontier(db, &t.ID, 1, "", "")
	if err != nil {
		return nil, err
	}
	if len(leaves) == 0 {
		return nil, nil
	}
	return leaves[0], nil
}

type TaskInfo struct {
	Task          *Task
	Parent        *Task
	Children      []*Task
	ChildBlockers map[string][]string // child short-ID → blocker short-IDs
	ChildLabels   map[int64][]string  // child task ID → labels (for `RenderMarkdownList` reuse)
	Blockers      []*Task
	Blocked       []*Task // tasks this task is blocking (outbound)
	FoundIn       *Task   // the task that surfaced this one, if recorded
	Surfaced      []*Task // tasks recorded as found in this one
	Labels        []string
	Notes         []NoteEntry
	Criteria      []Criterion
}

// NoteEntry is a single rendered note pulled from the event stream.
type NoteEntry struct {
	Actor     string
	Text      string
	CreatedAt int64
}

func RunInfo(db *sql.DB, shortID string) (*TaskInfo, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	var parent *Task
	if task.ParentID != nil {
		row := db.QueryRow(`
			SELECT id, short_id, parent_id, title, description, status, sort_order,
			       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
			FROM tasks WHERE id = ? AND deleted_at IS NULL
		`, *task.ParentID)
		p, err := scanTask(row)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		parent = p
	}

	rows, err := db.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_order,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
		ORDER BY sort_order
	`, task.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var children []*Task
	for rows.Next() {
		c, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		children = append(children, c)
	}

	blockers, err := GetBlockers(db, shortID)
	if err != nil {
		return nil, err
	}

	blocked, err := GetBlocked(db, shortID)
	if err != nil {
		return nil, err
	}

	foundIn, err := foundInSourceByID(db, task.ID)
	if err != nil {
		return nil, err
	}

	surfaced, err := surfacedByID(db, task.ID)
	if err != nil {
		return nil, err
	}

	labels, err := GetLabels(db, task.ID)
	if err != nil {
		return nil, err
	}

	notes, err := getNotesForTask(db, task.ID)
	if err != nil {
		return nil, err
	}

	criteria, err := GetCriteria(db, task.ID)
	if err != nil {
		return nil, err
	}

	childBlockers := map[string][]string{}
	childLabels := map[int64][]string{}
	if len(children) > 0 {
		childIDs := make([]int64, 0, len(children))
		for _, c := range children {
			childIDs = append(childIDs, c.ID)
		}
		bm, err := GetBlockersForTaskIDs(db, childIDs)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if blks := bm[c.ID]; len(blks) > 0 {
				childBlockers[c.ShortID] = blks
			}
		}
		lm, err := GetLabelsForTaskIDs(db, childIDs)
		if err != nil {
			return nil, err
		}
		childLabels = lm
	}

	return &TaskInfo{
		Task:          task,
		Parent:        parent,
		Children:      children,
		ChildBlockers: childBlockers,
		ChildLabels:   childLabels,
		Blockers:      blockers,
		Blocked:       blocked,
		FoundIn:       foundIn,
		Surfaced:      surfaced,
		Labels:        labels,
		Notes:         notes,
		Criteria:      criteria,
	}, nil
}

// GetAncestors walks the parent chain of the given task and returns the
// ancestors in root-first order (root, ..., parent). The named task is not
// included. Returns an empty slice for a root task.
func GetAncestors(db *sql.DB, shortID string) ([]*Task, error) {
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	var chain []*Task
	cur := task
	for cur.ParentID != nil {
		row := db.QueryRow(`
			SELECT id, short_id, parent_id, title, description, status, sort_order,
			       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
			FROM tasks WHERE id = ? AND deleted_at IS NULL
		`, *cur.ParentID)
		p, err := scanTask(row)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, p)
		cur = p
	}

	// Reverse to root-first order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// getNotesForTask returns the chronological list of `noted` events for a
// task, with the body extracted from the event detail JSON.
func getNotesForTask(db *sql.DB, taskID int64) ([]NoteEntry, error) {
	rows, err := db.Query(`
		SELECT actor, detail, created_at
		FROM events
		WHERE task_id = ? AND event_type = 'noted'
		ORDER BY created_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []NoteEntry
	for rows.Next() {
		var actor, detailJSON string
		var createdAt int64
		if err := rows.Scan(&actor, &detailJSON, &createdAt); err != nil {
			return nil, err
		}
		var detail map[string]any
		if detailJSON != "" {
			_ = json.Unmarshal([]byte(detailJSON), &detail)
		}
		text, _ := detail["text"].(string)
		notes = append(notes, NoteEntry{Actor: actor, Text: text, CreatedAt: createdAt})
	}
	return notes, rows.Err()
}
