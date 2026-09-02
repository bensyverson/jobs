package handlers

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
)

// collectTaskIDs walks a task forest in pre-order and returns every
// task id. Single pass so we can batch the follow-up lookups.
func collectTaskIDs(nodes []*job.TaskNode) []int64 {
	var ids []int64
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			ids = append(ids, n.Task.ID)
			walk(n.Children)
		}
	}
	walk(nodes)
	return ids
}

// collectTitlesByShortID indexes the forest by short id so blocker
// refs can carry the blocker's title for the hover tooltip without a
// second DB round-trip.
func collectTitlesByShortID(nodes []*job.TaskNode) map[string]string {
	out := make(map[string]string)
	var walk func([]*job.TaskNode)
	walk = func(ns []*job.TaskNode) {
		for _, n := range ns {
			out[n.Task.ShortID] = n.Task.Title
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// buildPlanNodes maps the domain forest into template-ready PlanNodes.
// Post-order so children are built first; a node is collapsed only
// when every descendant (including itself) has a closed status, which
// matches "auto-collapse fully-done subtrees" from the task spec.
func buildPlanNodes(
	nodes []*job.TaskNode,
	labels map[int64][]string,
	blockers map[int64][]string,
	notes map[int64][]PlanNote,
	actors map[int64]string,
	titlesByShortID map[string]string,
	addLabelURLs map[string]string,
	now time.Time,
	depth int,
) []*PlanNode {
	out := make([]*PlanNode, 0, len(nodes))
	for _, n := range nodes {
		children := buildPlanNodes(n.Children, labels, blockers, notes, actors, titlesByShortID, addLabelURLs, now, depth+1)

		taskBlockers := blockers[n.Task.ID]
		displayStatus := DisplayStatus(n.Task.Status, len(taskBlockers) > 0)

		subtreeHasOpen := isOpenStatus(displayStatus)
		for _, c := range children {
			if !c.Collapsed || isOpenStatus(c.DisplayStatus) {
				subtreeHasOpen = true
				break
			}
		}

		// Rollup: a still-open branch whose subtree contains active
		// (claimed) work shows as active itself, so the tree glows
		// where something is actually in progress. Done and canceled
		// parents keep their own status — a closed branch stays closed
		// even if a reopened descendant has picked up life again.
		if isOpenStatus(displayStatus) {
			for _, c := range children {
				if c.DisplayStatus == "active" {
					displayStatus = "active"
					break
				}
			}
		}

		ts := time.Unix(n.Task.UpdatedAt, 0)
		hasChildren := len(children) > 0
		hasDesc := strings.TrimSpace(n.Task.Description) != ""
		out = append(out, &PlanNode{
			ShortID:       n.Task.ShortID,
			URL:           "/tasks/" + n.Task.ShortID,
			Title:         n.Task.Title,
			Description:   n.Task.Description,
			DisplayStatus: displayStatus,
			Actor:         actors[n.Task.ID],
			Labels:        buildRowLabels(labels[n.Task.ID], addLabelURLs),
			RelTime:       render.RelativeTime(now, ts),
			ISOTime:       ts.UTC().Format(time.RFC3339),
			BlockedBy:     buildBlockerRefs(taskBlockers, titlesByShortID),
			Notes:         markNotesStatus(notes[n.Task.ID], displayStatus),
			Children:      children,
			Depth:         depth,
			HasChildren:   hasChildren,
			Collapsible:   hasChildren || hasDesc,
			Collapsed:     !subtreeHasOpen,
		})
	}
	return out
}

// isOpenStatus is true for any status that still warrants attention.
// Done and canceled subtrees can collapse; everything else expands.
func isOpenStatus(displayStatus string) bool {
	return displayStatus != "done" && displayStatus != "canceled"
}

// buildRowLabels turns a row's plain label-name list into PlanRowLabels
// with their pre-computed enable-URLs.
func buildRowLabels(names []string, addLabelURLs map[string]string) []PlanRowLabel {
	if len(names) == 0 {
		return nil
	}
	out := make([]PlanRowLabel, len(names))
	for i, n := range names {
		out[i] = PlanRowLabel{Name: n, URL: addLabelURLs[n], Color: template.CSS(render.LabelColor(n))}
	}
	return out
}

func buildBlockerRefs(shortIDs []string, titlesByShortID map[string]string) []PlanBlockerRef {
	if len(shortIDs) == 0 {
		return nil
	}
	out := make([]PlanBlockerRef, len(shortIDs))
	for i, s := range shortIDs {
		out[i] = PlanBlockerRef{
			ShortID: s,
			URL:     "#task-" + s,
			Title:   titlesByShortID[s],
		}
	}
	return out
}

// markNotesStatus copies the task's display status onto each of its
// notes so the c-plan-note row can pick up the same tint as its
// parent task (muted when done, live when active, etc.).
func markNotesStatus(notes []PlanNote, displayStatus string) []PlanNote {
	if len(notes) == 0 {
		return nil
	}
	out := make([]PlanNote, len(notes))
	for i, n := range notes {
		n.DisplayStatus = displayStatus
		out[i] = n
	}
	return out
}

// loadPlanActors returns the display-actor for each task id: the most
// recent actor who claimed, completed, or canceled it. Tasks that
// have no such event (brand-new, never claimed) are absent from the
// map so the template renders an empty avatar slot. One query.
func loadPlanActors(db *sql.DB, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string)
	if len(ids) == 0 {
		return out, nil
	}
	q, args := inClause(
		`SELECT task_id, actor FROM events
		 WHERE event_type IN ('claimed','done','canceled') AND task_id IN `,
		ids)
	q += ` ORDER BY created_at ASC, id ASC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID int64
		var actor string
		if err := rows.Scan(&taskID, &actor); err != nil {
			return nil, err
		}
		out[taskID] = actor // last write wins → latest relevant event
	}
	return out, rows.Err()
}

// loadPlanNotes returns all 'noted' events grouped by task id, in
// chronological order. The note body is the `text` field of the
// JSON detail payload emitted by RunNote.
func loadPlanNotes(db *sql.DB, ids []int64) (map[int64][]PlanNote, error) {
	out := make(map[int64][]PlanNote)
	if len(ids) == 0 {
		return out, nil
	}
	q, args := inClause(
		`SELECT task_id, actor, detail, created_at FROM events
		 WHERE event_type = 'noted' AND task_id IN `,
		ids)
	q += ` ORDER BY created_at ASC, id ASC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	for rows.Next() {
		var taskID int64
		var actor, detail string
		var createdAt int64
		if err := rows.Scan(&taskID, &actor, &detail, &createdAt); err != nil {
			return nil, err
		}
		text := noteTextFromDetail(detail)
		if text == "" {
			continue
		}
		ts := time.Unix(createdAt, 0)
		out[taskID] = append(out[taskID], PlanNote{
			Actor:   actor,
			RelTime: render.RelativeTime(now, ts),
			ISOTime: ts.UTC().Format(time.RFC3339),
			Text:    text,
		})
	}
	return out, rows.Err()
}

// noteTextFromDetail extracts the body text from a 'noted' event's
// detail blob. Returns empty string if the JSON is malformed or the
// text field is missing/empty — a silent skip is kinder than a panic
// on a hand-edited DB row.
func noteTextFromDetail(detail string) string {
	if detail == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return ""
	}
	s, _ := payload["text"].(string)
	return strings.TrimSpace(s)
}

// attachProseLinks resolves the short ids mentioned in every row
// description on the page and hands the one resulting map to every node.
// Plan rows render notes as verbatim <pre>, so descriptions are the only
// prose the view carries.
func attachProseLinks(db *sql.DB, nodes []*PlanNode) error {
	var descs []string
	var walk func([]*PlanNode)
	walk = func(ns []*PlanNode) {
		for _, n := range ns {
			descs = append(descs, n.Description)
			walk(n.Children)
		}
	}
	walk(nodes)

	links, err := job.ResolveProseLinks(db, descs)
	if err != nil {
		return err
	}
	var assign func([]*PlanNode)
	assign = func(ns []*PlanNode) {
		for _, n := range ns {
			n.Links = links
			assign(n.Children)
		}
	}
	assign(nodes)
	return nil
}

// inClause builds `prefix (?,?,?,…)` for a fixed-length id slice and
// returns the bound args. Callers append their own ORDER BY / LIMIT.
// Kept local to plan.go because the log view's equivalent is trivial
// enough to live inline and the two diverge in shape.
func inClause(prefix string, ids []int64) (string, []any) {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("(")
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("?")
		args[i] = id
	}
	b.WriteString(")")
	return b.String(), args
}
