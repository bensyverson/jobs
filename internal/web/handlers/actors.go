package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// ActorsPageData is the template payload for the per-actor board view.
type ActorsPageData struct {
	templates.Chrome
	Columns []ActorColumn
	// RangeTabs is the 7D / 14D / 30D / All link group in the view
	// header; RangeEmptyText is the empty state worded for the
	// current window ("No actor activity in the last 7 days").
	RangeTabs      []RangeTab
	RangeEmptyText string
}

// ActorColumn is one column on the actors board: an actor plus the
// (actor, task) cards rolled up from their event history. Cards are
// pre-sorted in DOM order — claims first, then history newest-first
// — so the template can range over them with no further logic.
type ActorColumn struct {
	Name       string
	URL        string
	Idle       bool
	ClaimCount int
	StatusText string
	Cards      []ActorCard
}

// ActorCard is one (actor, task) card. The latest state-changing
// event by this actor on this task sets the verb and tint. Notes by
// the same actor on the same task are folded into NoteCount and
// rendered as a badge instead of getting cards of their own.
type ActorCard struct {
	StateClass  string // matches c-actor-card--<state>
	Verb        string // verb word for the meta line
	VerbClass   string // matches c-log-row__verb--<verb>
	AgeText     string
	NoteCount   int
	NoteText    string // "1 note" / "N notes" — empty when NoteCount==0
	TaskShortID string
	TaskURL     string
	TaskTitle   string
	TaskDesc    string
	IsClaim     bool
	EventAt     int64 // unix timestamp; rendered for live-update reconciliation
	// CardKey identifies a card in the DOM as "{actor}:{shortID}" so
	// the live script can find/update it without rebuilding by hand.
	CardKey string
}

// ActorColumnCardLimit caps the number of cards rendered per column
// so a long-lived actor doesn't drag the DOM into the thousands.
// Active claims are always retained — only history is truncated when
// the column overflows. Mirrors the spirit of the log view's 200-row
// cap, halved because each column carries its own.
const ActorColumnCardLimit = 100

// stateChangingTypes are the event types that set the card's verb /
// tint. Anything else (like noted) folds in as metadata.
var stateChangingTypes = map[string]bool{
	"created":   true,
	"claimed":   true,
	"done":      true,
	"blocked":   true,
	"unblocked": true,
	"released":  true,
	"canceled":  true,
}

// Actors renders the column-per-actor board view at /actors.
//
// Accepts ?at=<event_id> as a time-travel upper bound: the event walk
// is scoped to events with id <= at, and IsClaim/ClaimCount are
// derived from the walk itself rather than the live tasks.claimed_by
// column so the column reflects claim state as of that moment.
//
// /actors/{name} stays live-only — the hero stats and 24h timeline
// rely on wall-clock now() in ways we deliberately do not thread a
// synthetic "now" through.
//
// Accepts ?range=7d|14d|30d|all (default 7d) as a lower bound: only
// actors with an event inside the window get a column, and a column
// carries only the cards its in-window events produced. Under ?at=
// the window is measured back from the cursor's event, not from now.
func Actors(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at, invalid := parseAtParam(r.URL.Query())
		if invalid {
			RenderError(deps, w, http.StatusBadRequest,
				"Bad request",
				"?at must be a log position, as the scrubber writes it (<ts>-<replica>-<seq>).")
			return
		}
		now := time.Now()
		anchor, err := rangeAnchor(r.Context(), deps.DB, at, now)
		if err != nil {
			InternalError(deps, w, "actors range anchor", err)
			return
		}
		rg := parseRange(r.URL.Query(), anchor)
		cols, err := loadActorColumns(r.Context(), deps.DB, now, at, rg)
		if err != nil {
			InternalError(deps, w, "actors columns", err)
			return
		}
		chrome, err := newChrome(r.Context(), deps, "actors", now)
		if err != nil {
			InternalError(deps, w, "actors initial frame", err)
			return
		}
		data := ActorsPageData{
			Chrome:         chrome,
			Columns:        cols,
			RangeTabs:      buildRangeTabs("/actors", r.URL.Query(), rg.Key),
			RangeEmptyText: actorsEmptyText(rg),
		}
		renderPage(deps, w, "actors", data)
	})
}

// loadActorColumns walks every event and rolls them up into per-actor
// columns of (actor, task) cards. One pass over the event stream:
//   - For each (actor, task) pair, keep the latest state-changing
//     event (its type sets the card's verb/tint).
//   - Count `noted` events separately as a notes badge.
//   - Track each actor's most-recent event time for column ordering
//     and "last seen" status text.
//   - Track per-task current claimer (set on `claimed`, cleared on
//     `released` / `done` / `canceled` / `claim_expired` / `reopened`)
//     so IsClaim is correct at any upper bound, not just live.
//
// A card is marked IsClaim when the task's effective claimer at the
// end of the walk matches the actor — those cards dock to the visual
// bottom of the column (DOM-first, since CSS uses column-reverse).
//
// atUpperBound is the time-travel anchor: when non-zero, the walk is
// restricted to events at or before that log position. The zero Position
// means "live."
//
// rg is the lower bound. Its cutoff is applied in SQL, so the whole
// rollup — cards, claim state, last-seen, which actors exist at all —
// is derived from in-window events only. That keeps the board
// internally consistent (a column never claims a claim it cannot
// show) at the cost of hiding a claim older than the window; claims
// expire in minutes, so in practice nothing survives that long.
func loadActorColumns(ctx context.Context, db *sql.DB, now time.Time, atUpperBound eventlog.Position, rg Range) ([]ActorColumn, error) {
	query := `
		SELECT e.id, e.actor, e.event_type, e.created_at,
		       t.id, t.short_id, t.title, t.description
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.actor <> ''
		  AND t.deleted_at IS NULL`
	args := []any{}
	if atUpperBound != (eventlog.Position{}) {
		query += " AND " + job.EventPositionExpr("e") + " <= (?, ?, ?)"
		args = append(args, job.EventPositionArgs(atUpperBound)...)
	}
	if rg.Bounded() {
		query += " AND e.created_at >= ?"
		args = append(args, rg.Cutoff)
	}
	query += `
		ORDER BY e.ts ASC, e.rep ASC, CASE WHEN e.rep = '' THEN e.id ELSE e.seq END ASC`
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type pairKey struct {
		actor  string
		taskID int64
	}
	type pairAccum struct {
		card     ActorCard
		hasState bool
	}
	type actorAccum struct {
		lastSeen   int64
		claimCount int
	}

	pairs := make(map[pairKey]*pairAccum)
	actors := make(map[string]*actorAccum)
	pairOrder := make(map[string][]pairKey)
	seenPair := make(map[pairKey]bool)
	// Per-task current claimer, derived from the event walk so claim
	// state is correct at any moment in history. Empty string means
	// "no current claim." On a `claimed` event the actor becomes the
	// claimer; release/done/canceled/claim_expired/reopened clear it.
	currentClaimer := make(map[int64]string)

	for rows.Next() {
		var eventID, createdAt, taskID int64
		var actor, eventType, shortID, title, desc string
		if err := rows.Scan(&eventID, &actor, &eventType, &createdAt, &taskID, &shortID, &title, &desc); err != nil {
			return nil, err
		}
		key := pairKey{actor: actor, taskID: taskID}
		p, ok := pairs[key]
		if !ok {
			p = &pairAccum{
				card: ActorCard{
					TaskShortID: shortID,
					TaskURL:     "/tasks/" + shortID,
					TaskTitle:   title,
					TaskDesc:    desc,
				},
			}
			pairs[key] = p
			if !seenPair[key] {
				seenPair[key] = true
				pairOrder[actor] = append(pairOrder[actor], key)
			}
		}

		switch {
		case eventType == "noted":
			p.card.NoteCount++
		case stateChangingTypes[eventType]:
			p.card.StateClass = "c-actor-card--" + eventType
			p.card.Verb = eventType
			p.card.VerbClass = "c-log-row__verb--" + eventType
			p.card.EventAt = createdAt
			p.hasState = true
		}

		// Maintain per-task claim state from the event stream.
		switch eventType {
		case "claimed":
			currentClaimer[taskID] = actor
		case "released", "done", "canceled", "claim_expired", "reopened":
			delete(currentClaimer, taskID)
		}

		a, ok := actors[actor]
		if !ok {
			a = &actorAccum{}
			actors[actor] = a
		}
		if createdAt > a.lastSeen {
			a.lastSeen = createdAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	cols := make([]ActorColumn, 0, len(actors))
	for actorName, a := range actors {
		var claimCards, historyCards []ActorCard
		for _, key := range pairOrder[actorName] {
			p := pairs[key]
			if !p.hasState {
				continue
			}
			card := p.card
			if currentClaimer[key.taskID] == actorName {
				card.IsClaim = true
				a.claimCount++
			}
			card.AgeText = render.RelativeTime(now, time.Unix(card.EventAt, 0))
			card.NoteText = noteCountLabel(card.NoteCount)
			card.CardKey = actorName + ":" + card.TaskShortID
			if card.IsClaim {
				claimCards = append(claimCards, card)
			} else {
				historyCards = append(historyCards, card)
			}
		}
		// Newest-first within each band. CSS column-reverse flips the
		// stream visually, so DOM-first claims dock at the bottom.
		sort.SliceStable(claimCards, func(i, j int) bool {
			return claimCards[i].EventAt > claimCards[j].EventAt
		})
		sort.SliceStable(historyCards, func(i, j int) bool {
			return historyCards[i].EventAt > historyCards[j].EventAt
		})

		// Apply the per-column cap. Claims always survive; history
		// fills the remaining budget. If claims alone exceed the cap
		// (extremely unlikely — an agent with 100+ open claims), keep
		// them all rather than truncate live state.
		if budget := ActorColumnCardLimit - len(claimCards); budget < len(historyCards) {
			if budget < 0 {
				budget = 0
			}
			historyCards = historyCards[:budget]
		}

		col := ActorColumn{
			Name:       actorName,
			URL:        "/actors/" + url.PathEscape(actorName),
			ClaimCount: a.claimCount,
			Idle:       a.claimCount == 0,
			Cards:      append(claimCards, historyCards...),
		}
		col.StatusText = actorStatusText(col.ClaimCount, render.RelativeTime(now, time.Unix(a.lastSeen, 0)))
		cols = append(cols, col)
	}

	sort.SliceStable(cols, func(i, j int) bool {
		li := actors[cols[i].Name].lastSeen
		lj := actors[cols[j].Name].lastSeen
		if li != lj {
			return li > lj
		}
		return cols[i].Name < cols[j].Name
	})
	return cols, nil
}

// actorsEmptyText words the board's empty state for the current
// window, so "nothing here" reads as "nothing lately" rather than
// "nothing ever" whenever the range could be the reason.
func actorsEmptyText(rg Range) string {
	switch rg.Key {
	case RangeAll:
		return "No actors have touched this store yet."
	default:
		return "No actor activity in the last " + rangeSpanWords(rg.Key) + "."
	}
}

// rangeSpanWords renders a bounded range key as prose.
func rangeSpanWords(key RangeKey) string {
	switch key {
	case Range14D:
		return "14 days"
	case Range30D:
		return "30 days"
	default:
		return "7 days"
	}
}

func noteCountLabel(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "1 note"
	default:
		return strconv.Itoa(n) + " notes"
	}
}

func actorStatusText(claimCount int, lastSeen string) string {
	if claimCount == 0 {
		return "idle · last seen " + lastSeen
	}
	if claimCount == 1 {
		return "1 claim · last seen " + lastSeen
	}
	return strconv.Itoa(claimCount) + " claims · last seen " + lastSeen
}
