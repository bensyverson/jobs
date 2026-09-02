package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// TaskPageData is the payload rendered by the task-detail template.
// Fields stay flat so templates don't have to dig through nested
// "Task.Foo.Bar" chains.
type TaskPageData struct {
	templates.Chrome
	ShortID        string
	Title          string
	Description    string
	Status         string
	Labels         []TaskLabel
	Parent         *TaskRef
	BlockedBy      []TaskRef
	Blocking       []TaskRef
	Criteria       []TaskCriterion
	CompletionNote string
	// CancelReason is the prose recorded on the canceled event for a
	// canceled task. Empty for tasks that aren't canceled. The detail
	// view renders it in a "Cancel reason" section parallel to the
	// "Completion note" section that done tasks render.
	CancelReason  string
	ClaimedBy     string
	ProgressNotes []TaskProgressNote
	History       []TaskHistoryEntry
	// FoundIn is the task that surfaced this one, or nil when no
	// found-in edge is recorded. Provenance, not sequence: the edge
	// gates nothing, so it renders as a plain reference row beside
	// Parent rather than alongside the blocker sections.
	FoundIn *TaskRef
	// Surfaced is the other end of that edge — the tasks recorded as
	// found in this one, open and closed alike. Empty for the common
	// case. The peek sheet deliberately omits it (spec: Dashboard →
	// Task page); only the full page renders the list.
	Surfaced []TaskRef
	// IsIssue is true when this task's *root* is an issue-tree. Kind
	// is a property of the root only, so a child reports its root's
	// kind. Drives the p-task--issue page variant.
	IsIssue bool
	// Links is the prose resolver for every body this page renders —
	// the short ids the inline pass may turn into links, resolved once
	// against the store rather than per body.
	Links job.ProseLinks
}

// TaskProgressNote is one row in the "Progress notes" section, ordered
// newest-first. The body is the raw note text; the avatar and actor
// link mirror how History rows render their actor.
type TaskProgressNote struct {
	Actor    string
	ActorURL string
	Text     string
	RelTime  string
	ISOTime  string
	// Links is the page's prose resolver, copied onto every row because
	// the note body renders inside a partial, where the page's own data
	// is out of scope. One map, shared by every row.
	Links job.ProseLinks
}

// TaskCriterion is one row in the Criteria checklist on the task page
// and peek sheet. State is one of "pending", "passed", "skipped",
// "failed" — same vocabulary as the CLI's CriterionState. StateBadge
// is the human-readable trailing label rendered for non-pending rows;
// blank when the row is pending and the section just shows the empty
// glyph.
type TaskCriterion struct {
	// ShortID is the criterion's stable 3-char handle. The task page
	// renders it as the row's id attribute so prose elsewhere can link
	// to /tasks/<task>#crit-<short id>.
	ShortID    string
	Label      string
	State      string
	StateBadge string
}

// TaskRef is a minimal reference used for parents, blockers, and the
// "blocks" list — just enough to render a pill + title.
type TaskRef struct {
	ShortID string
	Title   string
	Status  string
	URL     string
}

// TaskLabel carries the label name and the URL that scopes a plan or
// log view to just that label.
type TaskLabel struct {
	Name string
	URL  string
}

// TaskHistoryEntry is one row in the history section of the detail
// view, pre-rendered to avoid template-side conditionals. The actor's
// verb (and timestamp) is the entire row — note bodies live in their
// own Progress notes / Completion note sections above, so History
// stays a terse audit trail.
//
// ClusterSummary carries the parenthetical roll-up for criterion_state
// events that the close swept up: a contiguous trailing run of
// criterion_state events on the same task collapses onto the done row
// as ` (8 criteria marked passed)` (or ` (5 passed, 2 failed)` when
// mixed) instead of flooding the timeline as N+1 separate rows.
type TaskHistoryEntry struct {
	EventType      string
	Actor          string
	Verb           string
	RelTime        string
	ISOTime        string
	ActorURL       string
	ClusterSummary string
}

// Task renders /tasks/<id>. See vision §3.5. The full-page view
// mirrors the peek sheet's sections (Labels, Parent, Blocks, Notes,
// History); a non-existent id 404s rather than rendering an empty
// shell.
func Task(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := loadTaskPageData(deps, w, r)
		if !ok {
			return
		}
		renderPage(deps, w, "task", data)
	})
}

// loadTaskPageData centralizes the data loading shared between the
// full-page Task view and the Peek fragment. Returns ok=false after
// writing an error response (404 or 500); callers should return
// immediately when ok is false.
func loadTaskPageData(deps Deps, w http.ResponseWriter, r *http.Request) (TaskPageData, bool) {
	shortID := r.PathValue("id")
	if shortID == "" {
		NotFound(deps).ServeHTTP(w, r)
		return TaskPageData{}, false
	}

	task, err := job.GetTaskByShortID(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "task lookup", err)
		return TaskPageData{}, false
	}
	if task == nil {
		RenderError(deps, w, http.StatusNotFound,
			"Task not found",
			"No task exists with id "+shortID+". It may have been deleted, or the link may be mistyped.")
		return TaskPageData{}, false
	}

	labelNames, err := job.GetLabels(deps.DB, task.ID)
	if err != nil {
		InternalError(deps, w, "labels", err)
		return TaskPageData{}, false
	}
	parent, err := job.GetTaskParent(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "parent", err)
		return TaskPageData{}, false
	}
	blockers, err := job.GetBlockers(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "blockers", err)
		return TaskPageData{}, false
	}
	blocking, err := job.GetBlocking(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "blocking", err)
		return TaskPageData{}, false
	}
	events, err := job.GetEventsForTask(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "events", err)
		return TaskPageData{}, false
	}
	criteria, err := job.GetCriteria(deps.DB, task.ID)
	if err != nil {
		InternalError(deps, w, "criteria", err)
		return TaskPageData{}, false
	}
	foundIn, err := job.GetFoundInSource(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "found-in source", err)
		return TaskPageData{}, false
	}
	surfaced, err := job.GetSurfaced(deps.DB, shortID)
	if err != nil {
		InternalError(deps, w, "surfaced", err)
		return TaskPageData{}, false
	}
	rootKind, err := rootKindOf(deps.DB, task)
	if err != nil {
		InternalError(deps, w, "root kind", err)
		return TaskPageData{}, false
	}

	// One batched lookup so the Blocked-by / Blocks / Parent rows
	// all get a blocker-aware status without an N+1 per related
	// task.
	relIDs := make([]int64, 0, len(blockers)+len(blocking)+len(surfaced)+2)
	for _, t := range blockers {
		relIDs = append(relIDs, t.ID)
	}
	for _, t := range blocking {
		relIDs = append(relIDs, t.ID)
	}
	for _, t := range surfaced {
		relIDs = append(relIDs, t.ID)
	}
	if parent != nil {
		relIDs = append(relIDs, parent.ID)
	}
	if foundIn != nil {
		relIDs = append(relIDs, foundIn.ID)
	}
	relBlockers, err := job.GetBlockersForTaskIDs(deps.DB, relIDs)
	if err != nil {
		InternalError(deps, w, "related blockers", err)
		return TaskPageData{}, false
	}

	chrome, err := newChrome(r.Context(), deps, "", time.Now())
	if err != nil {
		InternalError(deps, w, "task initial frame", err)
		return TaskPageData{}, false
	}
	cancelReason := buildCancelReason(task.Status, events)

	notes := buildProgressNotes(events)
	// One resolver per page, built from every body the page will render,
	// then handed to each of them. The note rows carry their own copy
	// because they render inside a partial.
	bodies := make([]string, 0, len(notes)+3)
	bodies = append(bodies, task.Description, derefString(task.CompletionNote), cancelReason)
	for _, n := range notes {
		bodies = append(bodies, n.Text)
	}
	links, err := job.ResolveProseLinks(deps.DB, bodies)
	if err != nil {
		InternalError(deps, w, "prose links", err)
		return TaskPageData{}, false
	}
	for i := range notes {
		notes[i].Links = links
	}

	return TaskPageData{
		Chrome:         chrome,
		Links:          links,
		ShortID:        task.ShortID,
		Title:          task.Title,
		Description:    task.Description,
		Status:         DisplayStatus(task.Status, len(blockers) > 0),
		Labels:         buildTaskLabels(labelNames),
		Parent:         taskRefOrNil(parent, relBlockers),
		BlockedBy:      taskRefs(blockers, relBlockers),
		Blocking:       taskRefs(blocking, relBlockers),
		Criteria:       buildTaskCriteria(criteria),
		CompletionNote: derefString(task.CompletionNote),
		CancelReason:   cancelReason,
		ClaimedBy:      derefString(task.ClaimedBy),
		ProgressNotes:  notes,
		History:        buildHistory(events),
		FoundIn:        taskRefOrNil(foundIn, relBlockers),
		Surfaced:       taskRefs(surfaced, relBlockers),
		IsIssue:        rootKind.IsIssue(),
	}, true
}

// rootKindOf returns the tree kind of task's root. Kind attaches to
// the root only, so a child has to walk up for it; GetAncestors is
// root-first, making the root the first entry.
func rootKindOf(db *sql.DB, task *job.Task) (job.TreeKind, error) {
	if task.ParentID == nil {
		return task.Kind, nil
	}
	ancestors, err := job.GetAncestors(db, task.ShortID)
	if err != nil {
		return "", err
	}
	if len(ancestors) == 0 {
		return task.Kind, nil
	}
	return ancestors[0].Kind, nil
}

// buildCancelReason scans events newest-id first for the canceled
// event that most recently put the task into its current canceled
// state, and returns its "reason" detail field. Empty for tasks that
// aren't canceled or whose canceled event carries no reason.
//
// The reason lives only on the canceled event (the task row's
// completion_note column stays NULL on cancel), so the detail page
// has to read the event log to surface it.
func buildCancelReason(status string, events []job.EventEntry) string {
	if status != "canceled" {
		return ""
	}
	var bestID int64
	var best string
	for _, e := range events {
		if e.EventType != "canceled" {
			continue
		}
		if e.ID < bestID {
			continue
		}
		if e.Detail == "" {
			bestID = e.ID
			best = ""
			continue
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(e.Detail), &detail); err != nil {
			bestID = e.ID
			best = ""
			continue
		}
		if reason, ok := detail["reason"].(string); ok {
			bestID = e.ID
			best = reason
		} else {
			bestID = e.ID
			best = ""
		}
	}
	return best
}

// buildTaskCriteria pre-renders the criterion rows for the template.
// Mirrors the CLI's criterionGlyph vocabulary so screen-readers and
// color-blind users get a coherent story: pending rows have no badge
// (the empty checkbox carries the meaning); the other three each get
// a one-word badge that reads as the state.
func buildTaskCriteria(items []job.Criterion) []TaskCriterion {
	out := make([]TaskCriterion, len(items))
	for i, c := range items {
		state := string(c.State)
		var badge string
		switch c.State {
		case job.CriterionPassed:
			badge = "passed"
		case job.CriterionSkipped:
			badge = "skipped"
		case job.CriterionFailed:
			badge = "failed"
		}
		out[i] = TaskCriterion{ShortID: c.ShortID, Label: c.Label, State: state, StateBadge: badge}
	}
	return out
}

func buildTaskLabels(names []string) []TaskLabel {
	out := make([]TaskLabel, len(names))
	for i, n := range names {
		out[i] = TaskLabel{Name: n, URL: "/log?label=" + url.QueryEscape(n)}
	}
	return out
}

func taskRefOrNil(t *job.Task, blockerMap map[int64][]string) *TaskRef {
	if t == nil {
		return nil
	}
	r := toTaskRef(t, blockerMap)
	return &r
}

func taskRefs(ts []*job.Task, blockerMap map[int64][]string) []TaskRef {
	out := make([]TaskRef, len(ts))
	for i, t := range ts {
		out[i] = toTaskRef(t, blockerMap)
	}
	return out
}

func toTaskRef(t *job.Task, blockerMap map[int64][]string) TaskRef {
	hasBlockers := len(blockerMap[t.ID]) > 0
	return TaskRef{
		ShortID: t.ShortID,
		Title:   t.Title,
		Status:  DisplayStatus(t.Status, hasBlockers),
		URL:     "/tasks/" + t.ShortID,
	}
}

// buildProgressNotes filters the event log down to noted events with
// non-empty bodies, returning rows newest-first. Caller passes the
// event slice already sorted newest-first (GetEventsForTask uses
// ORDER BY created_at DESC, id DESC), so a forward walk preserves
// that order. Empty-body or malformed-detail events are skipped
// silently; History will still surface a "noted by" row for them, so
// the dashboard does not lose the fact that the event happened.
func buildProgressNotes(events []job.EventEntry) []TaskProgressNote {
	now := time.Now()
	out := make([]TaskProgressNote, 0)
	for _, e := range events {
		if e.EventType != "noted" {
			continue
		}
		text := noteTextFromDetail(e.Detail)
		if text == "" {
			continue
		}
		ts := time.Unix(e.CreatedAt, 0)
		out = append(out, TaskProgressNote{
			Actor:    e.Actor,
			ActorURL: "/actors/" + url.PathEscape(e.Actor),
			Text:     text,
			RelTime:  render.RelativeTime(now, ts),
			ISOTime:  ts.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func buildHistory(events []job.EventEntry) []TaskHistoryEntry {
	now := time.Now()
	rendered := make([]TaskHistoryEntry, len(events))
	for i, e := range events {
		ts := time.Unix(e.CreatedAt, 0)
		entry := TaskHistoryEntry{
			EventType: e.EventType,
			Actor:     e.Actor,
			Verb:      eventVerb(e.EventType, e.Detail),
			RelTime:   render.RelativeTime(now, ts),
			ISOTime:   ts.UTC().Format(time.RFC3339),
			ActorURL:  "/actors/" + url.PathEscape(e.Actor),
		}
		// System events have no human/agent doer. Blank the actor
		// fields so the template can skip the "<verb> by <actor>"
		// trailer and render just the verb (e.g. "claim expired").
		if e.EventType == "claim_expired" {
			entry.Actor = ""
			entry.ActorURL = ""
		}
		rendered[i] = entry
	}

	// Cluster the trailing run of criterion_state entries onto the
	// done that closes them. The rule is purely structural — no
	// timestamp window — because the live event log shows
	// criterion_state and done recorded in the same second when
	// they belong together. We walk events in chronological order
	// (sorted by id ascending) so the "trailing run" pattern is
	// well-defined regardless of the caller's input order; the
	// final output preserves the caller's order so the dashboard's
	// newest-first display still works.
	chronological := make([]int, len(events))
	for i := range chronological {
		chronological[i] = i
	}
	sortByID := func(events []job.EventEntry, indices []int) {
		// Insertion sort is fine — typical task event count is small.
		for i := 1; i < len(indices); i++ {
			for j := i; j > 0 && events[indices[j-1]].ID > events[indices[j]].ID; j-- {
				indices[j-1], indices[j] = indices[j], indices[j-1]
			}
		}
	}
	sortByID(events, chronological)

	swept := make(map[int]bool)
	for i := range chronological {
		idx := chronological[i]
		if events[idx].EventType != "done" {
			continue
		}
		// Walk backward over a contiguous run of criterion_state
		// events that immediately precede this done in chronological
		// order. Stop at the first non-criterion_state event.
		var clusterIdx []int
		for j := i - 1; j >= 0; j-- {
			cidx := chronological[j]
			if events[cidx].EventType != "criterion_state" {
				break
			}
			clusterIdx = append([]int{cidx}, clusterIdx...)
		}
		if len(clusterIdx) == 0 {
			continue
		}
		states := make([]string, 0, len(clusterIdx))
		for _, cidx := range clusterIdx {
			states = append(states, criterionStateOf(events[cidx].Detail))
			swept[cidx] = true
		}
		rendered[idx].ClusterSummary = clusterSummary(states)
	}

	out := make([]TaskHistoryEntry, 0, len(rendered)-len(swept))
	for i, entry := range rendered {
		if swept[i] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// eventVerb returns the display verb for an event type. The phrase
// is followed in templates by the actor name when the event has a
// human/agent actor, so verbs typically end in "by". For system
// events (e.g. claim expiration) the verb stands alone — the
// template suppresses the actor trailer when EventEntry.Actor is
// blanked out below. detail is consulted for events whose phrasing
// folds in payload data (criteria count, criterion label/state).
func eventVerb(t, detail string) string {
	switch t {
	case "created":
		return "added by"
	case "claimed":
		return "claimed by"
	case "done":
		return "done by"
	case "canceled":
		return "canceled by"
	case "released":
		return "released by"
	case "noted":
		return "noted by"
	case "blocked":
		return "blocked by"
	case "unblocked":
		return "unblocked by"
	case "claim_expired":
		return "claim expired"
	case "labeled":
		return "labeled by"
	case "criteria_added":
		return criteriaAddedVerb(detail) + " by"
	case "criterion_state":
		return criterionStateVerb(detail) + " by"
	default:
		return t + " by"
	}
}

// criteriaAddedVerb renders the count-aware phrase for a
// criteria_added event payload. Falls back to a count-less phrase if
// the detail is missing or malformed so a corrupt event still reads
// as prose rather than leaking the snake_case enum.
func criteriaAddedVerb(detailJSON string) string {
	count := 0
	if detailJSON != "" {
		var detail map[string]any
		if err := json.Unmarshal([]byte(detailJSON), &detail); err == nil {
			if list, ok := detail["criteria"].([]any); ok {
				count = len(list)
			}
		}
	}
	noun := "criteria"
	if count == 1 {
		noun = "criterion"
	}
	if count == 0 {
		return "added criteria"
	}
	return fmt.Sprintf("added %d %s", count, noun)
}

// criterionStateVerb renders the label+state phrase for a
// criterion_state event payload. Mirrors the CLI vocabulary so the
// two surfaces tell the same story.
func criterionStateVerb(detailJSON string) string {
	label, state := "", ""
	if detailJSON != "" {
		var detail map[string]any
		if err := json.Unmarshal([]byte(detailJSON), &detail); err == nil {
			if v, ok := detail["label"].(string); ok {
				label = v
			}
			if v, ok := detail["state"].(string); ok {
				state = v
			}
		}
	}
	if label == "" || state == "" {
		return "marked a criterion"
	}
	return fmt.Sprintf("marked %q %s", label, state)
}

// criterionStateOf extracts the "state" field from a criterion_state
// event's detail JSON. Returns the empty string if the payload is
// missing, malformed, or has no state — callers must tolerate this
// since the cluster summary keeps counting other states regardless.
func criterionStateOf(detailJSON string) string {
	if detailJSON == "" {
		return ""
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(detailJSON), &detail); err != nil {
		return ""
	}
	if v, ok := detail["state"].(string); ok {
		return v
	}
	return ""
}

// clusterSummary renders the parenthetical roll-up shown after the
// "done by <actor>" verb when criterion_state events were swept up
// into the close. Two shapes:
//
//   - Uniform: every criterion went to the same state — read as
//     "{N} criteri{a|on} marked {state}" (e.g. "8 criteria marked
//     passed", "1 criterion marked passed").
//   - Mixed: states differ — read as "{x} passed, {y} failed,
//     {z} skipped" with zero-count states omitted, in the canonical
//     order passed → failed → skipped → pending.
func clusterSummary(states []string) string {
	if len(states) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, s := range states {
		counts[s]++
	}
	// Drop empties; if every state was empty, fall back to a
	// count-only phrase rather than printing an awkward "N criteria
	// marked ".
	delete(counts, "")
	if len(counts) == 0 {
		noun := "criteria"
		if len(states) == 1 {
			noun = "criterion"
		}
		return fmt.Sprintf("%d %s", len(states), noun)
	}
	if len(counts) == 1 {
		var only string
		for k := range counts {
			only = k
		}
		noun := "criteria"
		if counts[only] == 1 {
			noun = "criterion"
		}
		return fmt.Sprintf("%d %s marked %s", counts[only], noun, only)
	}
	order := []string{"passed", "failed", "skipped", "pending"}
	parts := make([]string, 0, len(counts))
	for _, s := range order {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
			delete(counts, s)
		}
	}
	// Anything we didn't recognize (forward-compat) lands at the end.
	for s, n := range counts {
		parts = append(parts, fmt.Sprintf("%d %s", n, s))
	}
	return strings.Join(parts, ", ")
}

// DisplayStatus maps the DB's raw status column into the four visual
// status categories used by the dashboard's c-status-pill ("todo",
// "active", "blocked", "done"). An open task that has any unresolved
// blocker renders as "blocked" even though the column says "available"
// or "claimed" — the blocker relation is derived-state, not stored on
// the task row. Kept exported so handlers and future render helpers
// can share one normalization rule.
func DisplayStatus(raw string, hasOpenBlockers bool) string {
	switch raw {
	case "done":
		return "done"
	case "canceled":
		return "canceled"
	case "claimed":
		if hasOpenBlockers {
			return "blocked"
		}
		return "active"
	case "available":
		if hasOpenBlockers {
			return "blocked"
		}
		return "todo"
	default:
		return raw
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
