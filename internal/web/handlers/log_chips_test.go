package handlers_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

// extractChipGroup returns the HTML of one filter-bar group, picked by
// its aria-label ("Events", "Actor", "Label"). Chips are anchors, so
// the group ends at its first </div>.
func extractChipGroup(t *testing.T, body, ariaLabel string) string {
	t.Helper()
	marker := `aria-label="` + ariaLabel + `"`
	start := strings.Index(body, marker)
	if start == -1 {
		t.Fatalf("filter-bar group %q not found in body\n---\n%s", ariaLabel, body)
	}
	end := strings.Index(body[start:], `</div>`)
	if end == -1 {
		t.Fatalf("filter-bar group %q is not closed", ariaLabel)
	}
	return body[start : start+end]
}

// chipAnchors splits a group's HTML into one string per <a> chip.
func chipAnchors(group string) []string {
	parts := strings.Split(group, "<a ")
	out := make([]string, 0, len(parts))
	for _, p := range parts[1:] {
		if i := strings.Index(p, "</a>"); i >= 0 {
			p = p[:i]
		}
		out = append(out, p)
	}
	return out
}

// visibleChipCount counts the entity chips a reader actually sees.
// Entity chips carry data-actor-chip / data-label, which the leading
// "any" chip and the "+N more" chip do not.
func visibleChipCount(group string) int {
	n := 0
	for _, a := range chipAnchors(group) {
		if strings.Contains(a, "hidden") {
			continue
		}
		if strings.Contains(a, "data-actor-chip=") || strings.Contains(a, "data-label=") {
			n++
		}
	}
	return n
}

func hiddenChipCount(group string) int {
	n := 0
	for _, a := range chipAnchors(group) {
		if strings.Contains(a, "data-chip-overflow") {
			n++
		}
	}
	return n
}

// chipIndex reports the position of an actor's chip within the group,
// or -1. Used to assert most-recent-first ordering.
func chipIndex(group, actor string) int {
	for i, a := range chipAnchors(group) {
		if strings.Contains(a, `data-actor-chip="`+actor+`"`) {
			return i
		}
	}
	return -1
}

// seedActors creates n actors, oldest first, so recency order is the
// reverse of creation order.
func seedActors(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for i := range n {
		name := "agent-" + strconv.Itoa(i)
		mustAdd(t, db, name, "task-"+strconv.Itoa(i), nil, nil)
		// Stagger the timestamps so MAX(created_at) has a stable order;
		// a whole seed otherwise lands inside one second.
		if _, err := db.Exec(`UPDATE events SET created_at = ? WHERE actor = ?`,
			time.Now().Add(-time.Duration(n-i)*time.Minute).Unix(), name); err != nil {
			t.Fatalf("stagger %s: %v", name, err)
		}
	}
}

// --- the range bound on the strips ---

func TestLog_ActorChipsAreBoundedByTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 20*24*time.Hour)

	deps := newLogDeps(t, db)

	group := extractChipGroup(t, fetchLog(t, deps, ""), "Actor")
	mustContain(t, group, `data-actor-chip="fresh"`)
	if strings.Contains(group, `data-actor-chip="stale"`) {
		t.Errorf("a 20-day-old actor should be outside the default 7d window\n---\n%s", group)
	}

	all := extractChipGroup(t, fetchLog(t, deps, "range=all"), "Actor")
	mustContain(t, all, `data-actor-chip="fresh"`)
	mustContain(t, all, `data-actor-chip="stale"`)
}

func TestLog_ActorChipsAreOrderedByMostRecentEvent(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "oldest", "t1", nil, nil)
	mustAdd(t, db, "middle", "t2", nil, nil)
	mustAdd(t, db, "newest", "t3", nil, nil)
	backdateActorEvents(t, db, "oldest", 72*time.Hour)
	backdateActorEvents(t, db, "middle", 48*time.Hour)
	backdateActorEvents(t, db, "newest", 24*time.Hour)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, ""), "Actor")

	newest, middle, oldest := chipIndex(group, "newest"), chipIndex(group, "middle"), chipIndex(group, "oldest")
	if newest < 0 || middle < 0 || oldest < 0 {
		t.Fatalf("all three actors should have chips, got newest=%d middle=%d oldest=%d\n---\n%s",
			newest, middle, oldest, group)
	}
	if !(newest < middle && middle < oldest) {
		t.Errorf("actor chips should read most-recent-first, got newest=%d middle=%d oldest=%d",
			newest, middle, oldest)
	}
}

// --- the cap and the "+N more" chip ---

func TestLog_ActorChipsCapAtTwentyFourWithAMoreChip(t *testing.T) {
	db := setupLogTestDB(t)
	seedActors(t, db, 30)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, ""), "Actor")

	if n := visibleChipCount(group); n != 24 {
		t.Errorf("visible actor chips: got %d, want 24\n---\n%s", n, group)
	}
	if n := hiddenChipCount(group); n != 6 {
		t.Errorf("overflow actor chips: got %d, want 6", n)
	}
	mustContain(t, group, `data-chip-more`)
	mustContain(t, group, `+6 more`)
	mustContain(t, group, `chips=all`)
	// The most recent 24 are the ones kept; the oldest are the overflow.
	mustContain(t, group, `data-actor-chip="agent-29"`)
	for _, a := range chipAnchors(group) {
		if strings.Contains(a, `data-actor-chip="agent-0"`) && !strings.Contains(a, "hidden") {
			t.Errorf("agent-0 is the 30th most recent and should be in the overflow\n---\n%s", a)
		}
	}
}

func TestLog_ChipsAllRendersTheWholeStrip(t *testing.T) {
	db := setupLogTestDB(t)
	seedActors(t, db, 30)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, "chips=all"), "Actor")

	if n := visibleChipCount(group); n != 30 {
		t.Errorf("?chips=all visible actor chips: got %d, want 30", n)
	}
	if strings.Contains(group, "data-chip-more") {
		t.Errorf("?chips=all should not render a +N more chip\n---\n%s", group)
	}
	if strings.Contains(group, "data-chip-overflow") {
		t.Errorf("?chips=all should not hide any chip\n---\n%s", group)
	}
}

func TestLog_LabelChipsAreBoundedOrderedAndCapped(t *testing.T) {
	db := setupLogTestDB(t)
	stale := mustAdd(t, db, "alice", "stale-task", nil, []string{"stale-label"})
	mustAdd(t, db, "alice", "fresh-task", nil, []string{"fresh-label"})
	backdateActorTaskEvents(t, db, "alice", stale, 20*24*time.Hour)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, ""), "Label")
	mustContain(t, group, `data-label="fresh-label"`)
	if strings.Contains(group, `data-label="stale-label"`) {
		t.Errorf("a label whose only events are 20 days old is outside the default window\n---\n%s", group)
	}
	mustContain(t, extractChipGroup(t, fetchLog(t, deps, "range=all"), "Label"), `data-label="stale-label"`)
}

func TestLog_LabelChipsCapAtTwentyFour(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 26 {
		name := "l" + strconv.Itoa(i)
		mustAdd(t, db, "alice", "task-"+name, nil, []string{name})
	}

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, ""), "Label")

	if n := visibleChipCount(group); n != 24 {
		t.Errorf("visible label chips: got %d, want 24\n---\n%s", n, group)
	}
	mustContain(t, group, `+2 more`)
}

// --- the always-show-the-selection rule ---

func TestLog_SelectedActorChipSurvivesTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 20*24*time.Hour)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, "actor=stale"), "Actor")

	mustContain(t, group, `data-actor-chip="stale"`)
	for _, a := range chipAnchors(group) {
		if !strings.Contains(a, `data-actor-chip="stale"`) {
			continue
		}
		if strings.Contains(a, "hidden") {
			t.Errorf("the selected actor's chip must be visible, got %q", a)
		}
		if !strings.Contains(a, "c-filter-chip--active") {
			t.Errorf("the selected actor's chip must be marked active, got %q", a)
		}
	}
}

func TestLog_SelectedActorChipSurvivesTheCap(t *testing.T) {
	db := setupLogTestDB(t)
	seedActors(t, db, 30)

	deps := newLogDeps(t, db)
	// agent-0 is the 30th most recent — normally overflow.
	group := extractChipGroup(t, fetchLog(t, deps, "actor=agent-0"), "Actor")

	found := false
	for _, a := range chipAnchors(group) {
		if !strings.Contains(a, `data-actor-chip="agent-0"`) {
			continue
		}
		found = true
		if strings.Contains(a, "hidden") {
			t.Errorf("the selected actor's chip must stay visible past the cap, got %q", a)
		}
	}
	if !found {
		t.Errorf("no chip rendered for the selected actor\n---\n%s", group)
	}
}

func TestLog_SelectedLabelChipSurvivesTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	stale := mustAdd(t, db, "alice", "stale-task", nil, []string{"stale-label"})
	mustAdd(t, db, "alice", "fresh-task", nil, []string{"fresh-label"})
	backdateActorTaskEvents(t, db, "alice", stale, 20*24*time.Hour)

	deps := newLogDeps(t, db)
	group := extractChipGroup(t, fetchLog(t, deps, "label=stale-label"), "Label")
	mustContain(t, group, `data-label="stale-label"`)
	mustContain(t, group, `c-label-pill--active`)
}

func TestLog_SelectedTypeOutsideTheKnownListStillRenders(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, nil)

	deps := newLogDeps(t, db)
	// "labeled" is emitted but is not in knownEventTypes.
	group := extractChipGroup(t, fetchLog(t, deps, "type=labeled"), "Event type")

	mustContain(t, group, `>labeled<`)
	for _, a := range chipAnchors(group) {
		if strings.Contains(a, ">labeled<") && !strings.Contains(a, "c-filter-chip--active") {
			t.Errorf("the selected type chip must be marked active, got %q", a)
		}
	}
}

// --- the range bound on the event list itself ---

func TestLog_EventListIsBoundedByTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "fresh-task", nil, nil)
	stale := mustAdd(t, db, "alice", "stale-task", nil, nil)
	backdateActorTaskEvents(t, db, "alice", stale, 20*24*time.Hour)

	deps := newLogDeps(t, db)

	body := stripInitialFrame(fetchLog(t, deps, ""))
	mustContain(t, body, "fresh-task")
	if strings.Contains(body, "stale-task") {
		t.Errorf("a 20-day-old event should be outside the default 7d window")
	}
	mustContain(t, body, "showing 1 of 1 events")

	all := stripInitialFrame(fetchLog(t, deps, "range=all"))
	mustContain(t, all, "stale-task")
	mustContain(t, all, "showing 2 of 2 events")

	mid := stripInitialFrame(fetchLog(t, deps, "range=30d"))
	mustContain(t, mid, "stale-task")
}

func TestLog_PagingStaysInsideTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 4 {
		mustAdd(t, db, "alice", "task-"+strconv.Itoa(i), nil, nil)
	}
	stale := mustAdd(t, db, "alice", "stale-task", nil, nil)
	backdateActorTaskEvents(t, db, "alice", stale, 20*24*time.Hour)

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "limit=2"))

	if rows := strings.Count(body, `class="c-log-row__time"`); rows != 2 {
		t.Errorf("?limit=2 should render 2 rows, got %d", rows)
	}
	mustContain(t, body, `c-log-row--more`)
	// Paging carries the window forward so page 2 is not unbounded.
	page2 := stripInitialFrame(fetchLog(t, deps, "limit=2&range=30d&before=3"))
	mustContain(t, page2, `range=30d`)
}

// --- the selector itself ---

func TestLog_RangeTabsRenderInTheViewHeader(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	body := fetchLog(t, deps, "")
	mustContain(t, body, `<nav class="c-tabs" aria-label="Range">`)
	mustContain(t, body, `<a href="/log" class="c-tab c-tab--active" aria-current="true">7D</a>`)
	mustContain(t, body, `<a href="/log?range=14d" class="c-tab">14D</a>`)
	mustContain(t, body, `<a href="/log?range=30d" class="c-tab">30D</a>`)
	mustContain(t, body, `<a href="/log?range=all" class="c-tab">All</a>`)

	all := fetchLog(t, deps, "range=all")
	mustContain(t, all, `<a href="/log?range=all" class="c-tab c-tab--active" aria-current="true">All</a>`)
	if n := strings.Count(all, `aria-label="Range"`); n != 1 {
		t.Errorf("exactly one range nav expected, got %d", n)
	}
}

func TestLog_RangeTabsKeepTheOtherFilters(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchLog(t, deps, "actor=alice")
	mustContain(t, body, `href="/log?actor=alice&amp;range=all"`)
}

func TestLog_ChipHrefsPreserveTheRange(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, []string{"web"})
	deps := newLogDeps(t, db)

	body := fetchLog(t, deps, "range=30d")
	mustContain(t, body, `/log?actor=alice&amp;range=30d`)
	mustContain(t, body, `/log?label=web&amp;range=30d`)
	mustContain(t, body, `/log?range=30d&amp;type=claimed`)
}

func TestLog_InvalidRangeFallsBackToSevenDays(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "fresh", "fresh-task", nil, nil)
	mustAdd(t, db, "stale", "stale-task", nil, nil)
	backdateActorEvents(t, db, "stale", 20*24*time.Hour)

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "range=90d")

	mustContain(t, body, `<a href="/log" class="c-tab c-tab--active" aria-current="true">7D</a>`)
	if strings.Contains(extractChipGroup(t, body, "Actor"), `data-actor-chip="stale"`) {
		t.Errorf("an unrecognized range should fall back to 7d, not to all")
	}
}

// TestLog_RangeIsMeasuredFromTheScrubberCursor pins the interaction
// with ?at=: parked in history, the window is measured back from the
// cursor's own event, not from wall-clock now.
func TestLog_RangeIsMeasuredFromTheScrubberCursor(t *testing.T) {
	db := setupLogTestDB(t)
	old := mustAdd(t, db, "alice", "old-task", nil, nil)
	backdateActorTaskEvents(t, db, "alice", old, 20*24*time.Hour)
	mustAdd(t, db, "alice", "new-task", nil, nil)

	var oldEventID int64
	if err := db.QueryRow(
		`SELECT e.id FROM events e JOIN tasks t ON t.id = e.task_id WHERE t.short_id = ?`,
		old).Scan(&oldEventID); err != nil {
		t.Fatalf("old event id: %v", err)
	}

	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchLog(t, deps, "at="+strconv.FormatInt(oldEventID, 10)))

	// The cursor sits on the 20-day-old event; a 7-day window measured
	// back from *there* contains it.
	mustContain(t, body, "old-task")
}
