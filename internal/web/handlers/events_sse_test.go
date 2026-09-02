package handlers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// sseFrame is one decoded SSE event. Only the fields we care about in
// tests — enough to assert dedup and ordering.
type sseFrame struct {
	ID   string
	Type string
	Data map[string]any
}

// readSSE reads SSE frames from r until ctx is canceled or r closes,
// parsing id: / event: / data: lines. Returns a channel the test can
// drain.
func readSSE(t *testing.T, r io.Reader) <-chan sseFrame {
	t.Helper()
	out := make(chan sseFrame, 32)
	go func() {
		defer close(out)
		rd := bufio.NewReader(r)
		var f sseFrame
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "id: "):
				f.ID = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
			case strings.HasPrefix(line, "event: "):
				f.Type = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				raw := strings.TrimPrefix(line, "data: ")
				raw = strings.TrimSuffix(raw, "\n")
				var data map[string]any
				_ = json.Unmarshal([]byte(raw), &data)
				f.Data = data
			case line == "\n":
				out <- f
				f = sseFrame{}
			}
		}
	}()
	return out
}

func openSSE(t *testing.T, ts *httptest.Server, query string, headers map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.URL+"/events?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("sse open: %v", err)
	}
	return resp, cancel
}

func TestEvents_SSE_BackfillReplaysSinceID(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "one", nil, nil)
	mustAdd(t, db, "bob", "two", nil, nil)
	mustAdd(t, db, "carla", "three", nil, nil)

	deps := withBroadcaster(t, newLogDeps(t, db))
	ts := httptest.NewServer(handlers.Events(deps))
	defer ts.Close()

	// ?since=0 should backfill every event currently in the DB.
	resp, cancel := openSSE(t, ts, "since=0", nil)
	defer func() { resp.Body.Close(); cancel() }()
	ch := readSSE(t, resp.Body)

	seen := map[string]bool{}
	deadline := time.After(2 * time.Second)
	for len(seen) < 3 {
		select {
		case f, ok := <-ch:
			if !ok {
				t.Fatalf("stream closed with %d frames seen", len(seen))
			}
			seen[f.ID] = true
		case <-deadline:
			t.Fatalf("only received %d backfill frames within 2s: %v", len(seen), seen)
		}
	}
}

func TestEvents_SSE_BackfillThenLive_NoDuplicates(t *testing.T) {
	db := setupLogTestDB(t)
	// Seed one event so backfill is non-empty.
	mustAdd(t, db, "seed", "before subscribe", nil, nil)

	deps := withBroadcaster(t, newLogDeps(t, db))
	ts := httptest.NewServer(handlers.Events(deps))
	defer ts.Close()

	resp, cancel := openSSE(t, ts, "since=0", nil)
	defer func() { resp.Body.Close(); cancel() }()
	ch := readSSE(t, resp.Body)

	// Let backfill land.
	first := must1Frame(t, ch, time.Second)
	// Generate a live event.
	mustAdd(t, db, "live", "after subscribe", nil, nil)

	// Collect a few frames; verify no id appears twice.
	seen := map[string]bool{first.ID: true}
	deadline := time.After(2 * time.Second)
gather:
	for len(seen) < 2 {
		select {
		case f, ok := <-ch:
			if !ok {
				break gather
			}
			if seen[f.ID] {
				t.Errorf("duplicate id %q across backfill/live handoff", f.ID)
			}
			seen[f.ID] = true
		case <-deadline:
			break gather
		}
	}
	if len(seen) < 2 {
		t.Errorf("expected at least one backfill + one live frame; saw %d unique ids: %v", len(seen), seen)
	}
}

func TestEvents_SSE_ReconnectWithLastEventID(t *testing.T) {
	db := setupLogTestDB(t)
	mustAdd(t, db, "alice", "first", nil, nil)
	mustAdd(t, db, "alice", "second", nil, nil)
	mustAdd(t, db, "alice", "third", nil, nil)

	// Grab the middle event's position so we can resume past it.
	events, err := job.GetEventsForTaskTree(db, "")
	if err != nil || len(events) < 2 {
		t.Fatalf("seed: %v / %d events", err, len(events))
	}
	mid := events[1].Position().String()

	deps := withBroadcaster(t, newLogDeps(t, db))
	ts := httptest.NewServer(handlers.Events(deps))
	defer ts.Close()

	// Simulate a reconnect by passing ?since=<position>. Production
	// clients (EventSource) send the Last-Event-ID header, whose value is
	// the same position each frame's id: field carried.
	resp, cancel := openSSE(t, ts, "since="+mid, nil)
	defer func() { resp.Body.Close(); cancel() }()
	ch := readSSE(t, resp.Body)

	// Only the third event (after mid) should come through backfill, and
	// the frame's id: field must itself be a position — that is what makes
	// a Last-Event-ID usable verbatim as ?since=.
	f := must1Frame(t, ch, 2*time.Second)
	got, err := eventlog.ParsePosition(f.ID)
	if err != nil {
		t.Fatalf("frame id %q is not a position: %v", f.ID, err)
	}
	midPos, err := eventlog.ParsePosition(mid)
	if err != nil {
		t.Fatalf("seed position %q: %v", mid, err)
	}
	if got.Compare(midPos) <= 0 {
		t.Errorf("frame id %s, want after %s (resume must skip already-seen events)", f.ID, mid)
	}
}

func TestEvents_SSE_FilterScopesLiveStream(t *testing.T) {
	db := setupLogTestDB(t)
	deps := withBroadcaster(t, newLogDeps(t, db))
	ts := httptest.NewServer(handlers.Events(deps))
	defer ts.Close()

	resp, cancel := openSSE(t, ts, "actor=alice", nil)
	defer func() { resp.Body.Close(); cancel() }()
	ch := readSSE(t, resp.Body)

	// Create events for both actors. The stream should only see alice.
	mustAdd(t, db, "alice", "alice event", nil, nil)
	mustAdd(t, db, "bob", "bob event", nil, nil)
	mustAdd(t, db, "alice", "another alice event", nil, nil)

	var mu sync.Mutex
	var frames []sseFrame
	deadline := time.After(1500 * time.Millisecond)
gather:
	for {
		select {
		case f, ok := <-ch:
			if !ok {
				break gather
			}
			mu.Lock()
			frames = append(frames, f)
			mu.Unlock()
		case <-deadline:
			break gather
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 alice frames, got %d", len(frames))
	}
	for _, f := range frames {
		if f.Data == nil {
			continue
		}
		if actor, _ := f.Data["actor"].(string); actor != "alice" {
			t.Errorf("filter=alice leaked an actor=%q frame", actor)
		}
	}
}

// --- helpers ---

func must1Frame(t *testing.T, ch <-chan sseFrame, timeout time.Duration) sseFrame {
	t.Helper()
	select {
	case f, ok := <-ch:
		if !ok {
			t.Fatal("stream closed before any frame arrived")
		}
		return f
	case <-time.After(timeout):
		t.Fatalf("no frame within %v", timeout)
		return sseFrame{}
	}
}

func parseInt(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
