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
	// Before is an event-id cursor: only events with id < Before are
	// returned. Zero means "no cursor"; pagination starts from the
	// newest event.
	Before int64
	// Limit caps the number of rows returned. <=0 means "use the
	// default" (defaultLogLimit).
	Limit int
	// At is the time-travel upper bound: only events with id <= At are
	// included. Zero means "no upper bound" (live). Set together with
	// AtInvalid so callers can distinguish "absent" from "malformed."
	At        int64
	AtInvalid bool
}

// LogChip is one clickable chip in the log-view filter bar. HRef is a
// fully-formed query string so the template can emit <a href=…>.
type LogChip struct {
	Label  string
	HRef   string
	Active bool
	Actor  string // non-empty for actor chips — paints the avatar dot
	LabelK string // non-empty for label chips — paints the pill tint
}

// LogEventRow is one already-rendered row in the log view. Building
// this once on the server side keeps the template simple and pushes
// id-formatting / time-formatting into Go where it belongs.
type LogEventRow struct {
	// EventID is the event's numeric id. Rendered on the row as
	// data-event-id so the live-tail script can dedup incoming SSE
	// frames against the server-rendered set (prevents double-render
	// when the backfill window overlaps SSR output).
	EventID   int64
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
	Actors      []LogChip
	Labels      []LogChip
	TotalShown  int
	TotalEvents int
	// EventsURL is the SSE subscription URL — /events plus the same
	// filter query params as the page itself, so the live tail only
	// delivers events that match the current filter state.
	EventsURL string
	// HasMore is true when there are older events beyond what was
	// rendered. Drives the "Load older" affordance at the bottom.
	HasMore bool
	// MoreURL is /log?…&before=<oldestEventID>, preserving every
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
				"?at must be a positive integer event id.")
			return
		}

		events, totalEvents, hasMore, err := loadLogEvents(deps.DB, filters)
		if err != nil {
			InternalError(deps, w, "log query", err)
			return
		}

		actors, err := job.DistinctActors(deps.DB)
		if err != nil {
			InternalError(deps, w, "actors query", err)
			return
		}
		labelFreqs, err := job.AllTaskLabelFreqs(deps.DB)
		if err != nil {
			InternalError(deps, w, "label freqs query", err)
			return
		}
		labels := topLabelsByFreq(labelFreqs, filters.Label, 10)

		chrome, err := newChrome(r.Context(), deps, "log", time.Now())
		if err != nil {
			InternalError(deps, w, "log initial frame", err)
			return
		}
		data := LogPageData{
			Chrome:      chrome,
			Filters:     filters,
			Events:      events,
			EventTypes:  buildTypeChips(filters),
			Actors:      buildActorChips(filters, actors),
			Labels:      buildLabelChips(filters, labels),
			TotalShown:  len(events),
			TotalEvents: totalEvents,
			EventsURL:   eventsURL(filters),
			HasMore:     hasMore,
		}
		if hasMore && len(events) > 0 {
			data.MoreURL = moreURL(filters, events[len(events)-1].EventID)
		}
		renderPage(deps, w, "log", data)
	})
}

// moreURL returns /log?…&before=<oldestID>, preserving every other
// filter so paging through to older events keeps the same view.
func moreURL(f LogFilters, oldestID int64) string {
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
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	q.Set("before", strconv.FormatInt(oldestID, 10))
	return "/log?" + q.Encode()
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
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			f.Before = id
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
func parseAtParam(q url.Values) (at int64, invalid bool) {
	raw := q.Get("at")
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, true
	}
	return id, false
}

// loadLogEvents fetches events scoped to the task filter (or globally
// if unset), then applies actor/type/label/since filters in memory,
// then sorts newest-first and pages by Before/Limit. v1 accepts the
// simplicity tax of loading all events from SQL; a real cursor
// push-down comes when the event table grows beyond "fits in RAM."
// hasMore reports whether there are older events beyond what we
// returned, so the template can render the "Load older" affordance.
func loadLogEvents(db *sql.DB, f LogFilters) (rows []LogEventRow, total int, hasMore bool, err error) {
	raw, err := job.GetEventsForTaskTree(db, f.Task)
	if err != nil {
		return nil, 0, false, err
	}
	// In ?at mode, the total counter is also scoped to the at-window —
	// "showing N of M events" should reflect the moment we're pinned to,
	// not the live universe. Done before the per-event filter loop so
	// the total reflects only the upper-bound clamp, not actor/type/etc.
	if f.At > 0 {
		clamped := raw[:0]
		for _, e := range raw {
			if e.ID <= f.At {
				clamped = append(clamped, e)
			}
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
		if f.Before > 0 && e.ID >= f.Before {
			continue
		}
		filtered = append(filtered, e)
	}

	// Reverse-chrono (newest first) for display; DB returns ascending
	// by created_at. Tiebreak on event id descending so events sharing
	// a timestamp (common for batched test fixtures and rapid CLI
	// activity) are still ordered deterministically newest-first.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt != filtered[j].CreatedAt {
			return filtered[i].CreatedAt > filtered[j].CreatedAt
		}
		return filtered[i].ID > filtered[j].ID
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
		ts := time.Unix(e.CreatedAt, 0)
		row := LogEventRow{
			EventID:   e.ID,
			ShortID:   e.ShortID,
			Actor:     e.Actor,
			EventType: e.EventType,
			VerbText:  e.EventType,
			Title:     titles[e.TaskID],
			RelTime:   render.RelativeTime(now, ts),
			ISOTime:   ts.UTC().Format(time.RFC3339),
			TaskURL:   "/tasks/" + e.ShortID,
			ActorURL:  "/actors/" + url.PathEscape(e.Actor),
		}
		if e.EventType == "claim_expired" {
			row.VerbText = "expired"
			row.Actor = "Jobs"
			row.ActorURL = ""
			row.IsSystem = true
		}
		// Verbs collapse to a single word for events that carry extra
		// detail; the detail itself folds into the metadata cell. Keeps
		// the verb column the same width as DONE / CLAIMED.
		switch e.EventType {
		case "criteria_added":
			row.VerbText = "criteria"
		case "criterion_state":
			row.VerbText = "criterion"
		case "found_in_set", "found_in_cleared":
			row.VerbText = "found in"
		case "kind_changed":
			row.VerbText = "kind"
		}
		row.Metadata = buildLogRowMetadata(e.EventType, e.Detail)
		rows[i] = row
	}
	return rows, total, hasMore, nil
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

func buildTypeChips(f LogFilters) []LogChip {
	chips := []LogChip{
		{Label: "all", HRef: logURL(f, "type", ""), Active: f.Type == ""},
	}
	for _, t := range knownEventTypes {
		chips = append(chips, LogChip{
			Label:  typeChipLabel(t),
			HRef:   logURL(f, "type", t),
			Active: f.Type == t,
		})
	}
	return chips
}

// typeChipLabel renders a friendlier label for the event-type filter
// chips. Snake-cased event types collapse to their short verb form so
// the chip strip reads as the same vocabulary as the row verb column
// (CRITERIA / CRITERION rather than the raw enum). The HRef still
// carries the canonical type so the URL contract stays stable.
func typeChipLabel(t string) string {
	switch t {
	case "criteria_added":
		return "criteria"
	case "criterion_state":
		return "criterion"
	case "claim_expired":
		return "expired"
	case "found_in_set":
		return "found in"
	case "found_in_cleared":
		return "found-in cleared"
	case "kind_changed":
		return "kind"
	}
	return t
}

func buildActorChips(f LogFilters, actors []string) []LogChip {
	chips := []LogChip{
		{Label: "any", HRef: logURL(f, "actor", ""), Active: f.Actor == ""},
	}
	for _, a := range actors {
		chips = append(chips, LogChip{
			Label:  a,
			HRef:   logURL(f, "actor", a),
			Active: f.Actor == a,
			Actor:  a,
		})
	}
	return chips
}

// topLabelsByFreq returns the top-n labels by frequency (desc, name
// asc tiebreak), with the active label always included even if it
// would otherwise fall below the cap so the active selection is
// never orphaned. Names only — caller decides chip presentation.
func topLabelsByFreq(freqs map[string]int, active string, n int) []string {
	type entry struct {
		name  string
		count int
	}
	all := make([]entry, 0, len(freqs))
	for name, c := range freqs {
		all = append(all, entry{name, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].name < all[j].name
	})
	out := make([]string, 0, n+1)
	inOut := make(map[string]bool)
	for i := 0; i < len(all) && len(out) < n; i++ {
		out = append(out, all[i].name)
		inOut[all[i].name] = true
	}
	if active != "" && !inOut[active] {
		out = append(out, active)
	}
	return out
}

func buildLabelChips(f LogFilters, labels []string) []LogChip {
	chips := []LogChip{
		{Label: "any", HRef: logURL(f, "label", ""), Active: f.Label == ""},
	}
	for _, l := range labels {
		chips = append(chips, LogChip{
			Label:  l,
			HRef:   logURL(f, "label", l),
			Active: f.Label == l,
			LabelK: l,
		})
	}
	return chips
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

// logURL rebuilds /log?… with one key set (or cleared if value=="")
// while preserving the rest of the filter state. Lets chips toggle
// their own axis without forgetting the others.
func logURL(f LogFilters, setKey, setValue string) string {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("actor", f.Actor)
	set("task", f.Task)
	set("label", f.Label)
	set("type", f.Type)
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339))
	}
	if setValue == "" {
		q.Del(setKey)
	} else {
		q.Set(setKey, setValue)
	}
	if len(q) == 0 {
		return "/log"
	}
	return "/log?" + q.Encode()
}
