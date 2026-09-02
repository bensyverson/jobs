package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

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

	result := &AddResult{}
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		var parent *Task
		var parentID *int64
		if parentShortID != "" {
			p, err := GetTaskByShortID(tx, parentShortID)
			if err != nil {
				return err
			}
			if p == nil {
				return fmt.Errorf("task %q not found", parentShortID)
			}
			parent = p
			parentID = &p.ID
		}

		shortID, err := generateShortID(tx)
		if err != nil {
			return err
		}

		var sortKey string
		if beforeShortID != "" {
			beforeTask, err := GetTaskByShortID(tx, beforeShortID)
			if err != nil {
				return err
			}
			if beforeTask == nil {
				return fmt.Errorf("task %q not found", beforeShortID)
			}
			if (beforeTask.ParentID == nil) != (parentID == nil) {
				return fmt.Errorf("task %q is not a sibling of the new task", beforeShortID)
			}
			if beforeTask.ParentID != nil && parentID != nil && *beforeTask.ParentID != *parentID {
				return fmt.Errorf("task %q is not a sibling of the new task", beforeShortID)
			}
			sortKey, err = sortKeyBeforeSibling(tx, parentID, beforeTask, noSortKeyExclusion)
			if err != nil {
				return err
			}
		} else {
			sortKey, err = appendSortKey(tx, parentID, noSortKeyExclusion)
			if err != nil {
				return err
			}
		}

		createdPayload := CreatedPayload{
			ShortID:     shortID,
			ParentID:    parentShortID,
			Title:       title,
			Description: desc,
			SortKey:     sortKey,
		}
		if kind.IsIssue() {
			createdPayload.Kind = string(kind)
		}
		if err := b.emit(tx, EventCreated, shortID, actor, createdPayload); err != nil {
			return err
		}
		result.ShortID = shortID

		if len(labels) > 0 {
			normalized, err := normalizeLabelNames(labels)
			if err != nil {
				return err
			}
			taskID, ok, err := taskRowID(tx, shortID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("task %s vanished between create and label", shortID)
			}
			// Labels are the relations family (leaf fnD3D): the write and the
			// event move onto apply together there, not here.
			if _, _, err := insertLabels(tx, taskID, normalized); err != nil {
				return err
			}
			if err := recordEvent(tx, taskID, EventLabeled, actor, LabeledPayload{
				Names:    normalized,
				Existing: []string{},
			}); err != nil {
				return err
			}
		}

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
			if err := b.emit(tx, EventReleased, parent.ShortID, actor, ReleasedPayload{
				AutoReleased:     true,
				TriggeredByChild: shortID,
				WasClaimedBy:     prior,
				WasExpiresAt:     priorExpires,
			}); err != nil {
				return err
			}
			result.AutoReleasedParent = parent.ShortID
			result.AutoReleasedByActor = prior
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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

func RunEdit(db *sql.DB, shortID string, newTitle, newDesc *string, actor string) error {
	if newTitle == nil && newDesc == nil {
		return fmt.Errorf("edit requires --title and/or --desc")
	}

	return commit(db, func(tx dbtx, b *eventBatch) error {
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

		payload := EditedPayload{}
		if newTitle != nil {
			payload.OldTitle = new(task.Title)
			payload.NewTitle = newTitle
		}
		if newDesc != nil {
			payload.OldDesc = new(task.Description)
			payload.NewDesc = newDesc
		}

		if err := b.emit(tx, EventEdited, shortID, actor, payload); err != nil {
			return err
		}
		return maybeExtendClaim(tx, task.ID, actor)
	})
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

	return commit(db, func(tx dbtx, b *eventBatch) error {
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

		payload := NotedPayload{Text: text}
		if resultVal != nil {
			payload.Result = resultVal
		}
		if err := b.emit(tx, EventNoted, shortID, actor, payload); err != nil {
			return err
		}
		return maybeExtendClaim(tx, task.ID, actor)
	})
}

func RunMove(db *sql.DB, shortID, direction, relativeToShortID, actor string) error {
	return commit(db, func(tx dbtx, b *eventBatch) error {
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

		// The moving task is excluded from the neighbour search: it is leaving
		// its old position, and no other row's key is touched.
		var newSortKey string
		if direction == "before" {
			newSortKey, err = sortKeyBeforeSibling(tx, task.ParentID, relative, task.ID)
		} else {
			newSortKey, err = sortKeyAfterSibling(tx, task.ParentID, relative, task.ID)
		}
		if err != nil {
			return err
		}

		return b.emit(tx, EventMoved, shortID, actor, MovedPayload{
			Direction:  direction,
			RelativeTo: relativeToShortID,
			SortKey:    newSortKey,
			OldSortKey: task.SortKey,
		})
	})
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
	return commit(db, func(tx dbtx, b *eventBatch) error {
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

		var newSortKey string
		if direction == "" {
			newSortKey, err = appendSortKey(tx, newParentID, task.ID)
			if err != nil {
				return err
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
				newSortKey, err = sortKeyBeforeSibling(tx, newParentID, relative, task.ID)
			} else {
				newSortKey, err = sortKeyAfterSibling(tx, newParentID, relative, task.ID)
			}
			if err != nil {
				return err
			}
		}

		payload := ReparentedPayload{
			PriorParentID: priorParentShort,
			NewParentID:   newParentShortOut,
			SortKey:       newSortKey,
			OldSortKey:    task.SortKey,
		}
		if direction != "" {
			payload.Direction = direction
			payload.RelativeTo = relativeToShortID
		}
		if err := b.emit(tx, EventReparented, shortID, actor, payload); err != nil {
			return err
		}

		if newParentID == nil {
			return nil
		}
		newParent, err := getTaskByID(tx, *newParentID)
		if err != nil {
			return err
		}
		if newParent == nil || newParent.Status != "claimed" {
			return nil
		}
		prior := ""
		if newParent.ClaimedBy != nil {
			prior = *newParent.ClaimedBy
		}
		var priorExpires int64
		if newParent.ClaimExpiresAt != nil {
			priorExpires = *newParent.ClaimExpiresAt
		}
		return b.emit(tx, EventReleased, newParent.ShortID, actor, ReleasedPayload{
			AutoReleased:     true,
			TriggeredByChild: task.ShortID,
			WasClaimedBy:     prior,
			WasExpiresAt:     priorExpires,
		})
	})
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
// checks forward siblings (sort_key strictly greater than the
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

	// cameFromID / cameFromSortKey describe the child of the current
	// ancestor that is on the path from the closed task. At the first
	// iteration this is `closed` itself; each subsequent iteration
	// steps cameFrom up one level.
	cameFromID := closed.ID
	cameFromSortKey := closed.SortKey

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

		// Forward candidates: sort_key strictly greater than came-from's.
		var forward, earlier []*Task
		for _, c := range children {
			if c.ID == cameFromID {
				continue
			}
			if c.SortKey > cameFromSortKey {
				forward = append(forward, c)
			} else if c.SortKey < cameFromSortKey {
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
		cameFromSortKey = parent.SortKey
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
		SELECT id, short_id, parent_id, title, description, status, sort_key,
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
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id IS NULL AND deleted_at IS NULL
		ORDER BY sort_key
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
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
		ORDER BY sort_key
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
		if s.SortKey <= closed.SortKey {
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
// leaf (depth-first by sort_key). Returns nil if the subtree is entirely
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
			SELECT id, short_id, parent_id, title, description, status, sort_key,
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
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
		ORDER BY sort_key
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
			SELECT id, short_id, parent_id, title, description, status, sort_key,
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
