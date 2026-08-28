package job

import (
	"database/sql"
	"fmt"
	"strings"
)

// OrientView is the assembled, notes-enriched regeneration of a plan tree
// around a target leaf. It pairs a synthesized Header (the actionable
// punchline an agent would otherwise compute) with the full Tree of the
// target's root (or a narrower --scope subtree), each node carrying its
// status, full description, criteria with state, and substantive notes.
//
// RunOrient assembles the view; rendering (YAML, later markdown) is a
// separate concern handled by the renderer behind its own interface.
type OrientView struct {
	Header OrientHeader
	Tree   *OrientNode
}

// OrientHeader is the first-class `orient:` block: synthesis the agent would
// otherwise have to compute. It does not duplicate note bodies (those live in
// the tree) except for OwnNotes, the target's own prior progress notes, which
// are hoisted for primacy.
type OrientHeader struct {
	Target     string      // target short id
	Title      string      // target title
	Root       string      // short id of the target's true root
	Status     string      // target status
	BlockedBy  []string    // blocker short ids
	Blocks     []OrientRef // tasks finishing the target unblocks
	Criteria   CriteriaTally
	OwnNotes   []NoteEntry // target's own prior notes, inlined (empty when none)
	WeighNotes []string    // same-parent sibling-leaf ids that carry notes
}

// OrientRef is a short id + title pair, used for the header's Blocks list.
type OrientRef struct {
	ID    string
	Title string
}

// CriteriaTally is the passed/total acceptance-criteria count for the target.
type CriteriaTally struct {
	Passed int
	Total  int
}

// OrientNode is one task in the rendered tree, in identity → spec → history
// field order: the task itself, its outbound blocks, criteria with state, and
// substantive notes (history) last. Target marks the orient target. Closed is
// the close timestamp (Unix seconds) for done nodes, 0 otherwise.
//
// Desc is the assembly-owned projection of Task.Description: renderers must
// read Desc, never Task.Description, so history elision stays an assembly
// decision. In the default (elided) view, done leaves drop desc, and done
// nodes of any shape drop notes and criteria; done containers keep desc
// because that's where the plan narrative lives.
type OrientNode struct {
	Task     *Task
	Target   bool
	Closed   int64    // close timestamp for done nodes; 0 when not done
	Desc     string   // renderable description; "" when elided
	Labels   []string // the node's own labels
	Blocks   []string // outbound block target short ids
	Criteria []Criterion
	// CompletionNote is the single "what just happened" breadcrumb of the
	// elided view: set on exactly one node in the tree — the most recently
	// closed done task that has a completion note — and empty everywhere
	// else. The full view leaves it empty (completion notes fold into Notes).
	CompletionNote string
	Notes          []NoteEntry
	Children       []*OrientNode
}

// RunOrient assembles an OrientView for a target leaf. An empty targetShortID
// resolves the target to the next available leaf (matching RunNext). The
// rendered tree defaults to the whole root tree containing the target; an
// optional scopeShortID narrows rendering to that subtree. Target and scope
// are orthogonal: scope only bounds what is rendered, never what is targeted.
//
// The default view elides done-node history (see OrientNode) so orient output
// stays within an agent's context budget as a plan accumulates history; use
// RunOrientOpts with full=true for the unelided view.
func RunOrient(db *sql.DB, targetShortID, scopeShortID, actor string) (*OrientView, error) {
	return RunOrientOpts(db, targetShortID, scopeShortID, actor, false)
}

// RunOrientOpts is RunOrient with the elision policy explicit: full=true
// keeps desc, notes, and criteria on every node regardless of status.
func RunOrientOpts(db *sql.DB, targetShortID, scopeShortID, actor string, full bool) (*OrientView, error) {
	target, err := resolveOrientTarget(db, targetShortID, actor)
	if err != nil {
		return nil, err
	}

	// The target's true root anchors both the default render scope and the
	// header's Root field. GetAncestors is root-first, so the root is its
	// first element (or the target itself when it is already a root).
	ancestors, err := GetAncestors(db, target.ShortID)
	if err != nil {
		return nil, err
	}
	root := target
	if len(ancestors) > 0 {
		root = ancestors[0]
	}

	scopeRoot := root
	if strings.TrimSpace(scopeShortID) != "" {
		scopeRoot, err = GetTaskByShortID(db, scopeShortID)
		if err != nil {
			return nil, err
		}
		if scopeRoot == nil {
			return nil, fmt.Errorf("task %q not found", scopeShortID)
		}
	}

	tree, err := buildOrientNode(db, scopeRoot, target.ID, full)
	if err != nil {
		return nil, err
	}
	if !full {
		if err := annotateRecentCompletionNote(db, tree); err != nil {
			return nil, err
		}
	}

	header, err := buildOrientHeader(db, target, root)
	if err != nil {
		return nil, err
	}

	return &OrientView{Header: *header, Tree: tree}, nil
}

// OrientNoTasksView is what orient renders in place of an OrientView when
// there is no available leaf to target: no synthesized header, just the same
// guidance RunNext would have failed with, plus whatever tasks already exist
// in scope — the focused root's tree when a focus scoped the failed lookup,
// or every root in the forest otherwise.
type OrientNoTasksView struct {
	Message string
	Trees   []*OrientNode
}

// RunOrientNoTasks assembles the guidance view for orient's no-target case.
// message is the caller-facing text from the failed next-task lookup (the
// ErrNoAvailableTasks error orient caught); it is already the right wording
// for either the focused-root or the whole-repo case.
func RunOrientNoTasks(db *sql.DB, actor, message string, full bool) (*OrientNoTasksView, error) {
	focus, err := GetFocus(db, actor)
	if err != nil {
		return nil, err
	}

	var roots []*Task
	if focus != nil {
		roots = []*Task{focus}
	} else {
		roots, err = getRootTasks(db)
		if err != nil {
			return nil, err
		}
	}

	var trees []*OrientNode
	for _, root := range roots {
		// targetID 0 never matches a real task id, so no node in these
		// trees is flagged as the (nonexistent) target.
		tree, err := buildOrientNode(db, root, 0, full)
		if err != nil {
			return nil, err
		}
		trees = append(trees, tree)
	}

	return &OrientNoTasksView{Message: message, Trees: trees}, nil
}

// resolveOrientTarget returns the positional target when an id is given, else
// the next available leaf (RunNext).
func resolveOrientTarget(db *sql.DB, targetShortID, actor string) (*Task, error) {
	if strings.TrimSpace(targetShortID) == "" {
		return RunNext(db, "", actor)
	}
	target, err := GetTaskByShortID(db, targetShortID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("task %q not found", targetShortID)
	}
	return target, nil
}

// buildOrientNode recursively assembles the subtree rooted at t, flagging the
// node whose id matches targetID. Unless full is set, done nodes are elided:
// notes and criteria are dropped outright, and desc survives only on done
// containers (children carry the plan narrative; done leaves are reduced to
// title/id/status/closed).
func buildOrientNode(db *sql.DB, t *Task, targetID int64, full bool) (*OrientNode, error) {
	elide := t.Status == "done" && !full

	node := &OrientNode{
		Task:   t,
		Target: t.ID == targetID,
	}

	if !elide {
		notes, err := substantiveNotes(db, t)
		if err != nil {
			return nil, err
		}
		criteria, err := GetCriteria(db, t.ID)
		if err != nil {
			return nil, err
		}
		node.Notes = notes
		node.Criteria = criteria
	}

	blocked, err := GetBlocked(db, t.ShortID)
	if err != nil {
		return nil, err
	}
	for _, b := range blocked {
		node.Blocks = append(node.Blocks, b.ShortID)
	}
	labels, err := GetLabels(db, t.ID)
	if err != nil {
		return nil, err
	}
	node.Labels = labels

	// Done nodes carry their close timestamp, sourced from the `done` event.
	if t.Status == "done" {
		_, ts, err := doneEventMeta(db, t.ID)
		if err != nil {
			return nil, err
		}
		node.Closed = ts
	}

	children, err := getChildren(db, t.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		child, err := buildOrientNode(db, c, targetID, full)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
	}

	// Desc is decided after children are known: an elided done leaf drops
	// it; a done container keeps it (the slice-level narrative).
	if !elide || len(node.Children) > 0 {
		node.Desc = t.Description
	}
	return node, nil
}

// buildOrientHeader synthesizes the orient header for the target, leaning on
// RunInfo for the bundled blockers/blocked/criteria. OwnNotes is computed via
// substantiveNotes so a completion note folds in when the target is itself
// done (RunInfo.Notes carries only `noted` events).
func buildOrientHeader(db *sql.DB, target, root *Task) (*OrientHeader, error) {
	info, err := RunInfo(db, target.ShortID)
	if err != nil {
		return nil, err
	}

	var blockedBy []string
	for _, b := range info.Blockers {
		blockedBy = append(blockedBy, b.ShortID)
	}

	var blocks []OrientRef
	for _, b := range info.Blocked {
		blocks = append(blocks, OrientRef{ID: b.ShortID, Title: b.Title})
	}

	tally := CriteriaTally{Total: len(info.Criteria)}
	for _, c := range info.Criteria {
		if c.State == CriterionPassed {
			tally.Passed++
		}
	}

	ownNotes, err := substantiveNotes(db, target)
	if err != nil {
		return nil, err
	}

	weigh, err := weighNotes(db, target)
	if err != nil {
		return nil, err
	}

	return &OrientHeader{
		Target:     target.ShortID,
		Title:      target.Title,
		Root:       root.ShortID,
		Status:     target.Status,
		BlockedBy:  blockedBy,
		Blocks:     blocks,
		Criteria:   tally,
		OwnNotes:   ownNotes,
		WeighNotes: weigh,
	}, nil
}

// weighNotes returns the short ids of the target's same-parent sibling leaves
// that carry substantive notes. Their bodies stay folded into the tree; the
// header only points at them. Branch siblings (those with children) are
// excluded — weigh_notes is a leaf-to-leaf signal.
func weighNotes(db *sql.DB, target *Task) ([]string, error) {
	if target.ParentID == nil {
		return nil, nil
	}
	siblings, err := getChildren(db, *target.ParentID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range siblings {
		if s.ID == target.ID {
			continue
		}
		children, err := getChildren(db, s.ID)
		if err != nil {
			return nil, err
		}
		if len(children) > 0 {
			continue // leaves only
		}
		notes, err := substantiveNotes(db, s)
		if err != nil {
			return nil, err
		}
		if len(notes) > 0 {
			out = append(out, s.ShortID)
		}
	}
	return out, nil
}

// substantiveNotes returns a task's notes filtered to substance: the `noted`
// events (via getNotesForTask) followed by the completion note, if any.
// Churn events (heartbeat, claimed, claim_expired, released, moved, labeled,
// blocked, …) never appear because they are not `noted` events and are not
// the completion note. The completion note is attributed to the `done` event's
// actor and timestamp on a best-effort basis.
func substantiveNotes(db *sql.DB, t *Task) ([]NoteEntry, error) {
	notes, err := getNotesForTask(db, t.ID)
	if err != nil {
		return nil, err
	}
	out := append([]NoteEntry(nil), notes...)
	if t.CompletionNote != nil && strings.TrimSpace(*t.CompletionNote) != "" {
		actor, ts, err := doneEventMeta(db, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, NoteEntry{Actor: actor, Text: *t.CompletionNote, CreatedAt: ts})
	}
	return out, nil
}

// doneEventMeta returns the actor and timestamp of a task's most recent `done`
// event, used to attribute its completion note. Returns zero values when no
// `done` event exists.
// annotateRecentCompletionNote sets CompletionNote on the single done node in
// the assembled tree whose note-bearing close is the most recent (done-event
// recency, ties broken by event id). Noteless closes — cascade-closed
// containers foremost — are passed over so they never blank the breadcrumb.
func annotateRecentCompletionNote(db *sql.DB, tree *OrientNode) error {
	byID := map[int64]*OrientNode{}
	var collect func(n *OrientNode)
	collect = func(n *OrientNode) {
		if n.Task.Status == "done" {
			byID[n.Task.ID] = n
		}
		for _, c := range n.Children {
			collect(c)
		}
	}
	collect(tree)
	if len(byID) == 0 {
		return nil
	}

	placeholders := make([]string, 0, len(byID))
	args := make([]any, 0, len(byID))
	for id := range byID {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	row := db.QueryRow(fmt.Sprintf(`
		SELECT e.task_id, t.completion_note
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.event_type = 'done'
		  AND t.completion_note IS NOT NULL AND t.completion_note != ''
		  AND e.task_id IN (%s)
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT 1
	`, strings.Join(placeholders, ",")), args...)
	var taskID int64
	var note string
	if err := row.Scan(&taskID, &note); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	byID[taskID].CompletionNote = note
	return nil
}

func doneEventMeta(db *sql.DB, taskID int64) (string, int64, error) {
	row := db.QueryRow(`
		SELECT actor, created_at
		FROM events
		WHERE task_id = ? AND event_type = 'done'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, taskID)
	var actor string
	var ts int64
	if err := row.Scan(&actor, &ts); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, nil
		}
		return "", 0, err
	}
	return actor, ts, nil
}
