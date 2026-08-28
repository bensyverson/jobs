package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/handlers"
	"github.com/bensyverson/jobs/internal/web/templates"
)

func setupLogTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newLogDeps wires a Deps bundle good enough to drive the Log handler
// in tests: real DB, real templates+manifest.
func newLogDeps(t *testing.T, db *sql.DB) handlers.Deps {
	t.Helper()
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	return handlers.Deps{DB: db, Templates: e}
}

func fetchLog(t *testing.T, deps handlers.Deps, query string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/log?"+query, nil)
	w := httptest.NewRecorder()
	handlers.Log(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /log?%s: status %d, body=%s", query, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestLog_RendersEventsAsRows(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Root task", nil, []string{"web"})

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	mustContain(t, body, `class="c-filter-bar"`)
	mustContain(t, body, `class="c-log-row c-log-row--created"`)
	mustContain(t, body, `>Root task<`)
	mustContain(t, body, `data-actor="alice"`)
}

func TestLog_ActorFilter_HidesOtherActors(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustAdd(t, db, "bob", "bob-task", nil, nil)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "actor=alice"))

	if !strings.Contains(body, "alice-task") {
		t.Errorf("/log?actor=alice should include alice-task")
	}
	if strings.Contains(body, "bob-task") {
		t.Errorf("/log?actor=alice should NOT include bob-task")
	}
	// Active chip markup present.
	mustContain(t, body, `href="/log" class="c-filter-chip">`) // "any" chip points at /log
	mustContain(t, body, `c-filter-chip--active`)
}

func TestLog_TypeFilter_HidesOtherTypes(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "A task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "type=claimed")

	// The "created" row exists in the DB but should be filtered out.
	if !strings.Contains(body, `c-log-row--claimed`) {
		t.Errorf("/log?type=claimed should include claimed rows")
	}
	if strings.Contains(body, `c-log-row--created`) {
		t.Errorf("/log?type=claimed should not include created rows")
	}
}

// TestLog_EmptyDatabase_RendersPlaceholder also pins the wording,
// which the range selector made conditional: with the default 7-day
// window in force, "nothing matches your filters" would be a claim
// the page cannot make when no filter is set. ?range=all is where the
// absolute wording is honest, and a filter puts the blame back on the
// filters.
func TestLog_EmptyDatabase_RendersPlaceholder(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	mustContain(t, fetchLog(t, deps, ""), `No events in the last 7 days.`)
	mustContain(t, fetchLog(t, deps, "range=30d"), `No events in the last 30 days.`)
	mustContain(t, fetchLog(t, deps, "range=all"), `No events recorded in this store yet.`)
	mustContain(t, fetchLog(t, deps, "actor=nobody"), `No events match the current filters.`)
}

func TestLog_LiveRegionSrc_ReflectsFilters(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, nil)

	deps := newLogDeps(t, db)

	// No filters → live-region points at /events.
	body := fetchLog(t, deps, "")
	mustContain(t, body, `<live-region src="/events">`)

	// actor=alice → live-region scoped to the same actor.
	body = fetchLog(t, deps, "actor=alice")
	mustContain(t, body, `<live-region src="/events?actor=alice">`)
}

func TestLog_ChipsPreserveOtherFilters(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, nil)
	deps := newLogDeps(t, db)

	// With ?actor=alice, the type chips should encode &actor=alice.
	body := fetchLog(t, deps, "actor=alice")
	mustContain(t, body, `/log?actor=alice&amp;type=claimed`)
}

// The label strip used to keep the ten most-used labels by task
// frequency. It is now the 24 most recently active labels inside the
// window (7QB2y): the Log is a historical stream, so "what has moved
// lately" is the question the strip answers, and frequency ordering
// buried a label that had just seen a burst of events. The cap and
// the always-show-the-selection rule live in log_chips_test.go; what
// remains here is the ordering claim the old test made.
func TestLog_LabelStripIsOrderedByMostRecentActivity(t *testing.T) {
	db := setupLogTestDB(t)
	// "bulk" is on far more tasks; "recent" moved last. Recency wins.
	for n := range 5 {
		mustAdd(t, db, "alice", "bulk-"+strconv.Itoa(n), nil, []string{"bulk"})
	}
	id := mustAdd(t, db, "alice", "recent-task", nil, []string{"recent"})
	mustClaim(t, db, id, "alice")

	bar := extractFilterBar(t, fetchLog(t, deps(t, db), ""))
	recent := strings.Index(bar, `data-label="recent"`)
	bulk := strings.Index(bar, `data-label="bulk"`)
	if recent < 0 || bulk < 0 {
		t.Fatalf("both labels should have chips, got recent=%d bulk=%d\n---\n%s", recent, bulk, bar)
	}
	if recent > bulk {
		t.Errorf("the label with the most recent event should come first, got recent=%d bulk=%d", recent, bulk)
	}
}

// deps is a shorthand for newLogDeps in the tests above that only
// need it inline.
func deps(t *testing.T, db *sql.DB) handlers.Deps {
	t.Helper()
	return newLogDeps(t, db)
}

func TestLog_PaginationCapsInitialRender(t *testing.T) {
	db := setupLogTestDB(t)
	// 12 events (creation only), explicit limit of 5.
	for i := range 12 {
		mustAdd(t, db, "alice", "task-"+strconv.Itoa(i), nil, nil)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "limit=5")

	// Row count: count occurrences of c-log-row__time (one per row).
	rows := strings.Count(body, `class="c-log-row__time"`)
	if rows != 5 {
		t.Errorf("?limit=5 should render 5 rows, got %d", rows)
	}
	// HasMore affordance present.
	mustContain(t, body, `c-log-row--more`)
}

func TestLog_PaginationLoadMoreFiltersOlderEvents(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 12 {
		mustAdd(t, db, "alice", "task-"+strconv.Itoa(i), nil, nil)
	}

	deps := newLogDeps(t, db)

	// Page 1 (newest 5): record the oldest id from the more-link.
	body := fetchLog(t, deps, "limit=5")
	// The href encodes "before=<oldestID>".
	idx := strings.Index(body, `?before=`)
	if idx == -1 {
		t.Fatalf("expected before=<id> in the more-link href:\n%s", body)
	}
	rest := body[idx+len(`?before=`):]
	end := strings.IndexAny(rest, `"&`)
	beforeID := rest[:end]

	// Page 2 (next 5 older): should include strictly fewer events.
	body = stripInitialFrame(fetchLog(t, deps, "limit=5&before="+beforeID))
	rows := strings.Count(body, `class="c-log-row__time"`)
	if rows != 5 {
		t.Errorf("page 2 should render 5 rows, got %d", rows)
	}
	// Page 1's newest task should not appear on page 2.
	if strings.Contains(body, "task-11") {
		t.Errorf("page 2 should not include the newest task")
	}
}

func TestLog_PaginationOmitsLoadMoreWhenAllEventsFit(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 3 {
		mustAdd(t, db, "alice", "task-"+strconv.Itoa(i), nil, nil)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "limit=10")

	if strings.Contains(body, `c-log-row--more`) {
		t.Errorf("with no more events to fetch, the load-more affordance should not render")
	}
}

func TestLog_CriteriaAddedAndCriterionStateRenderShortVerbs(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "subject", nil, nil)
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "alpha"},
		{Label: "beta"},
		{Label: "gamma"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "beta", job.CriterionPassed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	// Verb span must not echo the raw event type — that's the leak
	// EUW3M is closing. The CSS class can still carry the type for
	// styling hooks, mirroring how claim_expired is handled.
	if strings.Contains(body, `criteria_added</span>`) {
		t.Errorf("log verb span should not render the raw 'criteria_added' enum")
	}
	if strings.Contains(body, `criterion_state</span>`) {
		t.Errorf("log verb span should not render the raw 'criterion_state' enum")
	}
	// Verbs are now collapsed to single-word forms so the verb column
	// stays the same width as DONE / CLAIMED. The folded-out detail
	// (count, label) lives in the new metadata cell — see
	// TestLog_RowsCarryMetadataCell.
	mustContain(t, body, `c-log-row__verb--criteria_added">criteria<`)
	mustContain(t, body, `c-log-row__verb--criterion_state">criterion<`)
}

func TestLog_NoteBodiesRenderInMetadataCell(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "subject", nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunNote(db, id, "LOG_NOTE_BODY_PROGRESS", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{id}, false, "LOG_NOTE_BODY_DONE", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	// Note bodies surface in the per-event metadata cell so the row
	// reads at a glance without leaving the log. The cell carries
	// data-event-meta so it's locatable + style-targetable per type.
	mustContain(t, body, `data-event-meta="noted">LOG_NOTE_BODY_PROGRESS</span>`)
	mustContain(t, body, `data-event-meta="done">LOG_NOTE_BODY_DONE</span>`)
	// Verb spans still render. The 'noted' verb stays as-is — only the
	// inline trailing text moved into the dedicated cell.
	mustContain(t, body, `c-log-row__verb--noted">noted<`)
}

func TestLog_FilterChipsIncludeCriteriaTypes(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "subject", nil, nil)
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "alpha"},
		{Label: "beta"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "alpha", job.CriterionPassed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	deps := newLogDeps(t, db)
	bar := extractFilterBar(t, fetchLog(t, deps, ""))

	// Both event types appear as type chips in the filter bar.
	mustContain(t, bar, `/log?type=criteria_added`)
	mustContain(t, bar, `/log?type=criterion_state`)

	// And clicking either chip filters the log to just that type.
	body := fetchLog(t, deps, "type=criterion_state")
	if !strings.Contains(body, `c-log-row--criterion_state`) {
		t.Errorf("?type=criterion_state should include criterion_state rows")
	}
	if strings.Contains(body, `c-log-row--criteria_added`) {
		t.Errorf("?type=criterion_state should NOT include criteria_added rows")
	}
}

func TestLog_ClaimExpiredRendersAsExpiredVerb(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "expired-task", nil, nil)
	// Synthesize a claim_expired event directly. The expirer's actor
	// in production is whoever triggered the sweep, but for display
	// the row should read as a system event regardless.
	taskID := taskIDForShortID(t, db, id)
	if _, err := db.Exec(`INSERT INTO events (task_id, event_type, actor, detail, created_at) VALUES (?, 'claim_expired', 'alice', '', ?)`, taskID, time.Now().Unix()); err != nil {
		t.Fatalf("seed claim_expired: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	// The verb word is rendered lowercase (CSS uppercases), so the
	// payload string is "expired".
	mustContain(t, body, `<span class="c-log-row__verb c-log-row__verb--claim_expired">expired</span>`)
	if strings.Contains(body, `claim_expired</span>`) {
		t.Errorf("verb text should not include the raw event_type")
	}
}

func TestLog_ClaimExpiredActorRendersAsSystemNotLink(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "expired-task", nil, nil)
	taskID := taskIDForShortID(t, db, id)
	if _, err := db.Exec(`INSERT INTO events (task_id, event_type, actor, detail, created_at) VALUES (?, 'claim_expired', 'alice', '', ?)`, taskID, time.Now().Unix()); err != nil {
		t.Fatalf("seed claim_expired: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	// "Jobs" appears as the system actor label, no anchor and no
	// avatar dot tied to the original claimer.
	mustContain(t, body, `c-log-row__actor--system`)
	mustContain(t, body, `>Jobs<`)
	// Confirm the row does not render a link to /actors/alice for the
	// expired event. The other event for this task (the created
	// event by alice) still does, so we narrow by isolating the
	// claim_expired row's actor cell.
	rows := splitLogRows(body)
	for _, row := range rows {
		if strings.Contains(row, `c-log-row__verb--claim_expired`) {
			if strings.Contains(row, `href="/actors/`) {
				t.Errorf("claim_expired row should not link the actor to /actors/*\n%s", row)
			}
			if strings.Contains(row, `data-actor="alice"`) {
				t.Errorf("claim_expired row should not surface the prior claimer as the actor\n%s", row)
			}
		}
	}
}

// --- ?at time-travel tests (R0Ro4) ---

// fetchLogStatus returns the status code and body so tests can assert
// non-200 responses (the regular fetchLog helper t.Fatal's on non-200).
func fetchLogStatus(t *testing.T, deps handlers.Deps, query string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/log?"+query, nil)
	w := httptest.NewRecorder()
	handlers.Log(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// maxEventID returns the current max event id in the test DB. Used to
// fix `?at` boundaries in tests that need to compare against absolute
// event ids the seed produced.
func maxEventID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM events`).Scan(&id); err != nil {
		t.Fatalf("maxEventID: %v", err)
	}
	return id
}

// eventIDForTaskCreate returns the id of the `created` event for a
// given short task id — the most reliable way to pin `?at` to a known
// state ("just before" / "at" / "after" a particular task's creation).
func eventIDForTaskCreate(t *testing.T, db *sql.DB, shortID string) int64 {
	t.Helper()
	var id int64
	err := db.QueryRow(`
		SELECT e.id FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'created'
		LIMIT 1
	`, shortID).Scan(&id)
	if err != nil {
		t.Fatalf("eventIDForTaskCreate(%q): %v", shortID, err)
	}
	return id
}

func TestLog_AtFiltersToUpperBoundInclusive(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "first-task", nil, nil)
	idSecond := mustAdd(t, db, "alice", "second-task", nil, nil)
	mustAdd(t, db, "alice", "third-task", nil, nil)

	// Pin ?at to second-task's created event id. Inclusive: rows for
	// first and second appear; third does not.
	at := eventIDForTaskCreate(t, db, idSecond)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "at="+strconv.FormatInt(at, 10)))

	if !strings.Contains(body, "first-task") {
		t.Errorf("?at=%d should include first-task (event id < at)", at)
	}
	if !strings.Contains(body, "second-task") {
		t.Errorf("?at=%d should include second-task (event id == at, inclusive)", at)
	}
	if strings.Contains(body, "third-task") {
		t.Errorf("?at=%d should NOT include third-task (event id > at)", at)
	}
}

func TestLog_AtAboveHeadRendersAsLive(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "live-task", nil, nil)

	// at far above any real event id behaves as if there were no filter.
	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "at=999999999")

	if !strings.Contains(body, "live-task") {
		t.Errorf("?at past head should render the same as live")
	}
}

func TestLog_AtMalformedReturns400(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "task", nil, nil)
	deps := newLogDeps(t, db)

	for _, raw := range []string{"at=foo", "at=0", "at=-1", "at=1.5"} {
		t.Run(raw, func(t *testing.T) {
			code, _ := fetchLogStatus(t, deps, raw)
			if code != 400 {
				t.Errorf("GET /log?%s: status %d, want 400", raw, code)
			}
		})
	}
}

func TestLog_AtScopesTotalEventsCounter(t *testing.T) {
	db := setupLogTestDB(t)
	idA := mustAdd(t, db, "alice", "a", nil, nil)
	mustAdd(t, db, "alice", "b", nil, nil)
	mustAdd(t, db, "alice", "c", nil, nil)

	// Pin ?at to the first event. TotalEvents should reflect 1, not 3.
	at := eventIDForTaskCreate(t, db, idA)

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "at="+strconv.FormatInt(at, 10))

	// The total-events counter renders inline as part of the log meta
	// strip. The exact wording can vary, but "1 event" should be
	// derivable; "3 events" should not appear in that meta region.
	// We assert by looking at the page header copy that includes the
	// total — searching for the exact strings the template emits.
	if strings.Contains(body, "3 events") || strings.Contains(body, "of 3") {
		t.Errorf("?at=%d should not surface the live-mode 3 events count", at)
	}
}

func TestLog_AtComposesWithActorFilter(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-1", nil, nil)
	mustAdd(t, db, "bob", "bob-1", nil, nil)
	idLast := mustAdd(t, db, "alice", "alice-2", nil, nil)
	atLast := eventIDForTaskCreate(t, db, idLast)

	// Walk back one event from alice-2 — bob-1 is the most recent
	// alice/bob mix before alice-2.
	at := atLast - 1

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "actor=alice&at="+strconv.FormatInt(at, 10)))

	if !strings.Contains(body, "alice-1") {
		t.Errorf("?actor=alice&at=%d should include alice-1", at)
	}
	if strings.Contains(body, "bob-1") {
		t.Errorf("?actor=alice&at=%d should NOT include bob-1 (filtered by actor)", at)
	}
	if strings.Contains(body, "alice-2") {
		t.Errorf("?actor=alice&at=%d should NOT include alice-2 (event id > at)", at)
	}
}

func TestLog_AtComposesWithTypeFilter(t *testing.T) {
	// ?type filters by event_type; ?at clamps the event-id walk. The
	// two are independent axes and should AND together: only events
	// with id <= at AND event_type == ?type appear.
	db := setupLogTestDB(t)
	idA := mustAdd(t, db, "alice", "task-a", nil, nil)
	mustClaim(t, db, idA, "alice")
	mustAdd(t, db, "alice", "task-b", nil, nil) // created event, post-claim
	atClaim := maxEventID(t, db) - 1            // pin to the claim event of task-a

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "type=claimed&at="+strconv.FormatInt(atClaim, 10)))

	// The claimed event for task-a is included; the created events
	// for task-a and task-b are filtered out by ?type=claimed.
	if !strings.Contains(body, "task-a") {
		t.Errorf("?type=claimed&at=%d should surface task-a (its claim event matches both filters)", atClaim)
	}
	// task-b's only event is its `created`, which is filtered out.
	if strings.Contains(body, "task-b") {
		t.Errorf("?type=claimed&at=%d should NOT surface task-b (no claim event)", atClaim)
	}
}

func TestLog_AtComposesWithLabelFilter(t *testing.T) {
	// Label filter resolves to a task-id set, then events on those
	// tasks are kept. Combined with ?at, the intersection is "events
	// on label-X tasks with id <= at."
	db := setupLogTestDB(t)
	idLabeled := mustAdd(t, db, "alice", "labeled-task", nil, []string{"web"})
	mustAdd(t, db, "alice", "plain-task", nil, nil)
	idLater := mustAdd(t, db, "alice", "later-labeled", nil, []string{"web"})
	atMid := eventIDForTaskCreate(t, db, idLater) - 1
	_ = idLabeled

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "label=web&at="+strconv.FormatInt(atMid, 10)))

	if !strings.Contains(body, "labeled-task") {
		t.Errorf("?label=web&at=%d should include labeled-task", atMid)
	}
	if strings.Contains(body, "plain-task") {
		t.Errorf("?label=web&at=%d should NOT include plain-task (no matching label)", atMid)
	}
	if strings.Contains(body, "later-labeled") {
		t.Errorf("?label=web&at=%d should NOT include later-labeled (event id > at)", atMid)
	}
}

func TestLog_AtComposesWithTaskFilter(t *testing.T) {
	// ?task scopes to a single task's tree; ?at clamps the event-id
	// walk. Events outside the tree are filtered before ?at applies,
	// so the returned set is "events on task X with id <= at."
	db := setupLogTestDB(t)
	idA := mustAdd(t, db, "alice", "task-a", nil, nil)
	mustAdd(t, db, "alice", "task-b", nil, nil) // separate task
	mustClaim(t, db, idA, "alice")              // late event on task-a

	atCreate := eventIDForTaskCreate(t, db, idA)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "task="+idA+"&at="+strconv.FormatInt(atCreate, 10)))

	if !strings.Contains(body, "task-a") {
		t.Errorf("?task=%s&at=%d should include task-a's creation event", idA, atCreate)
	}
	if strings.Contains(body, "task-b") {
		t.Errorf("?task=%s&at=%d should NOT include task-b (different task tree)", idA, atCreate)
	}
	// task-a's claim is event id > atCreate; the verb must not appear.
	rows := splitLogRows(body)
	for _, r := range rows {
		if strings.Contains(r, "claimed") {
			t.Errorf("?task=%s&at=%d should NOT include the post-at claim event, got row:\n%s", idA, atCreate, r)
		}
	}
}

// TestLog_RowsCarryMetadataCell pins the redesign that gives each log
// row an extra "metadata" cell after the task title. Verbs collapse to
// single words (DONE, CRITERIA, …) so the verb column stays narrow,
// and the per-event payload that used to fold into the verb (criteria
// count, criterion label/state, completion-note text, …) lives in the
// new cell. Events with no payload (created, released, …) leave it
// empty.
func TestLog_RowsCarryMetadataCell(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "subject", nil, nil)
	mustClaim(t, db, id, "alice") // claimed event with duration "30m"
	if err := job.RunNote(db, id, "shipping update", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "alpha"}, {Label: "beta"}, {Label: "gamma"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "beta", job.CriterionPassed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	if _, err := job.RunLabelAdd(db, id, []string{"frontend"}, "alice"); err != nil {
		t.Fatalf("RunLabelAdd: %v", err)
	}
	// Block on a sibling so we get a blocked + unblocked pair.
	other := mustAdd(t, db, "alice", "blocker", nil, nil)
	if err := job.RunBlock(db, id, other, "alice"); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	if err := job.RunUnblock(db, id, other, "alice"); err != nil {
		t.Fatalf("RunUnblock: %v", err)
	}
	// Re-claim before close (RunUnblock cleared blockers but the prior
	// claim is still in place; dry-run not needed). Adding a release
	// step to populate that branch of the metadata coverage too — the
	// release should leave the cell empty since the reason rides on a
	// sibling noted event.
	if err := job.RunRelease(db, id, "", "alice"); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{id}, false, "ship it: launch complete", nil, "alice", true, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	// Cancel a separate task so we can assert the cancel-reason metadata.
	cancelID := mustAdd(t, db, "alice", "drop this", nil, nil)
	if _, _, _, err := job.RunCancel(db, []string{cancelID}, "out of scope", false, false, false, "alice"); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	// Each row carries a c-log-row__meta cell.
	mustContain(t, body, `c-log-row__meta`)

	// claimed → planned lease window from the event payload.
	mustContain(t, body, `data-event-meta="claimed"`)
	mustContain(t, body, `<span class="c-log-row__meta" data-event-meta="claimed">30m</span>`)

	// noted → progress-note text.
	mustContain(t, body, `<span class="c-log-row__meta" data-event-meta="noted">shipping update</span>`)

	// criteria_added → label list.
	mustContain(t, body, `<span class="c-log-row__meta" data-event-meta="criteria_added">alpha, beta, gamma</span>`)

	// criterion_state → label, color-keyed to the new state via the meta cell's
	// data-state attribute (mirroring the criteria SVG icon coloring).
	mustContain(t, body, `data-event-meta="criterion_state" data-state="passed">beta</span>`)

	// labeled → label name(s).
	mustContain(t, body, `data-event-meta="labeled">frontend</span>`)

	// blocked / unblocked → blocker id-pill.
	mustContainAll(t, body,
		`data-event-meta="blocked"`,
		`data-event-meta="unblocked"`,
	)
	// Each carries a c-id-pill for the blocker short id.
	otherShort := other
	mustContainAll(t, body,
		`data-event-meta="blocked"><a class="c-id-pill" href="/tasks/`+otherShort+`">`+otherShort+`</a>`,
		`data-event-meta="unblocked"><a class="c-id-pill" href="/tasks/`+otherShort+`">`+otherShort+`</a>`,
	)

	// done → completion note text.
	mustContain(t, body, `data-event-meta="done">ship it: launch complete</span>`)

	// canceled → reason text.
	mustContain(t, body, `data-event-meta="canceled">out of scope</span>`)

	// Events with no payload leave the cell empty (no inner text). The
	// span itself still renders for layout consistency.
	mustContain(t, body, `<span class="c-log-row__meta" data-event-meta="created"></span>`)
	mustContain(t, body, `<span class="c-log-row__meta" data-event-meta="released"></span>`)
}

// --- helpers ---

func mustAdd(t *testing.T, db *sql.DB, actor, title string, parent *string, labels []string) string {
	t.Helper()
	parentID := ""
	if parent != nil {
		parentID = *parent
	}
	res, err := job.RunAdd(db, parentID, title, "", "", labels, actor)
	if err != nil {
		t.Fatalf("RunAdd(%q, %q): %v", actor, title, err)
	}
	return res.ShortID
}

func mustClaim(t *testing.T, db *sql.DB, shortID, actor string) {
	t.Helper()
	if err := job.RunClaim(db, shortID, "30m", "", actor, false); err != nil {
		t.Fatalf("RunClaim(%q, %q): %v", shortID, actor, err)
	}
}

func taskIDForShortID(t *testing.T, db *sql.DB, shortID string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM tasks WHERE short_id = ?`, shortID).Scan(&id); err != nil {
		t.Fatalf("taskIDForShortID(%q): %v", shortID, err)
	}
	return id
}

// splitLogRows returns the raw HTML for each c-log-row in body. Each
// returned string starts at one row's opening tag and ends just
// before the next row's tag (or the end of the log container). Used
// by tests that need to assert behavior on a specific row without
// false positives from sibling rows.
func splitLogRows(body string) []string {
	const marker = `<div class="c-log-row c-log-row--`
	var out []string
	for {
		i := strings.Index(body, marker)
		if i < 0 {
			break
		}
		body = body[i:]
		j := strings.Index(body[len(marker):], marker)
		if j < 0 {
			out = append(out, body)
			break
		}
		out = append(out, body[:len(marker)+j])
		body = body[len(marker)+j:]
	}
	return out
}

func mustContain(t *testing.T, body, needle string) {
	t.Helper()
	if !strings.Contains(body, needle) {
		t.Errorf("missing %q in body\n---\n%s", needle, body)
	}
}

// stripInitialFrame returns the body with the time-travel JSON island
// removed, so substring checks for task titles only match what was
// rendered in the visible page — not what's serialized into the
// scrubber's hydration island. The island carries every non-deleted
// task by design (so reverse-fold works), which would otherwise make
// "this task should NOT appear" assertions spuriously fail.
func stripInitialFrame(body string) string {
	const open = `<script type="application/json" id="initial-frame">`
	start := strings.Index(body, open)
	if start < 0 {
		return body
	}
	end := strings.Index(body[start:], `</script>`)
	if end < 0 {
		return body
	}
	return body[:start] + body[start+end+len(`</script>`):]
}
