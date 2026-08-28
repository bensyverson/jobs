package handlers_test

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/broadcast"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// withBroadcaster returns deps with a started broadcaster. Cancel
// fires on test cleanup.
func withBroadcaster(t *testing.T, deps handlers.Deps) handlers.Deps {
	t.Helper()
	bc := broadcast.New(deps.DB, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = bc.Start(ctx) }()
	deps.Broadcaster = bc
	// Let the broadcaster enter its poll loop before returning.
	time.Sleep(25 * time.Millisecond)
	return deps
}

func TestEvents_JSONFallback_ReturnsArray(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "event one", nil, nil)
	mustAdd(t, db, "bob", "event two", nil, nil)

	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/events", nil)
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, w.Body.String())
	}
	if len(decoded) < 2 {
		t.Errorf("got %d events, want at least 2", len(decoded))
	}
}

func TestEvents_JSON_SinceFiltersByID(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "first", nil, nil)
	mustAdd(t, db, "bob", "second", nil, nil)

	// Find the id of the first event so we can filter past it.
	first, err := job.GetEventsAfterID(db, "", 0)
	if err != nil {
		t.Fatalf("seed read: %v", err)
	}
	if len(first) < 1 {
		t.Fatal("seed: no events")
	}
	sinceID := first[0].ID

	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/events?since="+itoa(sinceID), nil)
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)

	var decoded []map[string]any
	json.Unmarshal(w.Body.Bytes(), &decoded)
	for _, e := range decoded {
		if id := int64FromJSON(e["id"]); id <= sinceID {
			t.Errorf("since=%d: got event id %d in payload", sinceID, id)
		}
	}
}

func TestEvents_JSON_ActorFilterScopes(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "A", nil, nil)
	mustAdd(t, db, "bob", "B", nil, nil)

	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/events?actor=alice", nil)
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)

	var decoded []map[string]any
	json.Unmarshal(w.Body.Bytes(), &decoded)
	for _, e := range decoded {
		if e["actor"] != "alice" {
			t.Errorf("actor=alice filter: got event with actor %v", e["actor"])
		}
	}
	if len(decoded) == 0 {
		t.Error("actor=alice: expected at least one event")
	}
}

func TestEvents_SSE_NoBroadcaster_503(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db) // no broadcaster
	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503 when broadcaster is nil", w.Code)
	}
}

func TestEvents_SSE_StreamsLiveEvents(t *testing.T) {
	db := setupLogTestDB(t)
	deps := withBroadcaster(t, newLogDeps(t, db))

	// Need a real HTTP server so r.Context() cancels when the client
	// disconnects (httptest.Recorder doesn't honor that).
	ts := httptest.NewServer(handlers.Events(deps))
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	ctx := t.Context()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Trigger an event after the subscription is live.
	time.Sleep(30 * time.Millisecond)
	mustAdd(t, db, "alice", "live-tail-event", nil, nil)

	// Read one SSE frame from the stream, with a deadline.
	frame := make(chan string, 1)
	go func() {
		rd := bufio.NewReader(resp.Body)
		var buf strings.Builder
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			buf.WriteString(line)
			if line == "\n" {
				frame <- buf.String()
				return
			}
		}
	}()

	select {
	case got := <-frame:
		if !strings.Contains(got, "event: created") {
			t.Errorf("frame missing event: created line\n---\n%s", got)
		}
		if !strings.Contains(got, "live-tail-event") {
			t.Errorf("frame missing task title 'live-tail-event'\n---\n%s", got)
		}
		if !strings.HasPrefix(got, "id: ") {
			t.Errorf("frame should start with 'id: '\n---\n%s", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive SSE frame within 2s")
	}
}

// --- helpers ---

func itoa(n int64) string {
	// Keep local so we don't sprinkle strconv imports in the testfile.
	s := ""
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		return "-" + s
	}
	return s
}

func int64FromJSON(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// The client's reverse fold needs the before/after kind and both ends
// of a found-in edge. `detail` is an opaque JSON string on the wire,
// so the guarantee this test pins is that the domain's breadcrumbs
// reach the client verbatim — no re-shaping in the handler.
func TestEvents_JSON_KindChangedCarriesBeforeAndAfter(t *testing.T) {
	db := setupLogTestDB(t)
	root := mustAdd(t, db, "alice", "a root", nil, nil)
	if _, err := job.RunSetKind(db, root, job.KindIssue, "alice"); err != nil {
		t.Fatalf("RunSetKind: %v", err)
	}

	detail := detailForEventType(t, db, "kind_changed")
	if detail["from"] != "task" {
		t.Errorf("detail.from = %v, want %q", detail["from"], "task")
	}
	if detail["to"] != "issue" {
		t.Errorf("detail.to = %v, want %q", detail["to"], "issue")
	}
}

func TestEvents_JSON_FoundInSetCarriesBothEnds(t *testing.T) {
	db := setupLogTestDB(t)
	source := mustAdd(t, db, "alice", "the source", nil, nil)
	surfaced := mustAdd(t, db, "alice", "the defect", nil, nil)
	if err := job.RunSetFoundIn(db, surfaced, source, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}

	detail := detailForEventType(t, db, "found_in_set")
	if detail["task_id"] != surfaced {
		t.Errorf("detail.task_id = %v, want %q", detail["task_id"], surfaced)
	}
	if detail["source_id"] != source {
		t.Errorf("detail.source_id = %v, want %q", detail["source_id"], source)
	}
}

func TestEvents_JSON_FoundInClearedCarriesThePriorSource(t *testing.T) {
	db := setupLogTestDB(t)
	source := mustAdd(t, db, "alice", "the source", nil, nil)
	surfaced := mustAdd(t, db, "alice", "the defect", nil, nil)
	if err := job.RunSetFoundIn(db, surfaced, source, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}
	if err := job.RunClearFoundIn(db, surfaced, "alice"); err != nil {
		t.Fatalf("RunClearFoundIn: %v", err)
	}

	detail := detailForEventType(t, db, "found_in_cleared")
	if detail["task_id"] != surfaced {
		t.Errorf("detail.task_id = %v, want %q", detail["task_id"], surfaced)
	}
	if detail["source_id"] != source {
		t.Errorf("detail.source_id = %v, want %q", detail["source_id"], source)
	}
}

// detailForEventType fetches /events and returns the parsed `detail`
// of the sole event of the named type, failing when there isn't
// exactly one.
func detailForEventType(t *testing.T, db *sql.DB, eventType string) map[string]any {
	t.Helper()
	deps := newLogDeps(t, db)
	req := httptest.NewRequest("GET", "/events?type="+eventType, nil)
	w := httptest.NewRecorder()
	handlers.Events(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d, want 200", w.Code)
	}

	var decoded []struct {
		EventType string `json:"event_type"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, w.Body.String())
	}
	if len(decoded) != 1 {
		t.Fatalf("got %d %s events, want 1: %s", len(decoded), eventType, w.Body.String())
	}
	if decoded[0].EventType != eventType {
		t.Fatalf("event_type = %q, want %q", decoded[0].EventType, eventType)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(decoded[0].Detail), &detail); err != nil {
		t.Fatalf("detail not JSON: %v\n%s", err, decoded[0].Detail)
	}
	return detail
}
