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

func fetchActorSingle(t *testing.T, deps handlers.Deps, name string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/actors/"+name, nil)
	req.SetPathValue("name", name)
	w := httptest.NewRecorder()
	handlers.ActorSingle(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func mustFetchActorSingle(t *testing.T, deps handlers.Deps, name string) string {
	t.Helper()
	code, body := fetchActorSingle(t, deps, name)
	if code != 200 {
		t.Fatalf("GET /actors/%s: status %d, body=%s", name, code, body)
	}
	return body
}

func TestActorSingle_RendersHeroWithNameAndAvatar(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `c-actor-hero`)
	mustContain(t, body, `c-actor-hero__avatar`)
	mustContain(t, body, `data-actor="alice"`)
	mustContain(t, body, `>alice<`)
	// Initial inside the hero avatar
	mustContain(t, body, `>A<`)
}

func TestActorSingle_BreadcrumbLinksBackToActors(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `href="/actors"`)
	mustContain(t, body, `All actors`)
}

func TestActorSingle_HeaderTabActorsActive(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `href="/actors" class="c-tab c-tab--active"`)
}

func TestActorSingle_HeroStatsTilesRender(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	for _, label := range []string{"In flight", "Done 1h", "Done 24h", "Blocked"} {
		if !strings.Contains(body, label) {
			t.Errorf("missing stat tile label %q", label)
		}
	}
	// Four stat tiles expected.
	tiles := strings.Count(body, `class="c-actor-stat"`)
	if tiles != 4 {
		t.Errorf("c-actor-stat tile count: got %d, want 4", tiles)
	}
}

func TestActorSingle_StatsCountsAreAccurate(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()

	// alice: 2 currently claimed, 3 done in last 1h, 5 done in 24h total.
	for i := range 2 {
		shortID := "c" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "claimed-"+strconv.Itoa(i), "claimed", now.Add(-10*time.Minute))
		homeSeedEventActor(t, db, taskID, "claimed", "alice", now.Add(-10*time.Minute))
		if _, err := db.Exec(`UPDATE tasks SET claimed_by='alice', claim_expires_at=? WHERE id=?`,
			now.Add(30*time.Minute).Unix(), taskID); err != nil {
			t.Fatalf("set claim: %v", err)
		}
	}
	// 3 done in last hour
	for i := range 3 {
		shortID := "d" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "done-1h-"+strconv.Itoa(i), "done", now.Add(-30*time.Minute))
		homeSeedEventActor(t, db, taskID, "done", "alice", now.Add(-30*time.Minute))
	}
	// 2 done between 1h and 24h ago — count toward 24h, not 1h
	for i := range 2 {
		shortID := "o" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "done-old-"+strconv.Itoa(i), "done", now.Add(-6*time.Hour))
		homeSeedEventActor(t, db, taskID, "done", "alice", now.Add(-6*time.Hour))
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	// Locate each stat tile by label and confirm its value.
	checks := map[string]string{
		"In flight": "2",
		"Done 1h":   "3",
		"Done 24h":  "5",
		"Blocked":   "0",
	}
	for label, want := range checks {
		assertStatTileValue(t, body, label, want)
	}
}

func TestActorSingle_TimelineHasFiveLanes(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `c-actor-timeline`)
	for _, verb := range []string{"created", "claimed", "done", "blocked", "noted"} {
		marker := `c-actor-timeline__lane-label">` + verb
		if !strings.Contains(body, marker) {
			t.Errorf("missing timeline lane for %q", verb)
		}
	}
}

func TestActorSingle_TimelineMarksCarryXPercent(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	taskID := homeSeedTask(t, db, "task1", "task1", "available", now.Add(-12*time.Hour))
	homeSeedEventActor(t, db, taskID, "created", "alice", now.Add(-12*time.Hour))

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	// At 12h ago on a 24h axis the mark sits ~50% in.
	mustContain(t, body, `c-actor-timeline__mark--created`)
	if !strings.Contains(body, `--x:50.`) && !strings.Contains(body, `--x:49.`) {
		t.Errorf("expected timeline mark near 50%% for an event 12h ago; body did not contain it")
	}
}

func TestActorSingle_EventListShowsOnlyThisActor(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustAdd(t, db, "bob", "bob-task", nil, nil)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(mustFetchActorSingle(t, deps, "alice"))

	mustContain(t, body, `alice-task`)
	if strings.Contains(body, `bob-task`) {
		t.Errorf("bob's events should not appear on alice's actor page")
	}
}

func TestActorSingle_404ForUnknownActor(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	code, _ := fetchActorSingle(t, deps, "nobody")
	if code != 404 {
		t.Errorf("unknown actor: got status %d, want 404", code)
	}
}

func TestActorSingle_StatusLineMentionsClaimsAndLastSeen(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "t", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `1 claim`)
	mustContain(t, body, `last seen`)
}

func TestActorSingle_LiveRegionScopedToActor(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `<live-region src="/events?actor=alice">`)
}

func TestActorSingle_EventListCarriesActorMarker(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `data-actor-events="alice"`)
}

func TestActorSingle_EventListCapsAtLimit(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// Seed more events than the cap. Each task gives one created
	// event, so cap+20 tasks = cap+20 events on alice's column.
	total := handlers.ActorEventListLimit + 20
	for i := range total {
		shortID := "x" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "task-"+strconv.Itoa(i), "available", now.Add(-time.Duration(total-i)*time.Minute))
		homeSeedEventActor(t, db, taskID, "created", "alice", now.Add(-time.Duration(total-i)*time.Minute))
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	rows := strings.Count(body, `class="c-log-row__time"`)
	if rows != handlers.ActorEventListLimit {
		t.Errorf("event row count: got %d, want %d", rows, handlers.ActorEventListLimit)
	}
}

func TestActorSingle_ViewAllLinkPointsToLogFilter(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "t", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `href="/log?actor=alice"`)
	mustContain(t, body, `View all in Log`)
}

func TestActorSingle_StatsScopedToThisActorOnly(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// Bob has done 5 tasks recently — none of those should pollute
	// alice's tile counts.
	for i := range 5 {
		shortID := "bob" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "bobs-"+strconv.Itoa(i), "done", now.Add(-15*time.Minute))
		homeSeedEventActor(t, db, taskID, "done", "bob", now.Add(-15*time.Minute))
	}
	// Alice has 1 done in last hour.
	taskID := homeSeedTask(t, db, "ali1", "alice-done", "done", now.Add(-15*time.Minute))
	homeSeedEventActor(t, db, taskID, "done", "alice", now.Add(-15*time.Minute))

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	assertStatTileValue(t, body, "Done 1h", "1")
	assertStatTileValue(t, body, "Done 24h", "1")
}

func TestActorSingle_TimelineExcludesEventsOlderThan24h(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	old := homeSeedTask(t, db, "old1", "old-task", "available", now.Add(-30*time.Hour))
	homeSeedEventActor(t, db, old, "created", "alice", now.Add(-30*time.Hour))

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	if strings.Contains(body, `c-actor-timeline__mark--created`) {
		t.Errorf("event 30h ago should not produce a timeline mark")
	}
	mustContain(t, body, `0 events`)
	mustContain(t, body, `class="c-actor-timeline__empty"`)
	mustContain(t, body, `No activity in the last 24 hours.`)
}

func TestActorSingle_TimelineCountsTotalEventsInWindow(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	for i := range 3 {
		shortID := "t" + strconv.Itoa(i)
		taskID := homeSeedTask(t, db, shortID, "t-"+strconv.Itoa(i), "available", now.Add(-30*time.Minute))
		homeSeedEventActor(t, db, taskID, "created", "alice", now.Add(-30*time.Minute))
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `3 events`)
}

func TestActorSingle_404RendersStyledErrorPage(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	code, body := fetchActorSingle(t, deps, "nobody")
	if code != 404 {
		t.Fatalf("status: got %d, want 404", code)
	}
	// Styled page goes through the shared layout (page chrome).
	mustContain(t, body, `c-header`)
	mustContain(t, body, `Actor not found`)
}

func TestActorSingle_ClaimExpiredRendersAsSystem(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "expired-task", nil, nil)
	taskID := taskIDForShortID(t, db, id)
	if _, err := db.Exec(`INSERT INTO events (task_id, event_type, actor, detail, created_at) VALUES (?, 'claim_expired', 'alice', '', ?)`, taskID, time.Now().Unix()); err != nil {
		t.Fatalf("seed claim_expired: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	// Find the claim_expired row and confirm it renders the system
	// actor + "expired" verb, with no anchor on its actor cell.
	rows := splitLogRows(body)
	var found bool
	for _, row := range rows {
		if !strings.Contains(row, `c-log-row--claim_expired`) {
			continue
		}
		found = true
		if !strings.Contains(row, `c-log-row__actor--system`) {
			t.Errorf("claim_expired row missing system actor marker:\n%s", row)
		}
		if !strings.Contains(row, `>Jobs<`) {
			t.Errorf("claim_expired row should label the actor as Jobs:\n%s", row)
		}
		if !strings.Contains(row, `>expired</span>`) {
			t.Errorf("claim_expired row should render verb text 'expired':\n%s", row)
		}
		if strings.Contains(row, `href="/actors/alice"`) {
			t.Errorf("claim_expired row should not link to the prior claimer:\n%s", row)
		}
	}
	if !found {
		t.Fatalf("no claim_expired row in body:\n%s", body)
	}
}

func TestActorSingle_BlockedTilePositiveCount(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	// Two tasks alice claims; both are blocked by an open blocker.
	for i := range 2 {
		shortID := "k" + strconv.Itoa(i)
		blockerID := homeSeedTask(t, db, "B"+strconv.Itoa(i), "blocker-"+strconv.Itoa(i), "available", now.Add(-1*time.Hour))
		taskID := homeSeedTask(t, db, shortID, "stuck-"+strconv.Itoa(i), "claimed", now.Add(-30*time.Minute))
		if _, err := db.Exec(`UPDATE tasks SET claimed_by='alice', claim_expires_at=? WHERE id=?`,
			now.Add(30*time.Minute).Unix(), taskID); err != nil {
			t.Fatalf("set claim: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)`,
			blockerID, taskID, now.Unix()); err != nil {
			t.Fatalf("insert block: %v", err)
		}
		homeSeedEventActor(t, db, taskID, "claimed", "alice", now.Add(-30*time.Minute))
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	assertStatTileValue(t, body, "Blocked", "2")
}

func TestActorSingle_EventListExcludesSoftDeletedTasks(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	live := homeSeedTask(t, db, "live1", "live-task", "available", now.Add(-15*time.Minute))
	gone := homeSeedTask(t, db, "gone1", "ghost-task", "available", now.Add(-5*time.Minute))
	homeSeedEventActor(t, db, live, "created", "alice", now.Add(-15*time.Minute))
	homeSeedEventActor(t, db, gone, "created", "alice", now.Add(-5*time.Minute))
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = ? WHERE id = ?`, now.Unix(), gone); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	mustContain(t, body, `live-task`)
	if strings.Contains(body, `ghost-task`) {
		t.Errorf("soft-deleted task should not appear in the event list")
	}
}

// assertStatTileValue locates a c-actor-stat tile whose label matches
// `label` and asserts its value matches `want`. The DOM order of the
// .c-actor-stat__value followed by .c-actor-stat__label inside the
// same tile lets us scope the value to the right tile.
func assertStatTileValue(t *testing.T, body, label, want string) {
	t.Helper()
	needle := `class="c-actor-stat__label">` + label
	before, _, ok := strings.Cut(body, needle)
	if !ok {
		t.Errorf("missing stat tile %q", label)
		return
	}
	// Walk back to find the preceding c-actor-stat__value.
	prefix := before
	valueOpen := strings.LastIndex(prefix, `class="c-actor-stat__value">`)
	if valueOpen < 0 {
		t.Errorf("no value tag preceding label %q", label)
		return
	}
	valueOpen += len(`class="c-actor-stat__value">`)
	valueEnd := strings.Index(prefix[valueOpen:], `<`)
	if valueEnd < 0 {
		t.Errorf("malformed value tag preceding label %q", label)
		return
	}
	got := strings.TrimSpace(prefix[valueOpen : valueOpen+valueEnd])
	if got != want {
		t.Errorf("%s tile value: got %q, want %q", label, got, want)
	}
}

// --- Shared log row (task yOO7t) ---

// fetchActorSingleQuery drives the handler with a query string, which
// the window control (task 02FhN) reads.
func fetchActorSingleQuery(t *testing.T, deps handlers.Deps, name, query string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/actors/"+name+"?"+query, nil)
	req.SetPathValue("name", name)
	w := httptest.NewRecorder()
	handlers.ActorSingle(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /actors/%s?%s: status %d, body=%s", name, query, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// seedFoundIn records a found_in_set event by actor: two tasks, the
// second surfaced by the first.
func seedFoundIn(t *testing.T, db *sql.DB, actor string) (task, source string) {
	t.Helper()
	source = mustAdd(t, db, actor, "the source task", nil, nil)
	task = mustAdd(t, db, actor, "the surfaced task", nil, nil)
	if err := job.RunSetFoundIn(db, task, source, actor); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}
	return task, source
}

// rowWithMeta returns the single c-log-row in body whose metadata cell
// carries data-event-meta="<eventType>", trimmed to the row's own
// markup. splitLogRows slices up to the *next* row, so the tail would
// otherwise carry whatever the surrounding page puts after the list.
// The row nests no <div>, so its first closing tag is its own.
func rowWithMeta(t *testing.T, body, eventType string) string {
	t.Helper()
	needle := `data-event-meta="` + eventType + `"`
	var found []string
	for _, row := range splitLogRows(stripInitialFrame(body)) {
		if !strings.Contains(row, needle) {
			continue
		}
		if end := strings.Index(row, "</div>"); end >= 0 {
			row = row[:end+len("</div>")]
		}
		found = append(found, strings.TrimSpace(row))
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one row with %s, got %d\n---\n%s", needle, len(found), body)
	}
	return found[0]
}

// TestActorSingle_FoundInSetRowMatchesLogRow is criterion 5VL: the
// actor page's Events section is the Log filtered to one actor, so the
// same event must produce byte-identical markup. Nothing is normalised
// away here — the redundant actor cell is hidden by the *list's*
// modifier, so the row itself carries no trace of which page it is on.
func TestActorSingle_FoundInSetRowMatchesLogRow(t *testing.T) {
	db := setupLogTestDB(t)
	seedFoundIn(t, db, "alice")
	deps := newLogDeps(t, db)

	logRow := rowWithMeta(t, fetchLog(t, deps, "actor=alice"), "found_in_set")
	actorRow := rowWithMeta(t, mustFetchActorSingle(t, deps, "alice"), "found_in_set")

	if actorRow != logRow {
		t.Errorf("actor-page row differs from log row\n  log:   %s\n  actor: %s", logRow, actorRow)
	}
}

// The hiding hangs off the list, so the list is what has to be marked —
// and the rows inside it must stay clean.
func TestActorSingle_EventListCarriesSingleActorModifier(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := mustFetchActorSingle(t, deps, "alice")
	mustContain(t, body, `class="c-log c-log--single-actor"`)
	if strings.Contains(body, "c-log-row--single-actor") {
		t.Errorf("the modifier belongs on the list, not on a row:\n%s", body)
	}
}

// The Log's folded verbs and metadata cell must reach the actor page,
// which before task yOO7t had its own thinner mapping.
func TestActorSingle_FoundInSetRendersFoldedVerbAndMetadata(t *testing.T) {
	db := setupLogTestDB(t)
	_, source := seedFoundIn(t, db, "alice")
	deps := newLogDeps(t, db)

	row := rowWithMeta(t, mustFetchActorSingle(t, deps, "alice"), "found_in_set")
	mustContain(t, row, `>found in<`)
	mustContain(t, row, `<a class="c-id-pill" href="/tasks/`+source+`">`+source+`</a>`)
}

// --- Timeline window control (task 02FhN) ---

func TestActorSingle_TimelineWindowControlRendersThreeOptions(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := mustFetchActorSingle(t, deps, "alice")
	for _, label := range []string{">24H<", ">7D<", ">30D<"} {
		mustContain(t, body, label)
	}
	mustContain(t, body, `href="/actors/alice?window=7d"`)
	mustContain(t, body, `href="/actors/alice?window=30d"`)
}

func TestActorSingle_TimelineWindowDefaultsTo24h(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := mustFetchActorSingle(t, deps, "alice")
	mustContain(t, body, `Timeline · 24 hours`)
	mustContain(t, body, `href="/actors/alice?window=24h" class="c-tab c-tab--active" aria-current="page"`)
}

func TestActorSingle_TimelineWindow7dSelected(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchActorSingleQuery(t, deps, "alice", "window=7d")
	mustContain(t, body, `Timeline · 7 days`)
	mustContain(t, body, `href="/actors/alice?window=7d" class="c-tab c-tab--active" aria-current="page"`)
}

func TestActorSingle_TimelineWindow30dSelected(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchActorSingleQuery(t, deps, "alice", "window=30d")
	mustContain(t, body, `Timeline · 30 days`)
	mustContain(t, body, `href="/actors/alice?window=30d" class="c-tab c-tab--active" aria-current="page"`)
}

func TestActorSingle_InvalidWindowFallsBackTo24h(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchActorSingleQuery(t, deps, "alice", "window=nonsense")
	mustContain(t, body, `Timeline · 24 hours`)
	mustContain(t, body, `href="/actors/alice?window=24h" class="c-tab c-tab--active" aria-current="page"`)
}

// An event three days old is outside the 24h window and inside 7d, so
// the lanes and the event count must both follow the selected window.
func TestActorSingle_TimelineMarksScaleToWindow(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	taskID := homeSeedTask(t, db, "w1", "old work", "done", now.Add(-72*time.Hour))
	homeSeedEventActor(t, db, taskID, "done", "alice", now.Add(-72*time.Hour))
	deps := newLogDeps(t, db)

	day := mustFetchActorSingle(t, deps, "alice")
	if strings.Contains(day, `c-actor-timeline__mark--done`) {
		t.Errorf("72h-old event should not mark the 24h timeline\n%s", day)
	}
	mustContain(t, day, `>0 events<`)

	week := fetchActorSingleQuery(t, deps, "alice", "window=7d")
	mustContain(t, week, `c-actor-timeline__mark--done`)
	mustContain(t, week, `>1 events<`)
	// 3 of 7 days ago sits at 4/7 of the axis.
	mustContain(t, week, `--x:57.1%`)
}

func TestActorSingle_AxisLabelsScaleToWindow(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	mustContain(t, mustFetchActorSingle(t, deps, "alice"), `>18h<`)
	mustContain(t, fetchActorSingleQuery(t, deps, "alice", "window=7d"), `>5d<`)
	mustContain(t, fetchActorSingleQuery(t, deps, "alice", "window=30d"), `>20d<`)
}

func TestActorSingle_EmptyTimelineMessageNamesWindow(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	taskID := homeSeedTask(t, db, "w2", "ancient", "done", now.Add(-90*24*time.Hour))
	homeSeedEventActor(t, db, taskID, "done", "alice", now.Add(-90*24*time.Hour))
	deps := newLogDeps(t, db)

	mustContain(t, mustFetchActorSingle(t, deps, "alice"), `No activity in the last 24 hours.`)
	mustContain(t, fetchActorSingleQuery(t, deps, "alice", "window=30d"), `No activity in the last 30 days.`)
}

// The hero's Done 24h tile answers "today", not "the selected window" —
// widening the timeline must not move it.
func TestActorSingle_Done24hIgnoresWindow(t *testing.T) {
	db := setupLogTestDB(t)
	now := time.Now()
	recent := homeSeedTask(t, db, "r1", "recent", "done", now.Add(-2*time.Hour))
	homeSeedEventActor(t, db, recent, "done", "alice", now.Add(-2*time.Hour))
	old := homeSeedTask(t, db, "r2", "older", "done", now.Add(-72*time.Hour))
	homeSeedEventActor(t, db, old, "done", "alice", now.Add(-72*time.Hour))
	deps := newLogDeps(t, db)

	for _, q := range []string{"", "window=7d", "window=30d"} {
		body := fetchActorSingleQuery(t, deps, "alice", q)
		idx := strings.Index(body, "Done 24h")
		if idx < 0 {
			t.Fatalf("no Done 24h tile (%q)", q)
		}
		tile := body[max(0, idx-160):idx]
		if !strings.Contains(tile, `<span class="c-actor-stat__value">1</span>`) {
			t.Errorf("Done 24h should stay 1 with %q, got tile %s", q, tile)
		}
	}
}

// The live module reads the window off the DOM so a new mark lands at
// the right percent without a reload.
func TestActorSingle_TimelineCarriesWindowSecondsForLiveMarks(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "a task", nil, nil)
	deps := newLogDeps(t, db)

	mustContain(t, mustFetchActorSingle(t, deps, "alice"), `data-timeline-window-secs="86400"`)
	mustContain(t, fetchActorSingleQuery(t, deps, "alice", "window=7d"), `data-timeline-window-secs="604800"`)
}
