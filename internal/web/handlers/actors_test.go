package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// countOuterCards counts <article> openings of c-actor-card. The
// outer article carries `c-actor-card ` with a trailing space (state
// modifier follows); BEM child classes like c-actor-card__meta lack
// that trailing space, so this filters them out.
func countOuterCards(body string) int {
	return strings.Count(body, `<article class="c-actor-card `)
}

func fetchActors(t *testing.T, deps handlers.Deps) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/actors", nil)
	w := httptest.NewRecorder()
	handlers.Actors(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /actors: status %d, body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestActors_RendersOneColumnPerActor(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustAdd(t, db, "bob", "bob-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	cols := strings.Count(body, `<section class="c-actor-col`)
	if cols != 2 {
		t.Errorf("c-actor-col count: got %d, want 2\n---\n%s", cols, body)
	}
}

func TestActors_HeaderTabActorsActive(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `href="/actors" class="c-tab c-tab--active"`)
}

func TestActors_ColumnHeaderHasAvatarLgAndNameLink(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `class="c-avatar c-avatar-lg"`)
	mustContain(t, body, `data-actor="alice"`)
	mustContain(t, body, `href="/actors/alice"`)
}

func TestActors_CollapsesActorTaskPairToLatestStateCard(t *testing.T) {
	db := setupLogTestDB(t)
	// alice creates → claims → releases. The card's verb tint should
	// reflect the latest state-changing event (released), not the
	// earlier created or claimed.
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunRelease(db, id, "", "alice"); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// One card for the (alice, alice-task) pair.
	cards := countOuterCards(body)
	if cards != 1 {
		t.Errorf("c-actor-card count: got %d, want 1", cards)
	}
	mustContain(t, body, `c-log-row__verb--released`)
	if strings.Contains(body, `c-log-row__verb--claimed`) {
		t.Errorf("collapsed card should not show the prior claimed verb")
	}
	if strings.Contains(body, `c-log-row__verb--created`) {
		t.Errorf("collapsed card should not show the prior created verb")
	}
}

func TestActors_NoteEventsCollapseToBadge(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	if err := job.RunNote(db, id, "first note", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	if err := job.RunNote(db, id, "second note", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// One card (created), with a "2 notes" badge — not three cards.
	cards := countOuterCards(body)
	if cards != 1 {
		t.Errorf("c-actor-card count: got %d, want 1", cards)
	}
	mustContain(t, body, `c-actor-card__notes`)
	mustContain(t, body, `2 notes`)
}

func TestActors_NoteSingularGrammar(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	if err := job.RunNote(db, id, "only note", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `1 note`)
	if strings.Contains(body, `1 notes`) {
		t.Errorf("singular note should not be pluralized")
	}
}

func TestActors_ActiveClaimComesBeforeHistoryInDOM(t *testing.T) {
	db := setupLogTestDB(t)
	// alice has a current claim on task A and a finished done on task B.
	idA := mustAdd(t, db, "alice", "alice-claimed-task", nil, nil)
	mustClaim(t, db, idA, "alice")
	idB := mustAdd(t, db, "alice", "alice-done-task", nil, nil)
	if _, _, err := job.RunDone(db, []string{idB}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// CSS uses column-reverse to dock claims at the visual bottom, so
	// the current claim card must appear earlier in DOM than the done
	// (history) card.
	claimIdx := strings.Index(body, "alice-claimed-task")
	doneIdx := strings.Index(body, "alice-done-task")
	if claimIdx < 0 || doneIdx < 0 {
		t.Fatalf("expected both task titles in body; got claim=%d done=%d\n%s", claimIdx, doneIdx, body)
	}
	if claimIdx > doneIdx {
		t.Errorf("active claim should precede history in DOM (claim at %d, done at %d)", claimIdx, doneIdx)
	}
}

func TestActors_IdleColumnGetsIdleClass(t *testing.T) {
	db := setupLogTestDB(t)
	// alice has activity but no current claim → idle column.
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `c-actor-col--idle`)
	mustContain(t, body, `c-actor-col__status--idle`)
}

func TestActors_ActiveColumnNotIdle(t *testing.T) {
	db := setupLogTestDB(t)
	// alice has a current claim → not idle.
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	if strings.Contains(body, `c-actor-col--idle`) {
		t.Errorf("active actor (with a claim) should not get c-actor-col--idle")
	}
	if strings.Contains(body, `c-actor-col__status--idle`) {
		t.Errorf("active actor's status row should not carry the idle modifier")
	}
}

func TestActors_ColumnsOrderedByMostRecentActivity(t *testing.T) {
	db := setupLogTestDB(t)
	// Seed explicit timestamps so the ordering signal isn't lost in
	// the sub-second race that mustAdd() would create.
	now := time.Now()
	aliceID := homeSeedTask(t, db, "atask", "alice-task", "available", now.Add(-1*time.Hour))
	bobID := homeSeedTask(t, db, "btask", "bob-task", "available", now.Add(-30*time.Minute))
	homeSeedEventActor(t, db, aliceID, "created", "alice", now.Add(-1*time.Hour))
	homeSeedEventActor(t, db, bobID, "created", "bob", now.Add(-30*time.Minute))

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	aliceIdx := strings.Index(body, `data-actor="alice"`)
	bobIdx := strings.Index(body, `data-actor="bob"`)
	if aliceIdx < 0 || bobIdx < 0 {
		t.Fatalf("missing actor markers; alice=%d bob=%d", aliceIdx, bobIdx)
	}
	if bobIdx > aliceIdx {
		t.Errorf("most-recent actor (bob) should precede alice in DOM (bob=%d alice=%d)", bobIdx, aliceIdx)
	}
}

func TestActors_TaskCardLinksToTask(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `href="/tasks/`+id+`"`)
}

func TestActors_ClaimCountAndStatusLineRender(t *testing.T) {
	db := setupLogTestDB(t)
	id1 := mustAdd(t, db, "alice", "t1", nil, nil)
	id2 := mustAdd(t, db, "alice", "t2", nil, nil)
	mustClaim(t, db, id1, "alice")
	mustClaim(t, db, id2, "alice")

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `2 claims`)
}

func TestActors_CapsCardsPerColumn(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// Seed 120 distinct (alice, task) history pairs — each gets its
	// own card. With the cap at 100, only the 100 most recent should
	// render in alice's column.
	for i := range 120 {
		shortID := "t" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "task-"+strconv.Itoa(i), "available", now.Add(-time.Duration(120-i)*time.Minute))
		homeSeedEventActor(t, db, taskID, "created", "alice", now.Add(-time.Duration(120-i)*time.Minute))
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	cards := countOuterCards(body)
	if cards != handlers.ActorColumnCardLimit {
		t.Errorf("card count: got %d, want %d", cards, handlers.ActorColumnCardLimit)
	}
	// Newest card should be present; oldest should be dropped.
	mustContain(t, body, `task-119`)
	if strings.Contains(body, `>task-0<`) {
		t.Errorf("oldest history card should be truncated past the cap")
	}
}

func TestActors_CapPrioritizesClaimsOverHistory(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// 100 history cards…
	for i := range 100 {
		shortID := "h" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "h-"+strconv.Itoa(i), "available", now.Add(-time.Duration(200-i)*time.Minute))
		homeSeedEventActor(t, db, taskID, "created", "alice", now.Add(-time.Duration(200-i)*time.Minute))
	}
	// …plus one current claim. The claim must survive the cap even
	// though the column is already at the limit before it's added.
	cID := homeSeedTask(t, db, "claim1", "current-claim", "claimed", now.Add(-1*time.Minute))
	homeSeedEventActor(t, db, cID, "claimed", "alice", now.Add(-1*time.Minute))
	if _, err := db.Exec(`UPDATE tasks SET claimed_by='alice', claim_expires_at=? WHERE id=?`, now.Add(30*time.Minute).Unix(), cID); err != nil {
		t.Fatalf("set claim: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	cards := countOuterCards(body)
	if cards != handlers.ActorColumnCardLimit {
		t.Errorf("card count: got %d, want %d", cards, handlers.ActorColumnCardLimit)
	}
	mustContain(t, body, `current-claim`)
}

func TestActors_NoteBadgeCarriesDataNoteCount(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "t", nil, nil)
	for range 3 {
		if err := job.RunNote(db, id, "n", nil, "alice"); err != nil {
			t.Fatalf("RunNote: %v", err)
		}
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// Live-update module reads data-note-count to bump from the
	// SSR-rendered value rather than restarting at 1.
	mustContain(t, body, `data-note-count="3"`)
}

func TestActors_VerbSpanCarriesBaseClassForLiveUpdates(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// The verb span needs BOTH the base class and the modifier so
	// the live-update module's `.c-log-row__verb` selector can find
	// it on state transitions. Without the base class a card that
	// moves from "claimed" → "done" still reads "claimed" in the
	// browser until the page reloads.
	mustContain(t, body, `<span class="c-log-row__verb c-log-row__verb--claimed">claimed</span>`)
}

func TestActors_BoardExposesLiveDataHooks(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `data-actors-board`)
	mustContain(t, body, `data-actor="alice"`)
	// Card carries (actor, task) identity and the timestamp of its
	// latest state-changing event so JS can decide whether an
	// incoming SSE frame is fresher.
	mustContain(t, body, `data-actor-task="alice:`+id+`"`)
	mustContain(t, body, `data-event-at=`)
	mustContain(t, body, `data-claim="1"`)
}

func TestActors_LiveRegionDefaultsToAllEvents(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `<live-region src="/events">`)
}

func TestActors_ExcludesEventsOnSoftDeletedTasks(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	keepID := homeSeedTask(t, db, "keep1", "kept-task", "available", now.Add(-10*time.Minute))
	gone := homeSeedTask(t, db, "gone1", "ghost-task", "available", now.Add(-5*time.Minute))
	homeSeedEventActor(t, db, keepID, "created", "alice", now.Add(-10*time.Minute))
	homeSeedEventActor(t, db, gone, "created", "alice", now.Add(-5*time.Minute))
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = ? WHERE id = ?`, now.Unix(), gone); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `kept-task`)
	if strings.Contains(body, `ghost-task`) {
		t.Errorf("soft-deleted task should not surface on the board")
	}
}

func TestActors_ColumnTiebreakByActorName(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// Both columns share the exact same last-seen timestamp.
	idA := homeSeedTask(t, db, "ta", "a-task", "available", now.Add(-1*time.Hour))
	idB := homeSeedTask(t, db, "tb", "b-task", "available", now.Add(-1*time.Hour))
	homeSeedEventActor(t, db, idA, "created", "zoe", now.Add(-1*time.Hour))
	homeSeedEventActor(t, db, idB, "created", "alice", now.Add(-1*time.Hour))

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	aliceIdx := strings.Index(body, `data-actor="alice"`)
	zoeIdx := strings.Index(body, `data-actor="zoe"`)
	if aliceIdx < 0 || zoeIdx < 0 {
		t.Fatalf("missing markers; alice=%d zoe=%d", aliceIdx, zoeIdx)
	}
	if aliceIdx > zoeIdx {
		t.Errorf("with tied recency, alice (alphabetically first) should precede zoe (alice=%d zoe=%d)", aliceIdx, zoeIdx)
	}
}

func TestActors_DescriptionOmittedWhenEmpty(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil) // empty description

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	if strings.Contains(body, `c-actor-card__desc`) {
		t.Errorf("empty description should not render the desc paragraph")
	}
}

func TestActors_MultipleNotesAccumulate(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "t", nil, nil)
	for range 4 {
		if err := job.RunNote(db, id, "n", nil, "alice"); err != nil {
			t.Fatalf("RunNote: %v", err)
		}
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	mustContain(t, body, `4 notes`)
}

func TestActors_ReleasedCardSitsInHistoryNotClaimBand(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunRelease(db, id, "", "alice"); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	// Released card → no claim badge, since claimed_by is nil now.
	if strings.Contains(body, `data-claim="1"`) {
		t.Errorf("released card should not carry data-claim")
	}
}

func TestActors_EmptyDatabaseRendersEmptyBoard(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	if strings.Contains(body, `c-actor-col`) {
		t.Errorf("empty db should render no actor columns")
	}
	mustContain(t, body, `c-actors-board`)
	mustContain(t, body, `class="c-actors-board__empty"`)
	// The empty state is worded for the current window: with the
	// default 7d range in force, "nothing ever" would be a claim the
	// page cannot make. ?range=all is where the absolute wording
	// still belongs.
	mustContain(t, body, `No actor activity in the last 7 days.`)
	mustContain(t, fetchActorsRange(t, deps, "range=all"), `No actors have touched this store yet.`)
}

// --- ?at time-travel tests (R0Ro4) ---

func fetchActorsStatus(t *testing.T, deps handlers.Deps, query string) (int, string) {
	t.Helper()
	url := "/actors"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handlers.Actors(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func fetchActorsQuery(t *testing.T, deps handlers.Deps, query string) string {
	t.Helper()
	code, body := fetchActorsStatus(t, deps, query)
	if code != 200 {
		t.Fatalf("GET /actors?%s: status %d, body=%s", query, code, body)
	}
	return body
}

func TestActors_AtFiltersEventWalkToUpperBound(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-early", nil, nil)
	idLate := mustAdd(t, db, "bob", "bob-late", nil, nil)
	atEarly := positionBeforeTaskCreate(t, db, idLate)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchActorsQuery(t, deps, "at="+atEarly))

	if !strings.Contains(body, "alice-early") {
		t.Errorf("?at=%s should still render alice-early (at or before the cursor)", atEarly)
	}
	if strings.Contains(body, "bob-late") {
		t.Errorf("?at=%s should NOT render bob-late (after the cursor)", atEarly)
	}
	if strings.Contains(body, `data-actor="bob"`) {
		t.Errorf("?at=%s should not produce a bob column at all (no events at or before the cursor)", atEarly)
	}
}

func TestActors_AtAboveHeadRendersAsLive(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchActorsQuery(t, deps, "at="+atPastHead)

	mustContain(t, body, `data-actor="alice"`)
	mustContain(t, body, `alice-task`)
}

func TestActors_AtMalformedReturns400(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)
	deps := newLogDeps(t, db)

	// A bare row id is no longer a cursor: a rebuild renumbers those.
	for _, raw := range []string{"at=foo", "at=0", "at=-1", "at=42"} {
		t.Run(raw, func(t *testing.T) {
			code, _ := fetchActorsStatus(t, deps, raw)
			if code != 400 {
				t.Errorf("GET /actors?%s: status %d, want 400", raw, code)
			}
		})
	}
}

// In ?at mode, claim docking must reflect claim state at that moment,
// not the live tasks.claimed_by column. alice claims, then releases —
// pinned at the moment of the claim, the card should still be a claim.
func TestActors_AtDerivesClaimFromEventWalk(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	// Capture the claim's position before releasing.
	atClaim := ""
	for _, e := range allEvents(t, db) {
		if e.ShortID == id && e.EventType == "claimed" {
			atClaim = e.Position().String()
		}
	}
	if atClaim == "" {
		t.Fatal("no claimed event")
	}

	if err := job.RunRelease(db, id, "", "alice"); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	deps := newLogDeps(t, db)

	// Live mode: card sits in history (not docked).
	live := fetchActorsQuery(t, deps, "")
	if strings.Contains(live, `c-log-row__verb--released`) == false {
		t.Errorf("live view should show the released verb")
	}

	// ?at=<claim event>: pinned at the moment of the claim, the card
	// should appear as the actor's active claim — verb = claimed, and
	// the column status text should report 1 claim.
	body := fetchActorsQuery(t, deps, "at="+atClaim)
	if !strings.Contains(body, `c-log-row__verb--claimed`) {
		t.Errorf("?at=<claim event> should render the card with verb=claimed:\n%s", body)
	}
	if strings.Contains(body, `c-log-row__verb--released`) {
		t.Errorf("?at=<claim event> should NOT render the released verb (release happens after at)")
	}
	if !strings.Contains(body, `1 claim`) {
		t.Errorf("?at=<claim event> should report 1 claim in the column status, got body:\n%s", body)
	}
}

// --- range selector (?range=7d|14d|30d|all) ---

// fetchActorsRange drives /actors with a raw query string.
func fetchActorsRange(t *testing.T, deps handlers.Deps, query string) string {
	t.Helper()
	url := "/actors"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handlers.Actors(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET %s: status %d, body=%s", url, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// backdateActorEvents rewrites every event by actor to age ago, so a
// test can place an actor outside the default 7-day window.
func backdateActorEvents(t *testing.T, db *sql.DB, actor string, age time.Duration) {
	t.Helper()
	ts := time.Now().Add(-age).Unix()
	if _, err := db.Exec(`UPDATE events SET created_at = ? WHERE actor = ?`, ts, actor); err != nil {
		t.Fatalf("backdateActorEvents(%q): %v", actor, err)
	}
}

// backdateActorTaskEvents backdates only one actor's events on one
// task, so a column can straddle the cutoff.
func backdateActorTaskEvents(t *testing.T, db *sql.DB, actor, shortID string, age time.Duration) {
	t.Helper()
	ts := time.Now().Add(-age).Unix()
	_, err := db.Exec(
		`UPDATE events SET created_at = ?
		  WHERE actor = ? AND task_id = (SELECT id FROM tasks WHERE short_id = ?)`,
		ts, actor, shortID)
	if err != nil {
		t.Fatalf("backdateActorTaskEvents(%q, %q): %v", actor, shortID, err)
	}
}

func countActorColumns(body string) int {
	return strings.Count(body, `<section class="c-actor-col`)
}

func TestActors_DefaultRangeDropsActorsWithNoEventInSevenDays(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 10*24*time.Hour)

	deps := newLogDeps(t, db)
	body := fetchActorsRange(t, deps, "")

	if n := countActorColumns(body); n != 1 {
		t.Errorf("default range column count: got %d, want 1", n)
	}
	mustContain(t, body, `data-actor="fresh"`)
	if strings.Contains(stripInitialFrame(body), `data-actor="stale"`) {
		t.Errorf("stale actor (10d old) should be outside the default 7d window")
	}
}

func TestActors_WiderRangesReadmitOlderActors(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 20*24*time.Hour)

	deps := newLogDeps(t, db)

	if n := countActorColumns(fetchActorsRange(t, deps, "range=14d")); n != 1 {
		t.Errorf("range=14d column count: got %d, want 1 (stale is 20d old)", n)
	}
	if n := countActorColumns(fetchActorsRange(t, deps, "range=30d")); n != 2 {
		t.Errorf("range=30d column count: got %d, want 2", n)
	}
	if n := countActorColumns(fetchActorsRange(t, deps, "range=all")); n != 2 {
		t.Errorf("range=all column count: got %d, want 2", n)
	}
}

func TestActors_InvalidRangeFallsBackToSevenDays(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 10*24*time.Hour)

	deps := newLogDeps(t, db)
	body := fetchActorsRange(t, deps, "range=90d")

	if n := countActorColumns(body); n != 1 {
		t.Errorf("invalid range column count: got %d, want 1 (fall back to 7d)", n)
	}
	mustContain(t, body, `<a href="/actors" class="c-tab c-tab--active" aria-current="true">7D</a>`)
}

func TestActors_CardsAreLimitedToTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "recent-task", nil, nil)
	ancient := mustAdd(t, db, "alice", "ancient-task", nil, nil)
	backdateActorTaskEvents(t, db, "alice", ancient, 10*24*time.Hour)

	deps := newLogDeps(t, db)

	body := stripInitialFrame(fetchActorsRange(t, deps, ""))
	if n := countOuterCards(body); n != 1 {
		t.Errorf("default range card count: got %d, want 1", n)
	}
	mustContain(t, body, "recent-task")
	if strings.Contains(body, "ancient-task") {
		t.Errorf("card for a 10-day-old event should be outside the default 7d window\n---\n%s", body)
	}

	all := stripInitialFrame(fetchActorsRange(t, deps, "range=all"))
	if n := countOuterCards(all); n != 2 {
		t.Errorf("range=all card count: got %d, want 2", n)
	}
	mustContain(t, all, "ancient-task")
}

func TestActors_RangeTabsAreLinksAndMarkTheActiveOne(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	body := fetchActorsRange(t, deps, "")
	mustContain(t, body, `<nav class="c-tabs" aria-label="Range">`)
	mustContain(t, body, `<a href="/actors" class="c-tab c-tab--active" aria-current="true">7D</a>`)
	mustContain(t, body, `<a href="/actors?range=14d" class="c-tab">14D</a>`)
	mustContain(t, body, `<a href="/actors?range=30d" class="c-tab">30D</a>`)
	mustContain(t, body, `<a href="/actors?range=all" class="c-tab">All</a>`)

	all := fetchActorsRange(t, deps, "range=all")
	mustContain(t, all, `<a href="/actors?range=all" class="c-tab c-tab--active" aria-current="true">All</a>`)
	mustContain(t, all, `<a href="/actors" class="c-tab">7D</a>`)
	if strings.Count(all, `aria-current="true"`) != 1 {
		t.Errorf("exactly one range tab should carry aria-current\n---\n%s", all)
	}
}

// Scrubbed into history, the window is measured back from the cursor
// rather than from wall-clock now. Anchoring on now instead would
// render an empty board here: every event predates now-7d.
func TestActors_RangeIsAnchoredOnTheScrubberCursor(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustAdd(t, db, "bob", "bob-task", nil, nil)
	mustAdd(t, db, "carol", "carol-task", nil, nil)
	backdateActorEvents(t, db, "alice", 40*24*time.Hour)
	backdateActorEvents(t, db, "bob", 38*24*time.Hour)

	bobAt := ""
	for _, e := range allEvents(t, db) {
		if e.Actor == "bob" {
			bobAt = e.Position().String()
		}
	}
	if bobAt == "" {
		t.Fatal("no bob event")
	}

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchActorsRange(t, deps, "at="+bobAt+"&range=7d"))

	if n := countActorColumns(body); n != 2 {
		t.Errorf("column count: got %d, want 2 (alice at -40d and bob at -38d are both inside the week before the cursor)\n---\n%s", n, body)
	}
	mustContain(t, body, `data-actor="alice"`)
	mustContain(t, body, `data-actor="bob"`)
	if strings.Contains(body, `data-actor="carol"`) {
		t.Errorf("carol is past the ?at upper bound and should not appear")
	}
}

// The range tabs keep ?at so switching windows doesn't jump the
// scrubber back to live.
func TestActors_RangeTabsPreserveTheAtCursor(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	at := positionBack(t, db, 0)
	deps := newLogDeps(t, db)
	body := fetchActorsRange(t, deps, "at="+at)

	mustContain(t, body, `href="/actors?at=`+at+`"`)
	mustContain(t, body, `href="/actors?at=`+at+`&amp;range=all"`)
}
