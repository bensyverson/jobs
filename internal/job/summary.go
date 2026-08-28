package job

import (
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"
)

// Summary is a two-level rollup for `job summary <parent>`. The target's
// own rollup gives the headline numbers; one rollup per direct child
// adds enough granularity to tell which sub-phase needs attention,
// without redrawing the full tree. Next names the first claimable leaf
// in scope (nil when the scope has no claimable work). DecisionTasks
// lists open tasks labeled "decision" within the scope.
type Summary struct {
	Target         *SubtreeRollup
	DirectChildren []*SubtreeRollup
	Next           *Task
	// Focus is the actor's focused root in forest scope (nil when unset,
	// when no actor was given, or in subtree scope). When set, Next was
	// resolved inside it.
	Focus         *Task
	DecisionTasks []*Task
	// Issues summarizes work across issue-tree roots (nil when the database
	// has none, and always nil in subtree scope). BuildRollup never sets
	// this itself — forest-scope callers attach it via BuildIssuesStatus
	// so the claimed-actor scoping can differ from Next's (see that
	// function's doc comment) — but RenderSummary renders it when set.
	Issues *IssuesStatus
}

type SubtreeRollup struct {
	ShortID string
	Title   string
	Status  string
	HasKids bool
	// IsBlocked is true when the task itself has at least one open blocker.
	// Distinct from Blocked (which counts descendants), and from Status
	// (which stays "available" on a blocked-but-unclaimed task).
	IsBlocked bool
	Done      int
	Open      int
	Blocked   int
	Available int
	InFlight  int
	Canceled  int
	NextID    string
	// ClosedAt is the unix timestamp of the latest done/canceled event in
	// the subtree, but only set when the subtree is fully complete
	// (Open == 0 with at least one closed descendant). Zero otherwise.
	ClosedAt int64
}

func RunSummary(db *sql.DB, shortID string) (*Summary, error) {
	target, err := GetTaskByShortID(db, shortID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}
	return BuildRollup(db, target, "")
}

// BuildRollup is the shared subtree-rollup engine used by both `status`
// (forest scope, target=nil) and `summary` (subtree scope, target!=nil).
// When target is nil the result's Target is nil and DirectChildren
// enumerates every top-level task; when target is non-nil the result
// mirrors the target-plus-direct-children shape `summary <id>` has
// always produced.
//
// In forest scope the actor's focus (when set) scopes Next to the focused
// root and is carried on Summary.Focus for rendering. Subtree scope ignores
// focus — an explicit target always wins.
func BuildRollup(db *sql.DB, target *Task, actor string) (*Summary, error) {
	var targetRollup *SubtreeRollup
	var directChildren []*Task
	var err error

	if target != nil {
		targetRollup, err = buildRollup(db, target)
		if err != nil {
			return nil, err
		}
		directChildren, err = getChildren(db, target.ID)
	} else {
		directChildren, err = getRootTasks(db)
	}
	if err != nil {
		return nil, err
	}

	var childRollups []*SubtreeRollup
	for _, c := range directChildren {
		// Issue-tree roots are demoted out of the per-root rollup entirely —
		// they get their own Issues: line instead (decision 4,
		// project/2026-08-28-issues-ux.md). BuildIssuesStatus computes that
		// line; the caller attaches it to Summary.Issues.
		if target == nil && c.Kind.IsIssue() {
			continue
		}
		cr, err := buildRollup(db, c)
		if err != nil {
			return nil, err
		}
		// In forest scope, hide root tasks that are fully closed with no open
		// descendants — they add noise without actionable signal.
		if target == nil && cr.Open == 0 && (cr.Status == "done" || cr.Status == "canceled") {
			continue
		}
		childRollups = append(childRollups, cr)
	}

	// Next: the first claimable leaf inside scope. Forest scope scans the
	// whole DB — unless the actor has a focus, which narrows the scan to
	// the focused root; subtree scope scans under target.
	var parentID *int64
	var focus *Task
	if target != nil {
		parentID = &target.ID
	} else if actor != "" {
		focus, err = GetFocus(db, actor)
		if err != nil {
			return nil, err
		}
		if focus != nil {
			parentID = &focus.ID
		}
	}
	// Unfocused, `status` answers the same question as `next` and must give
	// the same answer: issue trees are not the plan's next step. A focus (or
	// an explicit root) is an explicit scope and overrides that, exactly as
	// it does in RunNextFiltered.
	leaves, err := queryAvailableLeafFrontier(db, parentID, 1, "", KindTask)
	if err != nil {
		return nil, err
	}
	var next *Task
	if len(leaves) > 0 {
		next = leaves[0]
	}

	// Decisions stay scope-wide (never focus-narrowed): status is the
	// landscape briefing, and a pending decision in another tree is
	// exactly the kind of thing it must not hide.
	var decisionScope *int64
	if target != nil {
		decisionScope = &target.ID
	}
	decisions, err := queryDecisionTasks(db, decisionScope)
	if err != nil {
		return nil, err
	}

	return &Summary{Target: targetRollup, DirectChildren: childRollups, Next: next, Focus: focus, DecisionTasks: decisions}, nil
}

// queryDecisionTasks returns open tasks labeled "decision" within the
// given scope. When parentID is nil the whole DB is searched.
func queryDecisionTasks(db *sql.DB, parentID *int64) ([]*Task, error) {
	var rows *sql.Rows
	var err error
	if parentID == nil {
		rows, err = db.Query(`
			SELECT t.id, t.short_id, t.title, t.status
			FROM tasks t
			JOIN task_labels l ON l.task_id = t.id
			WHERE l.name = 'decision'
			  AND t.status NOT IN ('done', 'canceled')
			  AND t.deleted_at IS NULL
			ORDER BY t.sort_order, t.id
		`)
	} else {
		rows, err = db.Query(`
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT t.id, t.short_id, t.title, t.status
			FROM tasks t
			JOIN subtree s ON s.id = t.id
			JOIN task_labels l ON l.task_id = t.id
			WHERE l.name = 'decision'
			  AND t.status NOT IN ('done', 'canceled')
			  AND t.deleted_at IS NULL
			ORDER BY t.sort_order, t.id
		`, *parentID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t := &Task{}
		if err := rows.Scan(&t.ID, &t.ShortID, &t.Title, &t.Status); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// buildRollup walks the descendants of t (excluding t itself) and
// computes count slices. NextID names the first claimable leaf scoped
// to t — uses the same machinery as `next [parent]`.
func buildRollup(db *sql.DB, t *Task) (*SubtreeRollup, error) {
	r := &SubtreeRollup{
		ShortID: t.ShortID,
		Title:   t.Title,
		Status:  t.Status,
	}

	selfBlockers, err := openBlockerCounts(db, []int64{t.ID})
	if err != nil {
		return nil, err
	}
	r.IsBlocked = selfBlockers[t.ID] > 0

	descendants, err := collectDescendants(db, t.ID)
	if err != nil {
		return nil, err
	}
	r.HasKids = len(descendants) > 0

	descIDs := make([]int64, 0, len(descendants))
	for _, d := range descendants {
		descIDs = append(descIDs, d.ID)
	}
	blockerCounts, err := openBlockerCounts(db, descIDs)
	if err != nil {
		return nil, err
	}

	for _, d := range descendants {
		switch d.Status {
		case "done":
			r.Done++
		case "canceled":
			r.Canceled++
		case "claimed":
			r.Open++
			r.InFlight++
		default:
			r.Open++
			isBlocked := blockerCounts[d.ID] > 0
			if isBlocked {
				r.Blocked++
			}
		}
	}

	// "Available" reports the truly claimable leaves — same definition as
	// `next` and the leaf-frontier docs. Reuse the canonical query so we
	// don't drift.
	avail, err := queryAvailableTasks(db, t.ShortID, 0, "", false, "")
	if err != nil {
		return nil, err
	}
	r.Available = len(avail)
	if len(avail) > 0 {
		r.NextID = avail[0].ShortID
	}

	// A non-leaf subtree with zero open descendants and at least one
	// closed descendant is "fully complete". Source the closure
	// timestamp from the latest done/canceled event within scope.
	if r.HasKids && r.Open == 0 && (r.Done+r.Canceled) > 0 {
		ts, err := subtreeClosureTime(db, t.ID)
		if err != nil {
			return nil, err
		}
		r.ClosedAt = ts
	}

	return r, nil
}

// subtreeClosureTime returns the max created_at across done/canceled
// events in the subtree rooted at taskID. Zero if none.
func subtreeClosureTime(db *sql.DB, taskID int64) (int64, error) {
	var ts sql.NullInt64
	err := db.QueryRow(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
			UNION ALL
			SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT MAX(e.created_at) FROM events e
		JOIN subtree s ON s.id = e.task_id
		WHERE e.event_type IN ('done', 'canceled')
	`, taskID).Scan(&ts)
	if err != nil {
		return 0, err
	}
	if ts.Valid {
		return ts.Int64, nil
	}
	return 0, nil
}

func collectDescendants(db *sql.DB, taskID int64) ([]*Task, error) {
	var out []*Task
	queue := []int64{taskID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		children, err := getChildren(db, id)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			out = append(out, c)
			queue = append(queue, c.ID)
		}
	}
	return out, nil
}

// openBlockerCounts returns the count of open (non-done) blockers per
// task id. A task with count > 0 is currently blocked.
func openBlockerCounts(db *sql.DB, taskIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(taskIDs))
	if len(taskIDs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(taskIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		args = append(args, id)
	}
	rows, err := db.Query(`
		SELECT b.blocked_id, COUNT(*)
		FROM blocks b
		JOIN tasks t ON t.id = b.blocker_id
		WHERE b.blocked_id IN (`+placeholders+`)
		  AND t.status != 'done'
		  AND t.deleted_at IS NULL
		GROUP BY b.blocked_id
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func RenderSummary(w io.Writer, s *Summary) {
	if t := s.Target; t != nil {
		fmt.Fprintf(w, "%s · %s\n", t.Title, t.ShortID)
		if t.HasKids {
			fmt.Fprintf(w, "  %s\n", rollupLine(t))
		} else {
			fmt.Fprintf(w, "  status: %s\n", t.Status)
		}
	}

	// Leaf-only collapse: when every direct child is a leaf, the per-child
	// block is padding — the headline already says everything worth saying
	// at this granularity. Show only claimed children (the "who's working
	// on what" signal); roll the rest into the headline counts.
	leafOnly := len(s.DirectChildren) > 0
	for _, c := range s.DirectChildren {
		if c.HasKids {
			leafOnly = false
			break
		}
	}

	for _, c := range s.DirectChildren {
		if leafOnly && c.Status != "claimed" {
			continue
		}
		tail := summarizeChild(c)
		if tail == "" {
			fmt.Fprintf(w, "  %s (%s)\n", c.Title, c.ShortID)
		} else {
			fmt.Fprintf(w, "  %s (%s): %s\n", c.Title, c.ShortID, tail)
		}
	}

	if i := s.Issues; i != nil {
		if i.Next != nil {
			fmt.Fprintf(w, "Issues: %d open (%d claimed) · next %s\n", i.Open, i.Claimed, i.Next.ShortID)
		} else {
			fmt.Fprintf(w, "Issues: %d open (%d claimed)\n", i.Open, i.Claimed)
		}
	}

	if s.Focus != nil {
		fmt.Fprintf(w, "Focus: %s %q\n", s.Focus.ShortID, s.Focus.Title)
	}
	if s.Next != nil {
		fmt.Fprintf(w, "Next: %s %q\n", s.Next.ShortID, s.Next.Title)
	} else if s.Focus != nil {
		// Exhausted focused root: keep the loud-failure contract instead
		// of silently pointing at another tree.
		fmt.Fprintf(w, "Next: none available in focused root — 'claim --next <id>' in another tree to shift focus, or 'job focus --release' to release it\n")
	}
}

// rollupLine formats the target's scoreboard. Zero-count tokens are
// suppressed so the eye lands on actionable numbers. If the subtree is
// fully complete, the closure timestamp is appended.
func rollupLine(t *SubtreeRollup) string {
	parts := []string{fmt.Sprintf("%d of %d done", t.Done, t.Done+t.Open)}
	if t.Blocked > 0 {
		parts = append(parts, fmt.Sprintf("%d blocked", t.Blocked))
	}
	if t.Available > 0 {
		parts = append(parts, fmt.Sprintf("%d available", t.Available))
	}
	if t.InFlight > 0 {
		parts = append(parts, fmt.Sprintf("%d in flight", t.InFlight))
	}
	if t.Canceled > 0 {
		parts = append(parts, fmt.Sprintf("%d canceled", t.Canceled))
	}
	if t.ClosedAt > 0 {
		parts = append(parts, "closed "+time.Unix(t.ClosedAt, 0).Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, " · ")
}

// summarizeChild renders the per-child rollup tail. An empty return
// signals the caller to skip the trailing ": …" entirely.
//   - non-leaf child with open work: "X of Y done · next <id>"
//   - any child whose status matches the scope's dominant expectation
//     (done, available): empty tail — the headline already says it.
//   - exceptions (claimed, canceled, blocked, anything else): show as
//     ": <status>".
func summarizeChild(c *SubtreeRollup) string {
	if c.HasKids && c.Open > 0 {
		tail := fmt.Sprintf("%d of %d done", c.Done, c.Done+c.Open)
		if c.NextID != "" {
			tail += " · next " + c.NextID
		}
		return tail
	}
	// Leaf or fully-closed subtree → show only genuine exceptions.
	// An "available" child with an open blocker is effectively blocked;
	// surface that instead of the raw status.
	if c.Status == "available" && c.IsBlocked {
		return "blocked"
	}
	switch c.Status {
	case "done", "available":
		return ""
	}
	return c.Status
}
