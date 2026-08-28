package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// ActorSinglePageData is the template payload for /actors/{name}.
type ActorSinglePageData struct {
	templates.Chrome
	Name       string
	Initial    string
	StatusText string
	Stats      ActorHeroStats
	Timeline   ActorTimeline
	Events     []LogEventRow
	BackURL    string
	// LogURL is the "View all in Log" target — /log scoped to this
	// actor. Rendered next to the Events heading regardless of cap;
	// the Log view is the place for deep history with full filters.
	LogURL string
}

// ActorHeroStats are the four tile values in the hero band.
type ActorHeroStats struct {
	InFlight int
	Done1h   int
	Done24h  int
	Blocked  int
}

// ActorTimeline is the activity strip — five lanes (created / claimed
// / done / blocked / noted), each with marks positioned along the axis
// as a percent from the start of the window (0%) to "now" (100%).
type ActorTimeline struct {
	TotalEvents int
	Lanes       []ActorTimelineLane
	// Window is the span the strip covers, chosen by ?window=.
	Window ActorWindow
	// Options are the segmented-control links (24H / 7D / 30D), built
	// per actor so each one navigates to this actor's page.
	Options []ActorWindowOption
}

// ActorWindow is one selectable span of the single-actor timeline.
// Everything that scales with the span is carried here rather than
// derived at the template: the cutoff, the prose the heading and the
// empty state read, and the axis ticks.
type ActorWindow struct {
	// Key is the ?window= value ("24h", "7d", "30d").
	Key string
	// Label is the segmented-control label ("24H").
	Label string
	// Heading is the prose span, used by the timeline heading
	// ("Timeline · 24 hours") and the empty state.
	Heading  string
	Duration time.Duration
	// Ticks are the axis labels. Each window names its own so every
	// label sits at the percent it actually means — a shared
	// quarter-of-the-window rule would print "5d" over 5.25 days.
	Ticks []ActorAxisTick
}

// ActorAxisTick is one axis label at a fixed percent along the strip.
type ActorAxisTick struct {
	XPercent string
	Text     string
}

// ActorWindowOption is one link in the timeline's segmented control.
type ActorWindowOption struct {
	Label  string
	URL    string
	Active bool
}

// actorWindows are the offered spans, in control order. The first is
// the default: the page answers "what is this agent doing today", so a
// wider span is a deliberate choice, not the resting state. Distinct
// from the Actors board's ?range=, which bounds which actors get a
// column and defaults to 7 days.
var actorWindows = []ActorWindow{
	{
		Key: "24h", Label: "24H", Heading: "24 hours", Duration: 24 * time.Hour,
		Ticks: []ActorAxisTick{
			{"0.0", "24h"}, {"25.0", "18h"}, {"50.0", "12h"}, {"75.0", "6h"}, {"100.0", "now"},
		},
	},
	{
		Key: "7d", Label: "7D", Heading: "7 days", Duration: 7 * 24 * time.Hour,
		Ticks: []ActorAxisTick{
			{"0.0", "7d"}, {"28.6", "5d"}, {"57.1", "3d"}, {"85.7", "1d"}, {"100.0", "now"},
		},
	},
	{
		Key: "30d", Label: "30D", Heading: "30 days", Duration: 30 * 24 * time.Hour,
		Ticks: []ActorAxisTick{
			{"0.0", "30d"}, {"33.3", "20d"}, {"66.7", "10d"}, {"100.0", "now"},
		},
	},
}

// parseActorWindow resolves the ?window= value. Anything unrecognised —
// absent, empty, misspelled, or hand-edited — falls back to the
// default rather than erroring: a bad query param should not cost the
// reader their page.
func parseActorWindow(raw string) ActorWindow {
	for _, w := range actorWindows {
		if w.Key == raw {
			return w
		}
	}
	return actorWindows[0]
}

// actorWindowOptions builds the segmented control for one actor. The
// selected window is always named in the URL, so every option is a
// plain link to a self-describing address.
func actorWindowOptions(name string, active ActorWindow) []ActorWindowOption {
	base := "/actors/" + url.PathEscape(name)
	out := make([]ActorWindowOption, 0, len(actorWindows))
	for _, w := range actorWindows {
		out = append(out, ActorWindowOption{
			Label:  w.Label,
			URL:    base + "?window=" + w.Key,
			Active: w.Key == active.Key,
		})
	}
	return out
}

// ActorTimelineLane is one verb's row of marks. LaneClass is the
// per-verb modifier on the mark element (c-actor-timeline__mark--…).
type ActorTimelineLane struct {
	Verb      string
	LaneClass string
	Marks     []ActorTimelineMark
}

// ActorTimelineMark is one event positioned along the timeline axis.
type ActorTimelineMark struct {
	XPercent string // formatted "%.1f" — empty string is invalid
}

// ActorEventListLimit caps the per-actor event list to keep DOM
// bounded. The Log view paginates at 200 across all actors; the
// per-actor page is a hub, not a deep history scroll — past the cap
// the user follows the "View all in Log" link to /log?actor={name}.
const ActorEventListLimit = 100

// timelineVerbs is the canonical lane order on the timeline.
// The Log filter bar uses a different order (full event vocabulary);
// this is the subset that fits on a 5-lane chart.
var timelineVerbs = []string{"created", "claimed", "done", "blocked", "noted"}

// ActorSingle renders the per-actor hero + timeline + event list at
// /actors/{name}. Unknown actors get a 404. Live updates and the
// timeline scrubber arrive in later phase tasks.
func ActorSingle(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" {
			RenderError(deps, w, http.StatusNotFound, "Actor not found",
				"That actor isn't on the board.")
			return
		}

		now := time.Now()
		exists, err := actorExists(r.Context(), deps.DB, name)
		if err != nil {
			InternalError(deps, w, "actor exists", err)
			return
		}
		if !exists {
			RenderError(deps, w, http.StatusNotFound, "Actor not found",
				fmt.Sprintf("No events recorded for %q yet.", name))
			return
		}

		stats, lastSeen, err := loadActorStats(r.Context(), deps.DB, name, now)
		if err != nil {
			InternalError(deps, w, "actor stats", err)
			return
		}
		window := parseActorWindow(r.URL.Query().Get("window"))
		timeline, err := loadActorTimeline(r.Context(), deps.DB, name, now, window)
		if err != nil {
			InternalError(deps, w, "actor timeline", err)
			return
		}
		events, err := loadActorEvents(r.Context(), deps.DB, name, now)
		if err != nil {
			InternalError(deps, w, "actor events", err)
			return
		}

		chrome, err := newChrome(r.Context(), deps, "actors", now)
		if err != nil {
			InternalError(deps, w, "actor single initial frame", err)
			return
		}
		q := url.Values{}
		q.Set("actor", name)
		data := ActorSinglePageData{
			Chrome:     chrome,
			Name:       name,
			Initial:    render.InitialOf(name),
			Stats:      stats,
			Timeline:   timeline,
			Events:     events,
			BackURL:    "/actors",
			LogURL:     "/log?" + q.Encode(),
			StatusText: actorSingleStatusText(stats.InFlight, lastSeen, now),
		}
		renderPage(deps, w, "actor_single", data)
	})
}

func actorExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var present int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM events WHERE actor = ? LIMIT 1`, name,
	).Scan(&present)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// loadActorStats computes the four hero tiles plus the actor's
// most-recent event time (used for the status line).
func loadActorStats(ctx context.Context, db *sql.DB, name string, now time.Time) (ActorHeroStats, int64, error) {
	var stats ActorHeroStats
	var lastSeen int64

	if err := db.QueryRowContext(ctx, `
		SELECT MAX(created_at) FROM events WHERE actor = ?
	`, name).Scan(&lastSeen); err != nil {
		return stats, 0, err
	}

	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tasks
		WHERE claimed_by = ? AND status = 'claimed' AND deleted_at IS NULL
	`, name).Scan(&stats.InFlight); err != nil {
		return stats, 0, err
	}

	cutoff1h := now.Add(-1 * time.Hour).Unix()
	// The hero's Done 24h tile answers "what has this agent finished
	// today" and is deliberately independent of the timeline's
	// ?window= — widening the strip must not move the counter.
	cutoff24h := now.Add(-24 * time.Hour).Unix()
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE actor = ? AND event_type = 'done' AND created_at >= ?
	`, name, cutoff1h).Scan(&stats.Done1h); err != nil {
		return stats, 0, err
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE actor = ? AND event_type = 'done' AND created_at >= ?
	`, name, cutoff24h).Scan(&stats.Done24h); err != nil {
		return stats, 0, err
	}

	// "Blocked" tile: tasks claimed by this actor that have at least
	// one still-active blocker. Surfaces "what is this actor stuck
	// on" rather than how many `blocked` events they emitted.
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT t.id) FROM tasks t
		JOIN blocks b ON b.blocked_id = t.id
		JOIN tasks bt ON bt.id = b.blocker_id
		WHERE t.claimed_by = ?
		  AND t.status = 'claimed'
		  AND t.deleted_at IS NULL
		  AND bt.status != 'done'
		  AND bt.deleted_at IS NULL
	`, name).Scan(&stats.Blocked); err != nil {
		return stats, 0, err
	}
	return stats, lastSeen, nil
}

// loadActorTimeline buckets every event by this actor over the chosen
// window into the five canonical lanes. Each event becomes a single
// mark whose --x is its position from the start of the window (0%) to
// "now" (100%).
func loadActorTimeline(ctx context.Context, db *sql.DB, name string, now time.Time, window ActorWindow) (ActorTimeline, error) {
	cutoff := now.Add(-window.Duration).Unix()
	windowSecs := window.Duration.Seconds()

	rows, err := db.QueryContext(ctx, `
		SELECT event_type, created_at FROM events
		WHERE actor = ? AND created_at >= ?
		ORDER BY created_at ASC, id ASC
	`, name, cutoff)
	if err != nil {
		return ActorTimeline{}, err
	}
	defer rows.Close()

	byVerb := make(map[string][]ActorTimelineMark, len(timelineVerbs))
	total := 0
	for rows.Next() {
		var verb string
		var at int64
		if err := rows.Scan(&verb, &at); err != nil {
			return ActorTimeline{}, err
		}
		total++
		// Only the lanes the timeline actually renders pick up marks.
		// Other event types (released, canceled, claim_expired) still
		// count toward TotalEvents.
		if !isTimelineVerb(verb) {
			continue
		}
		offset := float64(at-cutoff) / windowSecs * 100.0
		if offset < 0 {
			offset = 0
		} else if offset > 100 {
			offset = 100
		}
		byVerb[verb] = append(byVerb[verb], ActorTimelineMark{
			XPercent: fmt.Sprintf("%.1f", offset),
		})
	}
	if err := rows.Err(); err != nil {
		return ActorTimeline{}, err
	}

	lanes := make([]ActorTimelineLane, 0, len(timelineVerbs))
	for _, v := range timelineVerbs {
		lanes = append(lanes, ActorTimelineLane{
			Verb:      v,
			LaneClass: "c-actor-timeline__mark--" + v,
			Marks:     byVerb[v],
		})
	}
	return ActorTimeline{
		TotalEvents: total,
		Lanes:       lanes,
		Window:      window,
		Options:     actorWindowOptions(name, window),
	}, nil
}

// WindowSecs is the strip's span in whole seconds, published to the
// DOM so the live module can place a new mark at the same scale the
// server used without re-deriving the window from the URL.
func (t ActorTimeline) WindowSecs() int64 {
	return int64(t.Window.Duration / time.Second)
}

func isTimelineVerb(v string) bool {
	return slices.Contains(timelineVerbs, v)
}

// loadActorEvents renders this actor's most recent events through the
// shared log row, so a row read here says exactly what the same row
// says on /log. Capped at ActorEventListLimit — pagination follows in
// a later phase task if needed.
func loadActorEvents(ctx context.Context, db *sql.DB, name string, now time.Time) ([]LogEventRow, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.task_id, e.event_type, e.created_at, e.detail, t.short_id, t.title
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.actor = ? AND t.deleted_at IS NULL
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT ?
	`, name, ActorEventListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LogEventRow
	for rows.Next() {
		var e job.EventEntry
		var title string
		if err := rows.Scan(&e.ID, &e.TaskID, &e.EventType, &e.CreatedAt, &e.Detail, &e.ShortID, &title); err != nil {
			return nil, err
		}
		e.Actor = name
		out = append(out, buildLogEventRow(e, title, now))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Defensive sort: query is already ordered, but keep deterministic
	// behavior in case the DB driver re-orders.
	sort.SliceStable(out, func(i, j int) bool { return out[i].EventID > out[j].EventID })
	return out, nil
}

func actorSingleStatusText(inFlight int, lastSeen int64, now time.Time) string {
	last := render.RelativeTime(now, time.Unix(lastSeen, 0))
	switch {
	case inFlight == 0:
		return "idle · last seen " + last
	case inFlight == 1:
		return "1 claim · last seen " + last
	default:
		return fmt.Sprintf("%d claims · last seen %s", inFlight, last)
	}
}
