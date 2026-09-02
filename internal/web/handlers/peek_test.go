package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

var _ = httptest.NewRequest // imported for plan test below

func mustBlock(t *testing.T, db *sql.DB, blocked, blocker string) error {
	t.Helper()
	return job.RunBlock(db, blocked, blocker, "alice")
}

func mustContainAll(t *testing.T, body string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(body, n) {
			t.Errorf("missing %q in body\n---\n%s", n, body)
		}
	}
}

func fetchPeek(t *testing.T, deps handlers.Deps, shortID string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/tasks/"+shortID+"/peek", nil)
	req.SetPathValue("id", shortID)
	w := httptest.NewRecorder()
	handlers.Peek(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String(), w.Header().Get("Content-Type")
}

func mustFetchPeek(t *testing.T, deps handlers.Deps, shortID string) string {
	t.Helper()
	code, body, _ := fetchPeek(t, deps, shortID)
	if code != 200 {
		t.Fatalf("GET /tasks/%s/peek: status %d, body=%s", shortID, code, body)
	}
	return body
}

func TestPeek_RendersWithoutLayoutChrome(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	// Fragment — no <html>, <head>, page header, footer, or scripts.
	for _, banned := range []string{
		"<!doctype html>",
		"<!DOCTYPE html>",
		`<html`,
		`<head`,
		`class="c-header"`,
		`class="c-footer"`,
		`<script`,
		`<live-region`,
	} {
		if strings.Contains(body, banned) {
			t.Errorf("fragment must not contain %q\n---\n%s", banned, body)
		}
	}
}

func TestPeek_HasPeekSheetWrapper(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `c-peek-sheet`)
	mustContain(t, body, `role="complementary"`)
}

func TestPeek_ContentTypeIsHTML(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	_, _, ct := fetchPeek(t, deps, id)
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html…", ct)
	}
}

func TestPeek_ShowsIDPillTitleAndStatusPill(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `>`+id+`<`)
	mustContain(t, body, `alice-task`)
	mustContain(t, body, `c-status-pill`)
}

func TestPeek_ShowsLabels(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "labeled-task", nil, []string{"web", "peek"})

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `data-label="web"`)
	mustContain(t, body, `data-label="peek"`)
}

func TestPeek_ShowsParentLink(t *testing.T) {
	db := setupLogTestDB(t)
	parent := mustAdd(t, db, "alice", "Parent task", nil, nil)
	child := mustAdd(t, db, "alice", "Child task", &parent, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, child)

	mustContain(t, body, `/tasks/`+parent)
	mustContain(t, body, `Parent task`)
}

func TestPeek_ShowsBlockedByAndBlocking(t *testing.T) {
	db := setupLogTestDB(t)
	subject := mustAdd(t, db, "alice", "Subject", nil, nil)
	blocker := mustAdd(t, db, "alice", "Blocker task", nil, nil)
	blocking := mustAdd(t, db, "alice", "Downstream task", nil, nil)
	if err := mustBlock(t, db, subject, blocker); err != nil {
		t.Fatalf("block subject: %v", err)
	}
	if err := mustBlock(t, db, blocking, subject); err != nil {
		t.Fatalf("block downstream: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, subject)

	mustContain(t, body, `Blocked by`)
	mustContain(t, body, `Blocker task`)
	mustContain(t, body, `Blocks`)
	mustContain(t, body, `Downstream task`)
}

func TestPeek_ShowsHistory(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `claimed by`)
	mustContain(t, body, `added by`)
	mustContain(t, body, `<strong>alice</strong>`)
}

func TestPeek_OpenFullPageLink(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `href="/tasks/`+id+`"`)
	mustContain(t, body, `Open full page`)
}

func TestPeek_HasCloseButton(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `c-peek-sheet__close`)
}

// --- section presence / absence --------------------------------------

func TestPeek_DescriptionSectionRendersWhenPresent(t *testing.T) {
	db := setupLogTestDB(t)
	if _, err := job.RunAdd(db, "", "alice-task", "An interesting description", "", nil, "alice"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	var shortID string
	if err := db.QueryRow(`SELECT short_id FROM tasks WHERE title = 'alice-task'`).Scan(&shortID); err != nil {
		t.Fatalf("scan short_id: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, shortID)

	mustContain(t, body, `>Description<`)
	mustContain(t, body, `An interesting description`)
}

func TestPeek_DescriptionSectionAbsentWhenEmpty(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	if strings.Contains(body, `>Description<`) {
		t.Errorf("empty description should not render its section")
	}
}

func TestPeek_CriteriaSection_RendersFourStatesAndOmittedWhenZero(t *testing.T) {
	db := setupLogTestDB(t)
	idEmpty := mustAdd(t, db, "alice", "no criteria", nil, nil)
	idFull := mustAdd(t, db, "alice", "has criteria", nil, nil)
	if _, err := job.RunAddCriteria(db, idFull, []job.Criterion{
		{Label: "p"},
		{Label: "q"},
		{Label: "r"},
		{Label: "s"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, idFull, "q", job.CriterionPassed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	if _, err := job.RunSetCriterion(db, idFull, "r", job.CriterionSkipped, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	if _, err := job.RunSetCriterion(db, idFull, "s", job.CriterionFailed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	deps := newLogDeps(t, db)
	bodyEmpty := mustFetchPeek(t, deps, idEmpty)
	if strings.Contains(bodyEmpty, ">Criteria<") {
		t.Errorf("peek should omit Criteria section when zero criteria")
	}

	body := mustFetchPeek(t, deps, idFull)
	mustContain(t, body, ">Criteria<")
	mustContainAll(t, body,
		`data-criterion-state="pending"`,
		`data-criterion-state="passed"`,
		`data-criterion-state="skipped"`,
		`data-criterion-state="failed"`,
	)
}

func TestPeek_NotesSectionRendersWhenTaskCompletedWithNote(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	if _, _, err := job.RunDone(db, []string{id}, false, "All wrapped up neatly.", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `>Completion note<`)
	mustContain(t, body, `All wrapped up neatly.`)
}

func TestPeek_NotesSectionAbsentWhenNoCompletionNote(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	if strings.Contains(body, `>Completion note<`) {
		t.Errorf("task without a completion note should not render the Completion note section")
	}
}

func TestPeek_ClaimedBySectionRendersWhenClaimed(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `>Claimed by<`)
	mustContain(t, body, `href="/actors/alice"`)
}

func TestPeek_ClaimedBySectionAbsentWhenUnclaimed(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	if strings.Contains(body, `>Claimed by<`) {
		t.Errorf("unclaimed task should not render the Claimed by section")
	}
}

func TestPeek_LabelsSectionAbsentWhenNoLabels(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	if strings.Contains(body, `>Labels<`) {
		t.Errorf("task without labels should not render the Labels section")
	}
}

func TestPeek_StatusPillReflectsTaskState(t *testing.T) {
	db := setupLogTestDB(t)

	tests := []struct {
		name     string
		setup    func(t *testing.T, db *sql.DB) string
		wantPill string
	}{
		{
			name: "todo",
			setup: func(t *testing.T, db *sql.DB) string {
				return mustAdd(t, db, "alice", "todo-task", nil, nil)
			},
			wantPill: "c-status-pill--todo",
		},
		{
			name: "active",
			setup: func(t *testing.T, db *sql.DB) string {
				id := mustAdd(t, db, "alice", "active-task", nil, nil)
				mustClaim(t, db, id, "alice")
				return id
			},
			wantPill: "c-status-pill--active",
		},
		{
			name: "done",
			setup: func(t *testing.T, db *sql.DB) string {
				id := mustAdd(t, db, "alice", "done-task", nil, nil)
				if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
					t.Fatalf("RunDone: %v", err)
				}
				return id
			},
			wantPill: "c-status-pill--done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := tt.setup(t, db)
			deps := newLogDeps(t, db)
			body := mustFetchPeek(t, deps, id)
			mustContain(t, body, tt.wantPill)
		})
	}
}

func TestPeek_HistoryEmptyStatePresent(t *testing.T) {
	db := setupLogTestDB(t)
	// Insert a task by direct INSERT so it has zero events. mustAdd
	// always emits the `created` event.
	if _, err := db.Exec(`
		INSERT INTO tasks (short_id, title, description, status, sort_key, created_at, updated_at)
		VALUES ('eventless', 'Quiet task', '', 'available', 0, ?, ?)
	`, 0, 0); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, "eventless")

	mustContain(t, body, `No events yet.`)
}

// --- peek-to-peek navigation -----------------------------------------

func TestPeek_ParentIDPillOptsInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	parent := mustAdd(t, db, "alice", "Parent", nil, nil)
	child := mustAdd(t, db, "alice", "Child", &parent, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, child)

	if !strings.Contains(body, `data-peek class="c-id-pill"`) &&
		!strings.Contains(body, `class="c-id-pill" data-peek`) {
		t.Errorf("parent id pill in peek should opt in to peek-to-peek navigation\n%s", body)
	}
}

func TestPeek_BlockerIDPillsOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	subject := mustAdd(t, db, "alice", "Subject", nil, nil)
	blocker := mustAdd(t, db, "alice", "Blocker", nil, nil)
	if err := mustBlock(t, db, subject, blocker); err != nil {
		t.Fatalf("block: %v", err)
	}

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, subject)

	// At least one peek-eligible id pill in the Blocked-by row.
	count := strings.Count(body, `data-peek class="c-id-pill"`) +
		strings.Count(body, `class="c-id-pill" data-peek`)
	if count < 1 {
		t.Errorf("blocker id pills should opt in to peek\n%s", body)
	}
}

// --- error fragment ---------------------------------------------------

func TestPeek_ErrorFragmentHasCloseButton(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	code, body, _ := fetchPeek(t, deps, "ghostX")
	if code != 404 {
		t.Fatalf("status: got %d, want 404", code)
	}
	mustContainAll(t, body,
		`c-peek-sheet--error`,
		`c-peek-sheet__close`,
		`data-peek-close`,
	)
}

func TestPeek_SoftDeletedTaskReturns404(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "going-away", nil, nil)
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = ? WHERE short_id = ?`, 1, id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	deps := newLogDeps(t, db)
	code, _, _ := fetchPeek(t, deps, id)
	if code != 404 {
		t.Errorf("soft-deleted task should peek-404; got %d", code)
	}
}

func TestPeek_BellCarriesTaskID(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `data-task-bell="`+id+`"`)
	mustContain(t, body, `aria-pressed="false"`)
}

func TestTaskPage_HasNotificationBellWithTaskID(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/tasks/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	handlers.Task(deps).ServeHTTP(w, req)
	body := w.Body.String()

	mustContain(t, body, `data-task-bell="`+id+`"`)
}

// --- peek opt-in across views ----------------------------------------

func TestHome_RowLinksOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)
	mustClaim(t, db, id, "alice")

	deps := newLogDeps(t, db)
	body := fetchHome(t, deps)

	// Every row link the home view emits should opt in via data-peek
	// so the click delegator can intercept it.
	rows := strings.Count(body, `class="c-row-link"`)
	peekable := strings.Count(body, `data-peek class="c-row-link"`) +
		strings.Count(body, `class="c-row-link" data-peek`)
	if rows == 0 || peekable != rows {
		t.Errorf("home row-links: got %d total, %d peekable; want every row link to carry data-peek", rows, peekable)
	}
}

func TestActors_RowLinksOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchActors(t, deps)

	rows := strings.Count(body, `class="c-row-link"`)
	peekable := strings.Count(body, `data-peek class="c-row-link"`) +
		strings.Count(body, `class="c-row-link" data-peek`)
	if rows == 0 || peekable != rows {
		t.Errorf("actors row-links: got %d total, %d peekable", rows, peekable)
	}
}

func TestActorSingle_RowLinksOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchActorSingle(t, deps, "alice")

	rows := strings.Count(body, `class="c-row-link"`)
	peekable := strings.Count(body, `data-peek class="c-row-link"`) +
		strings.Count(body, `class="c-row-link" data-peek`)
	if rows == 0 || peekable != rows {
		t.Errorf("actor-single row-links: got %d total, %d peekable", rows, peekable)
	}
}

func TestLog_RowLinksOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")

	rows := strings.Count(body, `class="c-row-link"`)
	peekable := strings.Count(body, `data-peek class="c-row-link"`) +
		strings.Count(body, `class="c-row-link" data-peek`)
	if rows == 0 || peekable != rows {
		t.Errorf("log row-links: got %d total, %d peekable", rows, peekable)
	}
}

func TestPlan_IDPillTaskLinksOptInToPeek(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/plan", nil)
	w := httptest.NewRecorder()
	handlers.Plan(deps).ServeHTTP(w, req)
	body := w.Body.String()

	// Every plan-row id pill that links to /tasks/<id> should opt in.
	if !strings.Contains(body, `data-peek class="c-id-pill"`) &&
		!strings.Contains(body, `class="c-id-pill" data-peek`) {
		t.Errorf("plan row id pills should carry data-peek\n%s", body)
	}
}

func TestPeek_OpenFullPageLinkDoesNotPeek(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "alice-task", nil, nil)

	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	// The "Open full page" CTA inside the peek fragment should hard-
	// navigate, not re-trigger the peek delegator.
	cta := `<a href="/tasks/` + id + `" class="c-button c-button--primary">Open full page</a>`
	if !strings.Contains(body, cta) {
		t.Errorf("Open full page CTA should not carry data-peek; got body:\n%s", body)
	}
}

func TestPeek_404ForUnknownID(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)

	code, body, _ := fetchPeek(t, deps, "ghostX")
	if code != 404 {
		t.Errorf("status: got %d, want 404", code)
	}
	// Even on 404 we should not return a full page — peek is a
	// fragment endpoint, so the error response must stay fragment-
	// shaped (no <html>) so the client can drop it into the sheet
	// without doubled chrome.
	if strings.Contains(body, "<html") {
		t.Errorf("404 should be a fragment, not a full page:\n%s", body)
	}
}
