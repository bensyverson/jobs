package job

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupSearchTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "search.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func searchMustAdd(t *testing.T, db *sql.DB, actor, title, desc string, parent *string, labels []string) string {
	t.Helper()
	parentID := ""
	if parent != nil {
		parentID = *parent
	}
	res, err := RunAdd(db, parentID, title, desc, "", labels, actor)
	if err != nil {
		t.Fatalf("RunAdd(%q): %v", title, err)
	}
	return res.ShortID
}

func searchMustNote(t *testing.T, db *sql.DB, shortID, text, actor string) {
	t.Helper()
	if err := RunNote(db, shortID, text, nil, actor); err != nil {
		t.Fatalf("RunNote(%q): %v", shortID, err)
	}
}

func searchMustClaim(t *testing.T, db *sql.DB, shortID, actor string) {
	t.Helper()
	if err := RunClaim(db, shortID, "", "", actor, false); err != nil {
		t.Fatalf("RunClaim(%q): %v", shortID, err)
	}
}

func searchMustDone(t *testing.T, db *sql.DB, shortID, actor string) {
	t.Helper()
	if _, _, err := RunDone(db, []string{shortID}, false, "", nil, actor, false, ""); err != nil {
		t.Fatalf("RunDone(%q): %v", shortID, err)
	}
}

// pickTask returns the first hit with Kind == "task". Fails the test if none.
func pickTask(t *testing.T, hits []SearchHit) SearchHit {
	t.Helper()
	for _, h := range hits {
		if h.Kind == "task" {
			return h
		}
	}
	t.Fatalf("no task hit in %+v", hits)
	return SearchHit{}
}

func TestRunSearch_MatchesByShortID(t *testing.T) {
	db := setupSearchTestDB(t)
	id := searchMustAdd(t, db, "alice", "Some task", "", nil, nil)

	hits, err := RunSearch(db, id, 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.Kind != "task" {
		t.Errorf("Kind = %q, want %q", h.Kind, "task")
	}
	if h.ShortID != id {
		t.Errorf("ShortID = %q, want %q", h.ShortID, id)
	}
	if h.MatchSource != "short_id" {
		t.Errorf("MatchSource = %q, want %q", h.MatchSource, "short_id")
	}
}

func TestRunSearch_MatchesByTitle(t *testing.T) {
	db := setupSearchTestDB(t)
	searchMustAdd(t, db, "alice", "Dashboard polish", "", nil, nil)

	hits, err := RunSearch(db, "polish", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.Kind != "task" || h.MatchSource != "title" {
		t.Errorf("got Kind=%q MatchSource=%q, want task/title", h.Kind, h.MatchSource)
	}
	if h.Excerpt != "" {
		t.Errorf("Excerpt should be empty for title match, got %q", h.Excerpt)
	}
}

func TestRunSearch_MatchesByDescription(t *testing.T) {
	db := setupSearchTestDB(t)
	searchMustAdd(t, db, "alice", "Task", "Build the search index now", nil, nil)

	hits, err := RunSearch(db, "index", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.Kind != "task" || h.MatchSource != "description" {
		t.Errorf("got Kind=%q MatchSource=%q, want task/description", h.Kind, h.MatchSource)
	}
	if !strings.Contains(strings.ToLower(h.Excerpt), "index") {
		t.Errorf("Excerpt = %q, want it to contain 'index'", h.Excerpt)
	}
}

// Notes live in the event log only (no longer appended to tasks.description),
// so a note-only match surfaces as MatchSource="note" with the excerpt drawn
// from the note body.
func TestRunSearch_MatchesByNote(t *testing.T) {
	db := setupSearchTestDB(t)
	id := searchMustAdd(t, db, "alice", "Task", "", nil, nil)
	searchMustNote(t, db, id, "Remember to update docs soon", "alice")

	hits, err := RunSearch(db, "docs", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	h := hits[0]
	if h.Kind != "task" || h.MatchSource != "note" {
		t.Errorf("got Kind=%q MatchSource=%q, want task/note", h.Kind, h.MatchSource)
	}
	if h.ShortID != id {
		t.Errorf("ShortID = %q, want %q", h.ShortID, id)
	}
	if !strings.Contains(strings.ToLower(h.Excerpt), "docs") {
		t.Errorf("Excerpt = %q, want it to contain 'docs'", h.Excerpt)
	}
}

// Notes rank just after description: a description hit on one task and a
// note-only hit on another both match, and the description hit is returned
// first.
func TestRunSearch_NoteRanksAfterDescription(t *testing.T) {
	db := setupSearchTestDB(t)
	descHit := searchMustAdd(t, db, "alice", "DescTask", "wombat in description", nil, nil)
	noteHit := searchMustAdd(t, db, "alice", "NoteTask", "", nil, nil)
	searchMustNote(t, db, noteHit, "wombat in note", "alice")

	hits, err := RunSearch(db, "wombat", 20)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var taskShortIDs []string
	for _, h := range hits {
		if h.Kind == "task" {
			taskShortIDs = append(taskShortIDs, h.ShortID)
		}
	}
	if len(taskShortIDs) != 2 {
		t.Fatalf("expected 2 task hits, got %d (%v)", len(taskShortIDs), taskShortIDs)
	}
	if taskShortIDs[0] != descHit {
		t.Errorf("rank[0] = %q, want description hit %q", taskShortIDs[0], descHit)
	}
	if taskShortIDs[1] != noteHit {
		t.Errorf("rank[1] = %q, want note hit %q", taskShortIDs[1], noteHit)
	}
	var taskSources []string
	for _, h := range hits {
		if h.Kind == "task" {
			taskSources = append(taskSources, h.MatchSource)
		}
	}
	if len(taskSources) != 2 || taskSources[0] != "description" || taskSources[1] != "note" {
		t.Errorf("MatchSource sequence = %v, want [description note]", taskSources)
	}
}

// Updated under the new design: a label-name match returns a label hit (the
// label itself), not a task. Selecting the label routes to /labels/<name>.
func TestRunSearch_MatchesByLabel(t *testing.T) {
	db := setupSearchTestDB(t)
	searchMustAdd(t, db, "alice", "Some unrelated title", "", nil, []string{"search"})

	hits, err := RunSearch(db, "search", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	// Expect at least one label hit; the task title doesn't match "search".
	var foundLabel bool
	for _, h := range hits {
		if h.Kind == "label" && h.Name == "search" {
			foundLabel = true
		}
		if h.Kind == "task" {
			t.Errorf("did not expect a task hit; got %+v", h)
		}
	}
	if !foundLabel {
		t.Fatalf("expected a label hit for 'search', got %+v", hits)
	}
}

func TestRunSearch_LabelDedup(t *testing.T) {
	db := setupSearchTestDB(t)
	searchMustAdd(t, db, "alice", "T1", "", nil, []string{"search"})
	searchMustAdd(t, db, "alice", "T2", "", nil, []string{"search"})
	searchMustAdd(t, db, "alice", "T3", "", nil, []string{"search"})

	hits, err := RunSearch(db, "search", 20)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var labelCount int
	for _, h := range hits {
		if h.Kind == "label" && h.Name == "search" {
			labelCount++
		}
	}
	if labelCount != 1 {
		t.Errorf("expected 1 deduped label hit, got %d", labelCount)
	}
}

func TestRunSearch_LabelCap(t *testing.T) {
	db := setupSearchTestDB(t)
	for i := range 7 {
		searchMustAdd(t, db, "alice", "T"+string(rune('A'+i)), "",
			nil, []string{"foo" + string(rune('A'+i))})
	}

	hits, err := RunSearch(db, "foo", 50)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var labelCount int
	for _, h := range hits {
		if h.Kind == "label" {
			labelCount++
		}
	}
	if labelCount > 5 {
		t.Errorf("expected at most 5 label hits, got %d", labelCount)
	}
	if labelCount < 5 {
		t.Errorf("expected exactly 5 label hits when 7 distinct labels match, got %d", labelCount)
	}
}

func TestRunSearch_RankOrder(t *testing.T) {
	db := setupSearchTestDB(t)
	titleHit := searchMustAdd(t, db, "alice", "wombat title", "", nil, nil)
	descHit := searchMustAdd(t, db, "alice", "Other", "wombat in description", nil, nil)

	hits, err := RunSearch(db, "wombat", 20)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var taskShortIDs []string
	for _, h := range hits {
		if h.Kind == "task" {
			taskShortIDs = append(taskShortIDs, h.ShortID)
		}
	}
	if len(taskShortIDs) != 2 {
		t.Fatalf("expected 2 task hits, got %d (%v)", len(taskShortIDs), taskShortIDs)
	}
	if taskShortIDs[0] != titleHit {
		t.Errorf("rank[0] = %q, want title hit %q", taskShortIDs[0], titleHit)
	}
	if taskShortIDs[1] != descHit {
		t.Errorf("rank[1] = %q, want description hit %q", taskShortIDs[1], descHit)
	}
}

func TestRunSearch_ExactShortIDRanksFirst(t *testing.T) {
	db := setupSearchTestDB(t)
	exactID := searchMustAdd(t, db, "alice", "Plain title", "", nil, nil)
	// Another task whose title contains the first task's short_id.
	searchMustAdd(t, db, "alice", "References "+exactID+" inline", "", nil, nil)

	hits, err := RunSearch(db, exactID, 20)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) < 2 {
		t.Fatalf("expected at least 2 hits, got %d (%+v)", len(hits), hits)
	}
	if hits[0].Kind != "task" || hits[0].ShortID != exactID {
		t.Errorf("first hit = %+v, want exact short_id %q", hits[0], exactID)
	}
	if hits[0].MatchSource != "short_id" {
		t.Errorf("MatchSource = %q, want short_id", hits[0].MatchSource)
	}
}

func TestRunSearch_ExcerptElision(t *testing.T) {
	db := setupSearchTestDB(t)
	long := strings.Repeat("alpha ", 30) + "needle " + strings.Repeat("omega ", 30)
	searchMustAdd(t, db, "alice", "T", long, nil, nil)

	hits, err := RunSearch(db, "needle", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	h := pickTask(t, hits)
	if !strings.HasPrefix(h.Excerpt, "…") {
		t.Errorf("Excerpt should start with '…' when text elided on left: %q", h.Excerpt)
	}
	if !strings.HasSuffix(h.Excerpt, "…") {
		t.Errorf("Excerpt should end with '…' when text elided on right: %q", h.Excerpt)
	}
	if !strings.Contains(h.Excerpt, "needle") {
		t.Errorf("Excerpt missing 'needle': %q", h.Excerpt)
	}
}

// Updated under the new design: searching "search" against a task that
// matches via title AND desc AND note AND label produces 1 task hit (deduped)
// plus 1 label hit (the label "search" itself).
func TestRunSearch_Deduplicates(t *testing.T) {
	db := setupSearchTestDB(t)
	id := searchMustAdd(t, db, "alice", "Search task", "search desc", nil, []string{"search"})
	searchMustNote(t, db, id, "search note", "alice")

	hits, err := RunSearch(db, "search", 20)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var taskHits, labelHits int
	for _, h := range hits {
		switch h.Kind {
		case "task":
			taskHits++
		case "label":
			labelHits++
		}
	}
	if taskHits != 1 {
		t.Errorf("expected 1 deduped task hit, got %d", taskHits)
	}
	if labelHits != 1 {
		t.Errorf("expected 1 label hit, got %d", labelHits)
	}
}

func TestRunSearch_PrioritySort(t *testing.T) {
	db := setupSearchTestDB(t)
	claimed := searchMustAdd(t, db, "alice", "Claimed task", "", nil, nil)
	_ = searchMustAdd(t, db, "alice", "Available task", "", nil, nil)
	done := searchMustAdd(t, db, "alice", "Done task", "", nil, nil)
	canceled := searchMustAdd(t, db, "alice", "Canceled task", "", nil, nil)

	searchMustClaim(t, db, claimed, "alice")
	searchMustDone(t, db, done, "alice")
	if _, _, _, err := RunCancel(db, []string{canceled}, "test", false, false, false, "alice"); err != nil {
		t.Fatalf("RunCancel: %v", err)
	}

	hits, err := RunSearch(db, "task", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	var statuses []string
	for _, h := range hits {
		if h.Kind == "task" {
			statuses = append(statuses, h.Status)
		}
	}
	want := []string{"claimed", "available", "done", "canceled"}
	if len(statuses) != len(want) {
		t.Fatalf("expected %d task hits, got %d (%v)", len(want), len(statuses), statuses)
	}
	for i, s := range want {
		if statuses[i] != s {
			t.Errorf("status[%d] = %q, want %q", i, statuses[i], s)
		}
	}
}

func TestRunSearch_Limit(t *testing.T) {
	db := setupSearchTestDB(t)
	for i := range 5 {
		searchMustAdd(t, db, "alice", "Task "+string(rune('A'+i)), "", nil, nil)
	}

	hits, err := RunSearch(db, "Task", 3)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits (limit), got %d", len(hits))
	}
}

func TestRunSearch_EmptyQuery(t *testing.T) {
	db := setupSearchTestDB(t)
	searchMustAdd(t, db, "alice", "Task", "", nil, nil)

	hits, err := RunSearch(db, "", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for empty query, got %d", len(hits))
	}

	hits, err = RunSearch(db, "   ", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for whitespace query, got %d", len(hits))
	}
}

func TestRunSearch_ExcludesDeleted(t *testing.T) {
	db := setupSearchTestDB(t)
	id := searchMustAdd(t, db, "alice", "Gone", "", nil, nil)
	now := time.Now()
	if _, err := db.Exec(`UPDATE tasks SET deleted_at = ? WHERE short_id = ?`, now.Unix(), id); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	hits, err := RunSearch(db, "Gone", 10)
	if err != nil {
		t.Fatalf("RunSearch: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for deleted task, got %d", len(hits))
	}
}
