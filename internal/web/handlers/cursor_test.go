package handlers_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// The scrubber addresses history as ?at=<log position>. A rebuild renumbers
// events.id, so a row-id cursor lands on a different event after any pull;
// the position does not move.

// dropForeignLog writes a one-event log file for another replica, dated
// earlier than every local event so the rebuild inserts it at the front.
func dropForeignLog(t *testing.T, cache, rep, shortID string) {
	t.Helper()
	payload, err := json.Marshal(job.CreatedPayload{ShortID: shortID, Title: "Foreign task", SortKey: "aaaaaa"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	line, err := json.Marshal(eventlog.Envelope{
		V: 1, Rep: rep, Seq: 1, TS: 1,
		Actor: "elsewhere", Type: eventlog.Type(job.EventCreated), Task: shortID, Data: payload,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	dir := eventlog.LogDir(eventlog.StoreDir(cache))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, rep+eventlog.LogExt), append(line, '\n'), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
}

func positionOfEvent(t *testing.T, db *sql.DB, title string) string {
	t.Helper()
	events, err := job.GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	for _, e := range events {
		if strings.Contains(e.Detail, title) {
			return e.Position().String()
		}
	}
	t.Fatalf("no event mentions %q", title)
	return ""
}

func TestLogAtPositionLandsOnTheSameEventAcrossARebuild(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".jobs.db")
	db, err := job.CreateDB(cache)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, title := range []string{"Alpha task", "Bravo task", "Charlie task"} {
		if _, err := job.RunAdd(db, "", title, "", "", nil, "alice"); err != nil {
			t.Fatalf("RunAdd %s: %v", title, err)
		}
	}

	at := positionOfEvent(t, db, "Bravo task")
	deps := newLogDeps(t, db)
	before := stripInitialFrame(fetchLog(t, deps, "at="+at+"&range=all"))
	if !strings.Contains(before, "Bravo task") || strings.Contains(before, "Charlie task") {
		t.Fatalf("?at= did not bound the window before the rebuild")
	}

	// A pull brings another replica's log in; reopening replays the union
	// and renumbers every row id.
	db.Close()
	dropForeignLog(t, cache, "ZZZZZZ", "frgn01")
	db, err = job.OpenDB(cache)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if sync := job.StoreSyncOf(db); sync == nil || sync.State != job.StoreRebuilt {
		t.Fatalf("the reopen did not rebuild: %+v", sync)
	}

	deps = newLogDeps(t, db)
	after := stripInitialFrame(fetchLog(t, deps, "at="+at+"&range=all"))
	if !strings.Contains(after, "Bravo task") {
		t.Errorf("?at=%s lost its event across the rebuild", at)
	}
	if strings.Contains(after, "Charlie task") {
		t.Errorf("?at=%s slid forward across the rebuild", at)
	}
}

func TestLogRejectsAnAtThatIsNotAPosition(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A task", nil, nil)
	deps := newLogDeps(t, db)

	req := httptest.NewRequest("GET", "/log?at=17", nil)
	w := httptest.NewRecorder()
	handlers.Log(deps).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /log?at=17 (a row id, not a position): status %d, want 400", w.Code)
	}
}

func TestEventsJSONCarriesThePositionAndAcceptsItAsSince(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "First task", nil, nil)
	mustAdd(t, db, "alice", "Second task", nil, nil)
	deps := newLogDeps(t, db)

	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /events: %d", w.Code)
	}
	var all []struct {
		ID       int64  `json:"id"`
		Position string `json:"position"`
		Title    string `json:"task_title"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d events, want 2", len(all))
	}
	for _, e := range all {
		if e.Position == "" {
			t.Fatalf("event %d carries no position", e.ID)
		}
		if _, err := eventlog.ParsePosition(e.Position); err != nil {
			t.Fatalf("position %q does not parse: %v", e.Position, err)
		}
	}

	req = httptest.NewRequest("GET", "/events?since="+all[0].Position, nil)
	w = httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET /events?since=<position>: %d", w.Code)
	}
	var rest []struct {
		Title string `json:"task_title"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rest) != 1 || rest[0].Title != "Second task" {
		t.Errorf("?since=<position of the first event> returned %+v, want just the second", rest)
	}
}
