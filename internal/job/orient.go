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
type OrientNode struct {
	Task     *Task
	Target   bool
	Closed   int64    // close timestamp for done nodes; 0 when not done
	Labels   []string // the node's own labels
	Blocks   []string // outbound block target short ids
	Criteria []Criterion
	Notes    []NoteEntry
	Children []*OrientNode
}

// RunOrient assembles an OrientView for a target leaf. An empty targetShortID
// resolves the target to the next available leaf (matching RunNext). The
// rendered tree defaults to the whole root tree containing the target; an
// optional scopeShortID narrows rendering to that subtree. Target and scope
// are orthogonal: scope only bounds what is rendered, never what is targeted.
func RunOrient(db *sql.DB, targetShortID, scopeShortID, actor string) (*OrientView, error) {
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

	tree, err := buildOrientNode(db, scopeRoot, target.ID)
	if err != nil {
		return nil, err
	}

	header, err := buildOrientHeader(db, target, root)
	if err != nil {
		return nil, err
	}

	return &OrientView{Header: *header, Tree: tree}, nil
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
// node whose id matches targetID.
func buildOrientNode(db *sql.DB, t *Task, targetID int64) (*OrientNode, error) {
	notes, err := substantiveNotes(db, t)
	if err != nil {
		return nil, err
	}
	criteria, err := GetCriteria(db, t.ID)
	if err != nil {
		return nil, err
	}
	blocked, err := GetBlocked(db, t.ShortID)
	if err != nil {
		return nil, err
	}
	var blocks []string
	for _, b := range blocked {
		blocks = append(blocks, b.ShortID)
	}
	labels, err := GetLabels(db, t.ID)
	if err != nil {
		return nil, err
	}

	node := &OrientNode{
		Task:     t,
		Target:   t.ID == targetID,
		Labels:   labels,
		Blocks:   blocks,
		Criteria: criteria,
		Notes:    notes,
	}

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
		child, err := buildOrientNode(db, c, targetID)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, child)
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
