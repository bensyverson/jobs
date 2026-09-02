package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// LogFilters are the query-param-driven filters on /log. Zero-value
// means "no filter on that axis." Vision §6.4 defines the axes;
// multi-valued filters (repeated `&label=` etc.) are a later concern.
type LogFilters struct {
	Actor string
	Task  string
	Label string
	Type  string
	Since time.Time // zero means "no since floor"
	// Before is the "load older" cursor: only events strictly before this
	// log position are returned. The zero Position means "no cursor";
	// pagination starts from the newest event.
	Before eventlog.Position
	// Limit caps the number of rows returned. <=0 means "use the
	// default" (defaultLogLimit).
	Limit int
	// At is the time-travel upper bound: only events at or before this log
	// position are included. The zero Position means "no upper bound"
	// (live). Set together with AtInvalid so callers can distinguish
	// "absent" from "malformed."
	//
	// A log position rather than a row id because a rebuild renumbers row
	// ids: a bookmarked ?at= would silently land on a different event
	// after any pull.
	At        eventlog.Position
	AtInvalid bool
}

// Live reports whether f has no time-travel upper bound.
func (f LogFilters) Live() bool { return f.At == (eventlog.Position{}) }

// LogChip is one clickable chip in the log-view filter bar. HRef is a
// fully-formed query string so the template can emit <a href=…>.
type LogChip struct {
	Label  string
	HRef   string
	Active bool
	Actor  string // non-empty for actor chips — paints the avatar dot
	LabelK string // non-empty for label chips — paints the pill tint
	// Overflow marks a chip past the strip's cap: rendered, but
	// hidden until the "+N more" chip expands the strip.
	Overflow bool
}

// LogEventRow is one already-rendered row in the log view. Building
// this once on the server side keeps the template simple and pushes
// id-formatting / time-formatting into Go where it belongs.
type LogEventRow struct {
	// EventID is the event's cache row id, rendered on the row as
	// data-event-id. It is a DOM key and nothing else — a rebuild
	// renumbers it, so no cursor is derived from it.
	EventID int64
	// Position is the event's log position, the cursor the "load older"
	// link and the scrubber address it by.
	Position  eventlog.Position
	ShortID   string
	Actor     string
	EventType string
	// VerbText is the human-readable verb shown in the row. Defaults
	// to EventType, but some events get a friendlier label —
	// claim_expired reads as "expired" so it renders as a clean
	// "EXPIRED" after the CSS uppercase rather than the raw enum.
	VerbText string
	Title    string
	RelTime  string
	ISOTime  string
	TaskURL  string
	ActorURL string
	// IsSystem flags housekeeping events whose "actor" is the Jobs
	// runtime, not a human or agent (e.g. claim_expired emitted by
	// the expiration sweep). The template renders these without an
	// avatar/link so the prior claimer isn't surfaced as the doer.
	IsSystem bool
	// Metadata is the trailing per-event payload column on the log
	// row. Most events carry one of: a short text snippet (note body,
	// completion note, cancel reason, claim duration, criterion
	// label, label name, criteria list) or a task-id pill (blocker
	// short id for blocked/unblocked rows). Events with no payload
	// (created, released, claim_expired) leave Metadata zero-valued
	// so the cell renders empty but keeps row layout consistent.
	Metadata LogRowMetadata
}

// LogRowMetadata is the payload-column data for one log row. PillID
// and Text are mutually exclusive — PillID renders as a c-id-pill
// linking to /tasks/<short_id>; Text renders as plain content. State
// is set on criterion_state rows so the template can color the label
// the same way the Criteria section colors its SVG icons (passed →
// green, failed → red, skipped → muted).
type LogRowMetadata struct {
	Text   string
	PillID string
	State  string
	// Prefix is a short lead-in rendered immediately before the id
	// pill. Only found_in_cleared uses it today ("cleared, was
	// <id>"), where the id alone would not say what happened to it.
	Prefix string
}

// LogPageData is the full payload the log template renders.
type LogPageData struct {
	templates.Chrome
	Filters     LogFilters
	Events      []LogEventRow
	EventTypes  []LogChip
	Actors      LogChipStrip
	Labels      LogChipStrip
	TotalShown  int
	TotalEvents int
	// RangeTabs is the 7D / 14D / 30D / All link group in the view
	// header; EmptyText is the empty state worded for the current
	// window ("No events in the last 7 days").
	RangeTabs []RangeTab
	EmptyText string
	// EventsURL is the SSE subscription URL — /events plus the same
	// filter query params as the page itself, so the live tail only
	// delivers events that match the current filter state.
	EventsURL string
	// HasMore is true when there are older events beyond what was
	// rendered. Drives the "Load older" affordance at the bottom.
	HasMore bool
	// MoreURL is /log?…&before=<oldest position>, preserving every
	// other filter so the next page lands on the same view.
	MoreURL string
}

// defaultLogLimit caps the initial render so a 50k-event database
// doesn't render every row in one shot. The "Load older" affordance
// at the bottom navigates to the next page.
const defaultLogLimit = 200

// knownEventTypes is the canonical ordered set of event types surfaced
// in the filter bar. Order matches the prototype so users see the
// same layout regardless of which events are present in the DB.
// criteria_added and criterion_state are authoring-side activity (not
// state transitions like done/canceled), so they sit alongside noted
// rather than at the lifecycle end of the row.
var knownEventTypes = []string{
	"created", "claimed", "done", "blocked", "unblocked",
	"noted", "criteria_added", "criterion_state",
	"found_in_set", "found_in_cleared", "kind_changed",
	"released", "canceled",
}

// Log renders the filterable event stream view. See vision §3.4.
// SSR-only for now; the live-tail swap lands in a later phase.
func Log(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filters := ParseLogFilters(r.URL.Query())
		if filters.AtInvalid {
			RenderError(deps, w, http.StatusBadRequest,
				"Bad request",
				"?at must be a log position, as the scrubber writes it (<ts>-<replica>-<seq>).")
			return
		}

		now := time.Now()
		anchor, err := rangeAnchor(r.Context(), deps.DB, filters.At, now)
		if err != nil {
			InternalError(deps, w, "log range anchor", err)
			return
		}
		rg := parseRange(r.URL.Query(), anchor)
		chips := logChipCtx{
			f:        filters,
			rangeKey: rg.Key,
			chipsAll: r.URL.Query().Get("chips") == chipsAllValue,
		}

		events, totalEvents, hasMore, err := loadLogEvents(deps.DB, filters, rg)
		if err != nil {
			InternalError(deps, w, "log query", err)
			return
		}

		actors, err := actorsInRange(r.Context(), deps.DB, filters.At, rg)
		if err != nil {
			InternalError(deps, w, "actors query", err)
			return
		}
		labels, err := labelsInRange(r.Context(), deps.DB, filters.At, rg)
		if err != nil {
			InternalError(deps, w, "labels query", err)
			return
		}

		chrome, err := newChrome(r.Context(), deps, "log", now)
		if err != nil {
			InternalError(deps, w, "log initial frame", err)
			return
		}
		data := LogPageData{
			Chrome:      chrome,
			Filters:     filters,
			Events:      events,
			EventTypes:  buildTypeChips(chips),
			Actors:      buildActorChips(chips, actors),
			Labels:      buildLabelChips(chips, labels),
			TotalShown:  len(events),
			TotalEvents: totalEvents,
			EventsURL:   eventsURL(filters),
			HasMore:     hasMore,
			RangeTabs:   buildRangeTabs("/log", r.URL.Query(), rg.Key),
			EmptyText:   logEmptyText(filters, rg),
		}
		if hasMore && len(events) > 0 {
			data.MoreURL = moreURL(chips, events[len(events)-1].Position)
		}
		renderPage(deps, w, "log", data)
	})
}

// ParseLogFilters reads a /log query string into a LogFilters value.
// Unknown keys are ignored; "since" accepts RFC3339 first, then a
// fallback of a unix-seconds integer. Malformed since values are
// silently dropped — we'd rather render with a zero since than return
// a 400 for a bookmarked URL that drifted. Same forgiveness applies
// to before and limit: garbage parses to zero / default. ?at is the
// exception — it's the time-travel anchor and we want callers to
// distinguish "absent" from "malformed" so the handler can 400 on
// nonsense rather than silently render a different page.
func ParseLogFilters(q url.Values) LogFilters {
	f := LogFilters{
		Actor: q.Get("actor"),
		Task:  q.Get("task"),
		Label: q.Get("label"),
		Type:  q.Get("type"),
	}
	if raw := q.Get("since"); raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			f.Since = t
		} else if sec, err := strconv.ParseInt(raw, 10, 64); err == nil {
			f.Since = time.Unix(sec, 0)
		}
	}
	if raw := q.Get("before"); raw != "" {
		if p, err := eventlog.ParsePosition(raw); err == nil {
			f.Before = p
		}
	}
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			f.Limit = n
		}
	}
	f.At, f.AtInvalid = parseAtParam(q)
	return f
}

// parseAtParam returns the time-travel upper bound parsed from the ?at
// query value. Empty / absent → (0, false) ("no upper bound, valid").
// Present-but-unparseable, zero, or negative → (0, true) ("invalid")
// so handlers can 400 rather than silently render a different page.
// Shared by /log and /actors.
func parseAtParam(q url.Values) (at eventlog.Position, invalid bool) {
	raw := q.Get("at")
	if raw == "" {
		return eventlog.Position{}, false
	}
	p, err := eventlog.ParsePosition(raw)
	if err != nil {
		return eventlog.Position{}, true
	}
	return p, false
}

// loadLogEvents fetches events scoped to the task filter (or globally
// if unset), then applies the range window and the
// actor/type/label/since filters in memory, then sorts newest-first
// and pages by Before/Limit. v1 accepts the simplicity tax of loading
// all events from SQL; a real cursor push-down comes when the event
// table grows beyond "fits in RAM."
// hasMore reports whether there are older events beyond what we
// returned, so the template can render the "Load older" affordance.
func loadLogEvents(db *sql.DB, f LogFilters, rg Range) (rows []LogEventRow, total int, hasMore bool, err error) {
	raw, err := job.GetEventsForTaskTree(db, f.Task)
	if err != nil {
		return nil, 0, false, err
	}
	// The window clamp runs first: "showing N of M events" should
	// reflect the slice of history in view — the ?at upper bound and
	// the ?range lower bound both — not the live universe. Done before
	// the per-event filter loop so the total reflects only the window,
	// not actor/type/etc.
	//
	// The lower bound is applied here rather than in SQL because the
	// task-tree scope is resolved in internal/job; one in-memory pass
	// over the same rows keeps the whole window in one place. Paging
	// (Before/Limit, below) then runs inside the window, so "load
	// older" walks back to the cutoff and stops.
	if !f.Live() || rg.Bounded() {
		clamped := raw[:0]
		for _, e := range raw {
			if !f.Live() && e.Position().Compare(f.At) > 0 {
				continue
			}
			if !rg.Includes(e.CreatedAt) {
				continue
			}
			clamped = append(clamped, e)
		}
		raw = clamped
	}
	total = len(raw)

	// Label filter: resolve to a task-ID set once.
	var labelTaskIDs map[int64]bool
	if f.Label != "" {
		ids, err := taskIDsWithLabel(db, f.Label)
		if err != nil {
			return nil, 0, false, err
		}
		labelTaskIDs = make(map[int64]bool, len(ids))
		for _, id := range ids {
			labelTaskIDs[id] = true
		}
	}

	filtered := make([]job.EventEntry, 0, len(raw))
	for _, e := range raw {
		if f.Actor != "" && e.Actor != f.Actor {
			continue
		}
		if f.Type != "" && e.EventType != f.Type {
			continue
		}
		if !f.Since.IsZero() && time.Unix(e.CreatedAt, 0).Before(f.Since) {
			continue
		}
		if labelTaskIDs != nil && !labelTaskIDs[e.TaskID] {
			continue
		}
		if f.Before != (eventlog.Position{}) && e.Position().Compare(f.Before) >= 0 {
			continue
		}
		filtered = append(filtered, e)
	}

	// Reverse-chrono (newest first) for display; the DB returns ascending
	// by log position. Reversing that order is the whole sort — the
	// position is total and, unlike created_at plus a row id, it does not
	// change under a rebuild.
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Position().Compare(filtered[j].Position()) > 0
	})

	// Pagination: cap to limit (defaultLogLimit if unset). hasMore
	// true iff there were strictly more rows after applying filters.
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLogLimit
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
		hasMore = true
	}

	// Titles: one batched lookup for the tasks still in view (after
	// pagination, so we only fetch what we'll render).
	ids := make([]int64, 0, len(filtered))
	seen := make(map[int64]bool, len(filtered))
	for _, e := range filtered {
		if seen[e.TaskID] {
			continue
		}
		seen[e.TaskID] = true
		ids = append(ids, e.TaskID)
	}
	titles, err := job.TaskTitlesByID(db, ids)
	if err != nil {
		return nil, 0, false, err
	}

	now := time.Now()
	rows = make([]LogEventRow, len(filtered))
	for i, e := range filtered {
		rows[i] = buildLogEventRow(e, titles[e.TaskID], now)
	}
	return rows, total, hasMore, nil
}

// systemActor is the name shown as the doer of housekeeping events
// (claim_expired) — the Jobs runtime, not whoever held the claim.
const systemActor = "Jobs"

// buildLogEventRow folds one stored event into the row shape the
// "log-row" template renders. Kept separate from loadLogEvents so the
// parity test can build a row from a synthetic event, and so the shape
// of a row is defined in exactly one place on the server side.
//
// The client mirror is assets/js/log-row.mjs; log_row_parity_test.go
// renders the same event both ways and diffs the markup.
func buildLogEventRow(e job.EventEntry, title string, now time.Time) LogEventRow {
	ts := time.Unix(e.CreatedAt, 0)
	row := LogEventRow{
		EventID:   e.ID,
		Position:  e.Position(),
		ShortID:   e.ShortID,
		Actor:     e.Actor,
		EventType: e.EventType,
		VerbText:  logRowVerb(e.EventType),
		Title:     title,
		RelTime:   render.RelativeTime(now, ts),
		ISOTime:   ts.UTC().Format(time.RFC3339),
		TaskURL:   "/tasks/" + e.ShortID,
		ActorURL:  "/actors/" + url.PathEscape(e.Actor),
	}
	if isSystemEventType(e.EventType) {
		row.Actor = systemActor
		row.ActorURL = ""
		row.IsSystem = true
	}
	row.Metadata = buildLogRowMetadata(e.EventType, e.Detail)
	return row
}

// isSystemEventType reports whether an event's actor is the Jobs
// runtime rather than a human or agent. Only the claim-expiration
// sweep qualifies today.
func isSystemEventType(eventType string) bool {
	return eventType == "claim_expired"
}

// logRowVerb is the human-readable verb for an event type. Verbs
// collapse to a single word for events that carry extra detail; the
// detail itself folds into the metadata cell. Keeps the verb column
// the same width as DONE / CLAIMED. Unmapped types render their raw
// event type, which the CSS uppercases.
func logRowVerb(eventType string) string {
	switch eventType {
	case "claim_expired":
		return "expired"
	case "criteria_added":
		return "criteria"
	case "criterion_state":
		return "criterion"
	case "found_in_set", "found_in_cleared":
		return "found in"
	case "kind_changed":
		return "kind"
	}
	return eventType
}

// buildLogRowMetadata folds the per-event payload into the trailing
// metadata cell on a log row. Each event type pulls one summary value
// from its detail JSON: a text snippet, a task-id pill (blocked /
// unblocked), or nothing (events whose payload is purely structural,
// like created or released). Unknown event types fall through to an
// empty cell — the row still renders normally.
//
// The vocabulary intentionally mirrors what the task/peek History and
// the Criteria section already show, so a row read from the Log says
// the same thing as the same row read from the task page.
func buildLogRowMetadata(eventType, detailJSON string) LogRowMetadata {
	if detailJSON == "" {
		return LogRowMetadata{}
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(detailJSON), &detail); err != nil {
		return LogRowMetadata{}
	}
	switch eventType {
	case "claimed":
		// The CLI string ("30m", "2h", …) is recorded directly on the
		// event, so we surface what the actor chose at claim time.
		if v, ok := detail["duration"].(string); ok {
			return LogRowMetadata{Text: v}
		}
	case "noted":
		if v, ok := detail["text"].(string); ok {
			return LogRowMetadata{Text: v}
		}
	case "done":
		if v, ok := detail["note"].(string); ok && v != "" {
			return LogRowMetadata{Text: v}
		}
	case "canceled":
		if v, ok := detail["reason"].(string); ok && v != "" {
			return LogRowMetadata{Text: v}
		}
	case "labeled":
		if list, ok := detail["names"].([]any); ok {
			names := make([]string, 0, len(list))
			for _, n := range list {
				if s, ok := n.(string); ok && s != "" {
					names = append(names, s)
				}
			}
			if len(names) > 0 {
				return LogRowMetadata{Text: strings.Join(names, ", ")}
			}
		}
	case "criteria_added":
		if list, ok := detail["criteria"].([]any); ok {
			labels := make([]string, 0, len(list))
			for _, c := range list {
				if obj, ok := c.(map[string]any); ok {
					if label, ok := obj["label"].(string); ok && label != "" {
						labels = append(labels, label)
					}
				}
			}
			if len(labels) > 0 {
				return LogRowMetadata{Text: strings.Join(labels, ", ")}
			}
		}
	case "criterion_state":
		label, _ := detail["label"].(string)
		state, _ := detail["state"].(string)
		return LogRowMetadata{Text: label, State: state}
	case "blocked", "unblocked":
		if v, ok := detail["blocker_id"].(string); ok && v != "" {
			return LogRowMetadata{PillID: v}
		}
	case "found_in_set":
		// The source id is the whole payload; a replacement also
		// records the displaced source, which the CLI renders as
		// "(was X)". The pill is the link, so the row shows the
		// current source and the prefix says it replaced one.
		v, _ := detail["source_id"].(string)
		if v == "" {
			return LogRowMetadata{}
		}
		if prev, ok := detail["previous_source_id"].(string); ok && prev != "" {
			return LogRowMetadata{PillID: v, Prefix: "replacing " + prev + ", now"}
		}
		return LogRowMetadata{PillID: v}
	case "found_in_cleared":
		if v, ok := detail["source_id"].(string); ok && v != "" {
			return LogRowMetadata{PillID: v, Prefix: "cleared, was"}
		}
	case "kind_changed":
		// Mirrors `job log`: "kind task-tree → issue-tree".
		from, _ := detail["from"].(string)
		to, _ := detail["to"].(string)
		if from != "" && to != "" {
			return LogRowMetadata{Text: from + "-tree → " + to + "-tree"}
		}
	}
	return LogRowMetadata{}
}

func taskIDsWithLabel(db *sql.DB, label string) ([]int64, error) {
	rows, err := db.Query(`SELECT task_id FROM task_labels WHERE name = ?`, label)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// eventsURL builds /events?… reflecting the same filter state as the
// page, so the SSE live-tail only emits events that match.
func eventsURL(f LogFilters) string {
	q := url.Values{}
	if f.Actor != "" {
		q.Set("actor", f.Actor)
	}
	if f.Task != "" {
		q.Set("task", f.Task)
	}
	if f.Label != "" {
		q.Set("label", f.Label)
	}
	if f.Type != "" {
		q.Set("type", f.Type)
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if len(q) == 0 {
		return "/events"
	}
	return "/events?" + q.Encode()
}
