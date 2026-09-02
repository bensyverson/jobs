package handlers

import (
	"context"
	"database/sql"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
)

// logChipCap is how many entity chips a strip shows before the rest
// fold behind a "+N more" chip. Two rows' worth at desktop density —
// enough that a busy week reads at a glance, few enough that the
// filter bar never becomes the page.
const logChipCap = 24

// LogChipStrip is one axis of the filter bar: the chips to render (in
// order, the leading "any" chip first) plus the overflow affordance.
// MoreCount is zero when nothing was cut, which is what the template
// tests to decide whether to render the "+N more" chip at all.
type LogChipStrip struct {
	Chips     []LogChip
	MoreCount int
	MoreHRef  string
}

// logChipCtx is everything a chip href has to carry forward: the
// filter state, the current window, and whether the strips are
// expanded. Chips toggle one axis and preserve the rest, so a click
// never silently resets the window or re-collapses an opened strip.
type logChipCtx struct {
	f        LogFilters
	rangeKey RangeKey
	chipsAll bool
}

// url rebuilds /log?… with one key set (or cleared when value is
// empty) while preserving the rest of the view.
func (c logChipCtx) url(setKey, setValue string) string {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("actor", c.f.Actor)
	set("task", c.f.Task)
	set("label", c.f.Label)
	set("type", c.f.Type)
	if !c.f.Since.IsZero() {
		q.Set("since", c.f.Since.UTC().Format(time.RFC3339))
	}
	// The default range is expressed by omitting the parameter, so the
	// canonical /log URL stays clean.
	if c.rangeKey != DefaultRangeKey {
		q.Set("range", string(c.rangeKey))
	}
	if c.chipsAll {
		q.Set("chips", chipsAllValue)
	}
	// The scrubber cursor rides along on every chip href, the same way
	// buildRangeTabs preserves it: a chip toggles one filter axis, it
	// doesn't exit history. Absent (0) means live, so the parameter is
	// omitted rather than written as "at=0".
	if !c.f.Live() {
		q.Set("at", c.f.At.String())
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

// chipsAllValue is the one recognized `?chips=` value: render every
// chip in the window rather than the capped strip. It is what the
// "+N more" chip links to, so the expander works with JS off.
const chipsAllValue = "all"

// buildTypeChips renders the event-type axis. Unlike the actor and
// label strips this is a fixed vocabulary, not a query — it reads the
// same on an empty database as on a busy one — so it is neither
// bounded by the range nor capped. A selected type outside the known
// list still gets a chip, so a hand-written or bookmarked ?type= is
// visible as a filter rather than silently in force.
func buildTypeChips(c logChipCtx) []LogChip {
	chips := []LogChip{
		{Label: "all", HRef: c.url("type", ""), Active: c.f.Type == ""},
	}
	known := false
	for _, t := range knownEventTypes {
		if t == c.f.Type {
			known = true
		}
		chips = append(chips, LogChip{
			Label:  typeChipLabel(t),
			HRef:   c.url("type", t),
			Active: c.f.Type == t,
		})
	}
	if c.f.Type != "" && !known {
		chips = append(chips, LogChip{
			Label:  typeChipLabel(c.f.Type),
			HRef:   c.url("type", c.f.Type),
			Active: true,
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

// buildActorChips renders the actor axis from the in-window actor
// list (already most-recent-first).
func buildActorChips(c logChipCtx, actors []string) LogChipStrip {
	names := withSelected(actors, c.f.Actor)
	entity := make([]LogChip, 0, len(names))
	for _, a := range names {
		entity = append(entity, LogChip{
			Label:  a,
			HRef:   c.url("actor", a),
			Active: c.f.Actor == a,
			Actor:  a,
		})
	}
	return c.strip(LogChip{
		Label:  "any",
		HRef:   c.url("actor", ""),
		Active: c.f.Actor == "",
	}, entity)
}

// buildLabelChips renders the label axis from the in-window label
// list (already most-recent-first).
func buildLabelChips(c logChipCtx, labels []string) LogChipStrip {
	names := withSelected(labels, c.f.Label)
	entity := make([]LogChip, 0, len(names))
	for _, l := range names {
		entity = append(entity, LogChip{
			Label:  l,
			HRef:   c.url("label", l),
			Active: c.f.Label == l,
			LabelK: l,
		})
	}
	return c.strip(LogChip{
		Label:  "any",
		HRef:   c.url("label", ""),
		Active: c.f.Label == "",
	}, entity)
}

// withSelected puts the current selection at the head of the list
// when the window or the query dropped it. A filter in force that has
// no chip is a filter the reader cannot see or clear.
func withSelected(names []string, selected string) []string {
	if selected == "" {
		return names
	}
	if slices.Contains(names, selected) {
		return names
	}
	return append([]string{selected}, names...)
}

// strip assembles one axis: the leading "any" chip, the entity chips,
// and the overflow bookkeeping. Everything past logChipCap is marked
// Overflow — rendered but hidden, so the JS expander is a class
// toggle rather than a fetch — except the active chip, which stays
// visible wherever it ranks. `?chips=all` renders the whole strip
// visible and drops the "+N more" chip; that is the no-JS path.
func (c logChipCtx) strip(lead LogChip, entity []LogChip) LogChipStrip {
	s := LogChipStrip{Chips: make([]LogChip, 0, len(entity)+1)}
	s.Chips = append(s.Chips, lead)
	if !c.chipsAll {
		kept := 0
		for i := range entity {
			if kept < logChipCap {
				kept++
				continue
			}
			if entity[i].Active {
				continue
			}
			entity[i].Overflow = true
			s.MoreCount++
		}
	}
	s.Chips = append(s.Chips, entity...)
	if s.MoreCount > 0 {
		s.MoreHRef = c.url("chips", chipsAllValue)
	}
	return s
}

// actorsInRange returns the actors with at least one event inside the
// window, most recent first. This is the
// chip strip's whole vocabulary: an agent who last ran in March is
// not a filter anyone reaches for this week.
//
// at is the time-travel upper bound (0 when live), so under ?at= the
// strip reflects who was active as of that moment.
func actorsInRange(ctx context.Context, db *sql.DB, at eventlog.Position, rg Range) ([]string, error) {
	query := `
		SELECT e.actor, MAX(e.created_at) AS last_at, MAX(e.ts) AS last_ts
		FROM events e
		WHERE e.actor <> ''`
	args := []any{}
	if at != (eventlog.Position{}) {
		query += " AND " + job.EventPositionExpr("e") + " <= (?, ?, ?)"
		args = append(args, job.EventPositionArgs(at)...)
	}
	if rg.Bounded() {
		query += " AND e.created_at >= ?"
		args = append(args, rg.Cutoff)
	}
	query += `
		GROUP BY e.actor
		ORDER BY last_at DESC, last_ts DESC, e.actor ASC`
	return queryNames(ctx, db, query, args...)
}

// labelsInRange returns the labels carried by tasks with at least one
// event inside the window, ordered by that most recent event. Same
// argument as actorsInRange: the strip should offer the filters this
// window can actually produce rows for.
func labelsInRange(ctx context.Context, db *sql.DB, at eventlog.Position, rg Range) ([]string, error) {
	query := `
		SELECT tl.name, MAX(e.created_at) AS last_at, MAX(e.ts) AS last_ts
		FROM task_labels tl
		JOIN tasks t ON t.id = tl.task_id
		JOIN events e ON e.task_id = tl.task_id
		WHERE t.deleted_at IS NULL`
	args := []any{}
	if at != (eventlog.Position{}) {
		query += " AND " + job.EventPositionExpr("e") + " <= (?, ?, ?)"
		args = append(args, job.EventPositionArgs(at)...)
	}
	if rg.Bounded() {
		query += " AND e.created_at >= ?"
		args = append(args, rg.Cutoff)
	}
	query += `
		GROUP BY tl.name
		ORDER BY last_at DESC, last_ts DESC, tl.name ASC`
	return queryNames(ctx, db, query, args...)
}

// queryNames runs a (name, last_at, last_ts) query and returns the names in
// the order the query produced them. created_at has one-second granularity,
// so the event's millisecond log clock breaks the ties a busy second
// produces. The row id would have done as well until a rebuild renumbered
// it; ts does not move.
func queryNames(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		var lastAt, lastTS int64
		if err := rows.Scan(&name, &lastAt, &lastTS); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// logEmptyText words the log's empty state for the current window, so
// "nothing here" reads as "nothing lately" when the range could be
// the reason. With a filter in force the filters are the likelier
// cause, and the wording says so.
func logEmptyText(f LogFilters, rg Range) string {
	if f.Actor != "" || f.Task != "" || f.Label != "" || f.Type != "" || !f.Since.IsZero() {
		return "No events match the current filters."
	}
	if rg.Key == RangeAll {
		return "No events recorded in this store yet."
	}
	return "No events in the last " + rangeSpanWords(rg.Key) + "."
}

// moreURL returns /log?…&before=<oldest position>, preserving every other
// filter — the window included — so paging through to older events
// keeps the same view rather than escaping the range.
func moreURL(c logChipCtx, oldest eventlog.Position) string {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("actor", c.f.Actor)
	set("task", c.f.Task)
	set("label", c.f.Label)
	set("type", c.f.Type)
	if !c.f.Since.IsZero() {
		q.Set("since", c.f.Since.UTC().Format(time.RFC3339))
	}
	if c.f.Limit > 0 {
		q.Set("limit", strconv.Itoa(c.f.Limit))
	}
	if c.rangeKey != DefaultRangeKey {
		q.Set("range", string(c.rangeKey))
	}
	if c.chipsAll {
		q.Set("chips", chipsAllValue)
	}
	if !c.f.Live() {
		q.Set("at", c.f.At.String())
	}
	q.Set("before", oldest.String())
	return "/log?" + q.Encode()
}
