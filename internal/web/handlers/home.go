package handlers

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"time"

	"github.com/bensyverson/jobs/internal/web/render"
	"github.com/bensyverson/jobs/internal/web/signals"
	"github.com/bensyverson/jobs/internal/web/templates"
)

// HomePageData is the template payload for the landing view. Each
// signal card's data is pre-shaped here so the template stays simple —
// formatting (durations, percentages) happens in Go, not in Go
// templates.
type HomePageData struct {
	templates.Chrome
	Activity          ActivityCard
	NewlyBlocked      NewlyBlockedCard
	LongestClaim      LongestClaimCard
	OldestTodo        OldestTodoCard
	ActiveClaims      ActiveClaimsPanel
	RecentCompletions RecentCompletionsPanel
	Upcoming          UpcomingPanel
	Blocked           BlockedStripPanel
	Graph             render.SubwayView
}

// ActivityCard carries the 60-bucket histogram and the per-type
// legend totals for the activity signal card.
type ActivityCard struct {
	Bars        []ActivityBar
	TotalDone   int
	TotalClaim  int
	TotalCreate int
	TotalBlock  int
	TotalEvents int
}

// ActivityBar is one minute's worth of stacked events. When Empty is
// true the bar renders as a thin placeholder pill and segment fields
// go unused. Otherwise HeightPercent drives the bar's height and the
// four segment flex values drive the stack proportions.
type ActivityBar struct {
	Empty         bool
	HeightPercent int
	Done          int
	Claim         int
	Create        int
	Block         int
}

// NewlyBlockedCard is the "newly blocked in last 10m" alarm card.
type NewlyBlockedCard struct {
	Count       int
	ProgressPct int
	Items       []BlockRefView
}

// BlockRefView is one (blocked → waiting-on) row in the context line.
type BlockRefView struct {
	BlockedShortID   string
	BlockedURL       string
	WaitingOnShortID string
	WaitingOnURL     string
}

// LongestClaimCard is the "longest active claim" alarm card.
type LongestClaimCard struct {
	Present      bool
	Actor        string
	ActorURL     string
	TaskShortID  string
	TaskURL      string
	TaskTitle    string
	DurationText string
	ProgressPct  int
}

// OldestTodoCard is the "oldest todo" alarm card.
type OldestTodoCard struct {
	Present     bool
	TaskShortID string
	TaskURL     string
	Title       string
	AgeText     string
	ProgressPct int
}

// ActiveClaimsPanel is the "Active claims" list on Home: one row per
// currently held claim, oldest first.
type ActiveClaimsPanel struct {
	Count int
	Rows  []ActiveClaimRow
}

// ActiveClaimRow is a single in-flight claim. ClaimedAtUnix drives the
// client-side tick in home-live.js — the server pins an initial
// DurationText, but the JS ticker keeps it moving between SSR refreshes.
type ActiveClaimRow struct {
	Actor         string
	ActorURL      string
	TaskShortID   string
	TaskURL       string
	TaskTitle     string
	DurationText  string
	ClaimedAtUnix int64
}

// RecentCompletionsPanel is the "Recent completions" list on Home.
// Done and canceled both count as completions — both are terminal
// states that take a task off the board. Rows are oldest-first so
// the timeline reads naturally (top = earlier, bottom = just now).
type RecentCompletionsPanel struct {
	Count int
	Rows  []RecentCompletionRow
}

// RecentCompletionRow is a single recent terminal event.
type RecentCompletionRow struct {
	Actor           string
	ActorURL        string
	TaskShortID     string
	TaskURL         string
	TaskTitle       string
	AgeText         string
	CompletedAtUnix int64
}

// RecentCompletionsLimit caps the panel at a reasonable scroll depth.
// Panel is scrollable (max-height: 280px ≈ 8 rows visible); the rest
// paginate via overflow scroll.
const RecentCompletionsLimit = 25

// BlockedStripPanel is the "Blocked" list on Home: tasks with at least
// one active blocker, each row stacked so its "waiting on …" line fits
// one or more blocker pills without overflow.
type BlockedStripPanel struct {
	Count int
	Rows  []BlockedRow
}

// BlockedRow is one blocked task plus the set of still-active blockers
// it's waiting on.
type BlockedRow struct {
	TaskShortID string
	TaskURL     string
	TaskTitle   string
	Blockers    []BlockerLink
}

// BlockerLink is one pill inside a BlockedRow's "waiting on" line.
type BlockerLink struct {
	ShortID string
	URL     string
}

// BlockedStripLimit caps the panel's depth. Dashboards that exceed
// this probably want the Plan view's full blocker tree anyway.
const BlockedStripLimit = 20

// UpcomingPanel is the "Upcoming" list on Home: available, unblocked
// leaves in preorder — the set that mirrors what `job status` would
// surface as claimable if another agent joined.
type UpcomingPanel struct {
	Count int
	Rows  []UpcomingRow
}

// UpcomingRow is one claimable leaf. No actor column — nothing in this
// panel is claimed.
type UpcomingRow struct {
	TaskShortID   string
	TaskURL       string
	TaskTitle     string
	AgeText       string
	CreatedAtUnix int64
}

// UpcomingLimit caps the panel at the same depth as other Home panels.
const UpcomingLimit = 25

// Home renders the landing "Now" view: four signal cards on top,
// other Home-view sections (claims table, recent completions, blocked
// strip, mini-graph) layered in by later Phase 5 tasks.
func Home(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		sig, err := signals.Compute(r.Context(), deps.DB, now)
		if err != nil {
			InternalError(deps, w, "home signals", err)
			return
		}

		claims, err := loadActiveClaims(r.Context(), deps.DB, now)
		if err != nil {
			InternalError(deps, w, "home active claims", err)
			return
		}

		recent, err := loadRecentCompletions(r.Context(), deps.DB, now)
		if err != nil {
			InternalError(deps, w, "home recent completions", err)
			return
		}

		blocked, err := loadBlockedStrip(r.Context(), deps.DB)
		if err != nil {
			InternalError(deps, w, "home blocked strip", err)
			return
		}

		upcoming, err := loadUpcoming(r.Context(), deps.DB, now)
		if err != nil {
			InternalError(deps, w, "home upcoming", err)
			return
		}

		sub, err := signals.BuildSubway(r.Context(), deps.DB, now)
		if err != nil {
			InternalError(deps, w, "home mini-graph", err)
			return
		}

		chrome, err := newChrome(r.Context(), deps, "home", now)
		if err != nil {
			InternalError(deps, w, "home initial frame", err)
			return
		}

		data := HomePageData{
			Chrome:            chrome,
			Activity:          buildActivityCard(sig.Activity),
			NewlyBlocked:      buildNewlyBlockedCard(sig.NewlyBlocked),
			LongestClaim:      buildLongestClaimCard(sig.LongestClaim),
			OldestTodo:        buildOldestTodoCard(sig.OldestTodo),
			ActiveClaims:      claims,
			RecentCompletions: recent,
			Upcoming:          upcoming,
			Blocked:           blocked,
			Graph:             render.LayoutSubway(sub),
		}
		renderPage(deps, w, "home", data)
	})
}

// loadBlockedStrip returns every blocked, still-open, non-deleted task
// along with its still-active blockers. A block edge is "active" when
// the blocker task is neither done nor soft-deleted (canceled tasks
// remove their edges outright in cancel.go, so they don't need to be
// filtered here — but they would be excluded anyway). Groups edges by
// blocked task in a single query; LIMIT is applied to the set of
// distinct blocked tasks, not the edge count.
func loadBlockedStrip(ctx context.Context, db *sql.DB) (BlockedStripPanel, error) {
	var panel BlockedStripPanel
	rows, err := db.QueryContext(ctx, `
		SELECT t.short_id, t.title, t.created_at, t.id,
		       bt.short_id, bt.id, b.created_at
		FROM blocks b
		JOIN tasks t  ON t.id  = b.blocked_id
		JOIN tasks bt ON bt.id = b.blocker_id
		WHERE t.deleted_at IS NULL
		  AND t.status NOT IN ('done', 'canceled')
		  AND bt.deleted_at IS NULL
		  AND bt.status != 'done'
		ORDER BY t.created_at ASC, t.id ASC, b.created_at ASC, bt.id ASC
	`)
	if err != nil {
		return panel, err
	}
	defer rows.Close()

	// Walk the flattened rows and group by blocked_short_id. Insertion
	// order is preserved by indexing via a parallel slice of keys.
	byID := make(map[int64]*BlockedRow)
	var order []int64
	for rows.Next() {
		var shortID, title, blkShort string
		var createdAt, taskID, blkID, edgeCreatedAt int64
		if err := rows.Scan(&shortID, &title, &createdAt, &taskID, &blkShort, &blkID, &edgeCreatedAt); err != nil {
			return panel, err
		}
		row, ok := byID[taskID]
		if !ok {
			if len(order) >= BlockedStripLimit {
				// We've already filled the panel; drop further blocked
				// tasks but keep reading so we don't leave a rows handle
				// open on an error path.
				continue
			}
			row = &BlockedRow{
				TaskShortID: shortID,
				TaskURL:     "/tasks/" + shortID,
				TaskTitle:   title,
			}
			byID[taskID] = row
			order = append(order, taskID)
		}
		row.Blockers = append(row.Blockers, BlockerLink{
			ShortID: blkShort,
			URL:     "/tasks/" + blkShort,
		})
	}
	if err := rows.Err(); err != nil {
		return panel, err
	}

	panel.Rows = make([]BlockedRow, 0, len(order))
	for _, id := range order {
		panel.Rows = append(panel.Rows, *byID[id])
	}
	panel.Count = len(panel.Rows)
	return panel, nil
}

// loadUpcoming returns up to UpcomingLimit available leaves in
// preorder — the claimable frontier mirroring `job status`'s Next:
// pool. A row is included when the task is available (not claimed, not
// done, not canceled), not soft-deleted, has no still-active blockers
// (a blocker counts only if it's neither done nor deleted), and has
// no open children (a child counts as open when its status is not
// done/canceled and it is not soft-deleted). Ordering is the sort-key
// path, joined with '/' — a byte below every character in the sort-key
// alphabet — so siblings read top-to-bottom in declaration order and
// deeper descendants follow their ancestors.
func loadUpcoming(ctx context.Context, db *sql.DB, now time.Time) (UpcomingPanel, error) {
	var panel UpcomingPanel
	rows, err := db.QueryContext(ctx, `
		WITH RECURSIVE subtree(id, sort_path) AS (
			SELECT t.id, t.sort_key
			FROM tasks t
			WHERE t.parent_id IS NULL AND t.deleted_at IS NULL
			UNION ALL
			SELECT t.id, s.sort_path || '/' || t.sort_key
			FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT t.short_id, t.title, t.created_at
		FROM tasks t JOIN subtree s ON s.id = t.id
		WHERE t.status = 'available'
		  AND t.deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM blocks b
		    JOIN tasks bt ON bt.id = b.blocker_id
		    WHERE b.blocked_id = t.id
		      AND bt.status != 'done'
		      AND bt.deleted_at IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM tasks c
		    WHERE c.parent_id = t.id
		      AND c.status NOT IN ('done', 'canceled')
		      AND c.deleted_at IS NULL
		  )
		ORDER BY s.sort_path
		LIMIT ?
	`, UpcomingLimit)
	if err != nil {
		return panel, err
	}
	defer rows.Close()

	for rows.Next() {
		var shortID, title string
		var createdAt int64
		if err := rows.Scan(&shortID, &title, &createdAt); err != nil {
			return panel, err
		}
		panel.Rows = append(panel.Rows, UpcomingRow{
			TaskShortID:   shortID,
			TaskURL:       "/tasks/" + shortID,
			TaskTitle:     title,
			AgeText:       render.RelativeTime(now, time.Unix(createdAt, 0)),
			CreatedAtUnix: createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return panel, err
	}
	panel.Count = len(panel.Rows)
	return panel, nil
}

// loadRecentCompletions returns up to RecentCompletionsLimit of the
// most recent done/canceled events joined to their tasks, sorted
// newest-first for display. Tasks with a deleted_at or a missing
// short_id are excluded via the INNER JOIN.
func loadRecentCompletions(ctx context.Context, db *sql.DB, now time.Time) (RecentCompletionsPanel, error) {
	var panel RecentCompletionsPanel
	rows, err := db.QueryContext(ctx, `
		SELECT e.id, e.actor, e.created_at, t.short_id, t.title
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.event_type IN ('done', 'canceled')
		  AND t.deleted_at IS NULL
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT ?
	`, RecentCompletionsLimit)
	if err != nil {
		return panel, err
	}
	defer rows.Close()

	// Query returns newest-first; we render in the same order so the
	// freshest completion sits at the top of the panel.
	for rows.Next() {
		var id int64
		var actor, shortID, title string
		var completedAt int64
		if err := rows.Scan(&id, &actor, &completedAt, &shortID, &title); err != nil {
			return panel, err
		}
		panel.Rows = append(panel.Rows, RecentCompletionRow{
			Actor:           actor,
			ActorURL:        "/actors/" + actor,
			TaskShortID:     shortID,
			TaskURL:         "/tasks/" + shortID,
			TaskTitle:       title,
			AgeText:         render.RelativeTime(now, time.Unix(completedAt, 0)),
			CompletedAtUnix: completedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return panel, err
	}
	panel.Count = len(panel.Rows)
	return panel, nil
}

// loadActiveClaims lists every currently claimed task with enough
// context to render a row in the Active Claims panel. Rows are sorted
// newest-first so the most recent claim is at the top. Multiple prior
// 'claimed' events for the same task are collapsed via MAX so we
// always measure from the current holder's most recent claim.
func loadActiveClaims(ctx context.Context, db *sql.DB, now time.Time) (ActiveClaimsPanel, error) {
	var panel ActiveClaimsPanel
	rows, err := db.QueryContext(ctx, `
		SELECT t.short_id, t.title, t.claimed_by, MAX(e.created_at) AS claimed_at
		FROM tasks t
		JOIN events e ON e.task_id = t.id
		WHERE t.status = 'claimed'
		  AND t.deleted_at IS NULL
		  AND t.claimed_by IS NOT NULL
		  AND e.event_type = 'claimed'
		  AND e.actor = t.claimed_by
		GROUP BY t.id
		ORDER BY claimed_at DESC, t.id DESC
	`)
	if err != nil {
		return panel, err
	}
	defer rows.Close()

	for rows.Next() {
		var shortID, title, actor string
		var claimedAt int64
		if err := rows.Scan(&shortID, &title, &actor, &claimedAt); err != nil {
			return panel, err
		}
		age := max(now.Unix()-claimedAt, 0)
		panel.Rows = append(panel.Rows, ActiveClaimRow{
			Actor:         actor,
			ActorURL:      "/actors/" + actor,
			TaskShortID:   shortID,
			TaskURL:       "/tasks/" + shortID,
			TaskTitle:     title,
			DurationText:  render.ClaimDuration(time.Duration(age) * time.Second),
			ClaimedAtUnix: claimedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return panel, err
	}
	panel.Count = len(panel.Rows)
	return panel, nil
}

// buildActivityCard scales buckets against the busiest minute so the
// tallest bar peaks at 100% regardless of absolute volume. Minutes
// with no events collapse to the --empty placeholder.
func buildActivityCard(a signals.Activity) ActivityCard {
	out := ActivityCard{
		Bars:        make([]ActivityBar, 0, len(a.Buckets)),
		TotalDone:   a.TotalDone,
		TotalClaim:  a.TotalClaim,
		TotalCreate: a.TotalCreate,
		TotalBlock:  a.TotalBlock,
		TotalEvents: a.TotalEvents(),
	}

	max := 0
	for _, b := range a.Buckets {
		if t := b.Total(); t > max {
			max = t
		}
	}

	for _, b := range a.Buckets {
		total := b.Total()
		if total == 0 || max == 0 {
			out.Bars = append(out.Bars, ActivityBar{Empty: true})
			continue
		}
		pct := int(math.Round(float64(total) / float64(max) * 100))
		if pct < 1 {
			pct = 1
		}
		out.Bars = append(out.Bars, ActivityBar{
			HeightPercent: pct,
			Done:          b.Done,
			Claim:         b.Claim,
			Create:        b.Create,
			Block:         b.Block,
		})
	}
	return out
}

func buildNewlyBlockedCard(nb signals.NewlyBlocked) NewlyBlockedCard {
	items := make([]BlockRefView, 0, len(nb.Items))
	for _, r := range nb.Items {
		items = append(items, BlockRefView{
			BlockedShortID:   r.BlockedShortID,
			BlockedURL:       "/tasks/" + r.BlockedShortID,
			WaitingOnShortID: r.WaitingOnShortID,
			WaitingOnURL:     "/tasks/" + r.WaitingOnShortID,
		})
	}
	return NewlyBlockedCard{
		Count:       nb.Count,
		ProgressPct: pct(nb.Progress),
		Items:       items,
	}
}

func buildLongestClaimCard(lc signals.LongestClaim) LongestClaimCard {
	if !lc.Present {
		return LongestClaimCard{}
	}
	return LongestClaimCard{
		Present:      true,
		Actor:        lc.Actor,
		ActorURL:     "/actors/" + lc.Actor,
		TaskShortID:  lc.TaskShortID,
		TaskURL:      "/tasks/" + lc.TaskShortID,
		TaskTitle:    lc.TaskTitle,
		DurationText: render.ClaimDuration(time.Duration(lc.DurationSeconds) * time.Second),
		ProgressPct:  pct(lc.Progress),
	}
}

func buildOldestTodoCard(ot signals.OldestTodo) OldestTodoCard {
	if !ot.Present {
		return OldestTodoCard{}
	}
	now := time.Time{}
	then := now.Add(-time.Duration(ot.AgeSeconds) * time.Second)
	return OldestTodoCard{
		Present:     true,
		TaskShortID: ot.TaskShortID,
		TaskURL:     "/tasks/" + ot.TaskShortID,
		Title:       ot.Title,
		AgeText:     render.RelativeTime(now, then),
		ProgressPct: pct(ot.Progress),
	}
}

// pct converts a 0..1 progress into a rounded integer percentage
// suitable for a CSS --progress custom property.
func pct(p float64) int {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 100
	}
	return int(math.Round(p * 100))
}
