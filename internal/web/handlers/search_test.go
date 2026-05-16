package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

func fetchSearch(t *testing.T, deps handlers.Deps, query string) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/search?"+query, nil)
	w := httptest.NewRecorder()
	handlers.Search(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /search?%s: status %d, body=%s", query, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func mustNote(t *testing.T, db *sql.DB, shortID, text, actor string) {
	t.Helper()
	if err := job.RunNote(db, shortID, text, nil, actor); err != nil {
		t.Fatalf("RunNote(%q): %v", shortID, err)
	}
}

func TestSearch_MatchesByTitle(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Dashboard search", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=search")
	if !strings.Contains(body, "Dashboard search") {
		t.Errorf("expected search result for 'Dashboard search' in %s", body)
	}
}

func TestSearch_MatchesByShortID(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "Find me", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q="+id)
	if !strings.Contains(body, "Find me") {
		t.Errorf("expected search result by short_id")
	}
	if !strings.Contains(body, `"short_id":"`+id+`"`) {
		t.Errorf("expected JSON short_id field")
	}
}

func TestSearch_MatchesByNote(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "Task", nil, nil)
	mustNote(t, db, id, "Remember the milk", "alice")
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=milk")
	if !strings.Contains(body, "Task") {
		t.Errorf("expected search result from note text")
	}
}

// Updated under the new design: label-name matches return a label hit (the
// label itself), which routes the user to /labels/<name>.
func TestSearch_MatchesByLabel(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Unrelated", nil, []string{"searchable"})
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=searchable")
	if !strings.Contains(body, `"kind":"label"`) {
		t.Errorf("expected a label-kind result: %s", body)
	}
	if !strings.Contains(body, `"name":"searchable"`) {
		t.Errorf("expected label name 'searchable': %s", body)
	}
	if !strings.Contains(body, `"url":"/labels/searchable"`) {
		t.Errorf("expected label url '/labels/searchable': %s", body)
	}
}

func TestSearch_TaskResultShape(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Polish UI", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=Polish")
	for _, want := range []string{
		`"kind":"task"`,
		`"title":"Polish UI"`,
		`"match_source":"title"`,
		`"url":"/tasks/`,
		`"display_status":`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body: %s", want, body)
		}
	}
}

func TestSearch_DescriptionMatchHasExcerpt(t *testing.T) {
	db := setupLogTestDB(t)
	mustAddWithDesc(t, db, "alice", "T", "the quick brown fox jumps over", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=quick")
	if !strings.Contains(body, `"match_source":"description"`) {
		t.Errorf("expected match_source description: %s", body)
	}
	if !strings.Contains(body, `"excerpt":`) {
		t.Errorf("expected excerpt field: %s", body)
	}
	if !strings.Contains(body, `quick`) {
		t.Errorf("expected excerpt to contain match: %s", body)
	}
}

func TestSearch_PrioritySort(t *testing.T) {
	db := setupLogTestDB(t)
	claimed := mustAdd(t, db, "alice", "Claimed", nil, nil)
	_ = mustAdd(t, db, "alice", "Available", nil, nil)
	done := mustAdd(t, db, "alice", "Done", nil, nil)
	canceled := mustAdd(t, db, "alice", "Canceled", nil, nil)

	if err := job.RunClaim(db, claimed, "", "", "alice", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{done}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	if _, _, _, err := job.RunCancel(db, []string{canceled}, "test", false, false, false, "alice"); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	deps := newLogDeps(t, db)
	body := fetchSearch(t, deps, "q=e")

	// Status order in JSON should be claimed, available, done, canceled.
	claimedIdx := strings.Index(body, `"status":"claimed"`)
	availableIdx := strings.Index(body, `"status":"available"`)
	doneIdx := strings.Index(body, `"status":"done"`)
	canceledIdx := strings.Index(body, `"status":"canceled"`)

	if claimedIdx == -1 || availableIdx == -1 || doneIdx == -1 || canceledIdx == -1 {
		t.Fatalf("missing statuses in response: %s", body)
	}
	if !(claimedIdx < availableIdx && availableIdx < doneIdx && doneIdx < canceledIdx) {
		t.Errorf("priority sort wrong: claimed=%d available=%d done=%d canceled=%d",
			claimedIdx, availableIdx, doneIdx, canceledIdx)
	}
}

func TestSearch_Limit(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 5 {
		mustAdd(t, db, "alice", "Task "+string(rune('A'+i)), nil, nil)
	}
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=Task&limit=2")
	// Count short_id occurrences in JSON array.
	count := strings.Count(body, `"short_id"`)
	if count != 2 {
		t.Errorf("expected 2 results (limit), got %d", count)
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	db := setupLogTestDB(t)
	for i := range 25 {
		mustAdd(t, db, "alice", "Task "+string(rune('A'+i)), nil, nil)
	}
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=Task")
	count := strings.Count(body, `"short_id"`)
	if count != 20 {
		t.Errorf("expected default limit 20, got %d", count)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=")
	if body != "[]\n" && body != "[]" {
		t.Errorf("expected empty JSON array, got %q", body)
	}
}

func TestSearch_NoMatch(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "Task", nil, nil)
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=xyznonexistent")
	if body != "[]\n" && body != "[]" {
		t.Errorf("expected empty JSON array, got %q", body)
	}
}

func TestSearch_DisplayStatusBlocked(t *testing.T) {
	db := setupLogTestDB(t)
	blocker := mustAdd(t, db, "alice", "Blocker", nil, nil)
	blocked := mustAdd(t, db, "alice", "Blocked", nil, nil)
	if err := job.RunBlock(db, blocked, blocker, "alice"); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	deps := newLogDeps(t, db)

	body := fetchSearch(t, deps, "q=Blocked")
	if !strings.Contains(body, `"display_status":"blocked"`) {
		t.Errorf("expected display_status blocked, got %s", body)
	}
}
