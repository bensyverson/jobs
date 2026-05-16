package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTail_FormatJson_EmitsJSONLines(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "X")
	job.MustClaim(t, db, id, "1h")

	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("job.GetEventsForTaskTree: %v", err)
	}
	var buf bytes.Buffer
	if err := job.FormatEventLogJSONLines(&buf, events); err != nil {
		t.Fatalf("format: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(events) {
		t.Fatalf("got %d lines for %d events:\n%s", len(lines), len(events), buf.String())
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Errorf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}
}

func TestTail_FormatJson_NoTrailingArray(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "X")

	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("job.GetEventsForTaskTree: %v", err)
	}
	var buf bytes.Buffer
	if err := job.FormatEventLogJSONLines(&buf, events); err != nil {
		t.Fatalf("format: %v", err)
	}
	out := buf.String()
	if strings.HasPrefix(out, "[") {
		t.Errorf("JSON-lines should not be wrapped in []:\n%s", out)
	}
	if strings.Contains(out, "},\n{") {
		t.Errorf("should not have comma separators between objects:\n%s", out)
	}
}

func TestTail_Events_Filter(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "X")
	job.MustClaim(t, db, id, "1h")
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("job.GetEventsForTaskTree: %v", err)
	}
	filter := job.EventFilter{Types: job.ParseFilterList("claimed,done")}
	out := job.FilterEvents(events, filter)
	for _, e := range out {
		if e.EventType != "claimed" && e.EventType != "done" {
			t.Errorf("unexpected event type %q passed filter", e.EventType)
		}
	}
	if len(out) != 2 {
		t.Errorf("expected 2 filtered events, got %d", len(out))
	}
}

func TestTail_Users_Filter(t *testing.T) {
	db := job.SetupTestDB(t)
	addRes, err := job.RunAdd(db, "", "X", "", "", nil, "alice")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	id := addRes.ShortID
	if _, err := job.RunAdd(db, "", "Y", "", "", nil, "bob"); err != nil {
		t.Fatalf("add bob: %v", err)
	}

	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("job.GetEventsForTaskTree: %v", err)
	}
	filter := job.EventFilter{Users: job.ParseFilterList("alice")}
	out := job.FilterEvents(events, filter)
	for _, e := range out {
		if e.Actor != "alice" {
			t.Errorf("unexpected actor %q passed filter", e.Actor)
		}
	}
	if len(out) == 0 {
		t.Errorf("expected at least 1 alice event")
	}
}

func TestTail_Events_Intersection_Users(t *testing.T) {
	db := job.SetupTestDB(t)
	addRes, err := job.RunAdd(db, "", "X", "", "", nil, "alice")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	id := addRes.ShortID
	if err := job.RunClaim(db, id, "1h", "", "bob", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	events, err := job.GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("job.GetEventsForTaskTree: %v", err)
	}
	filter := job.EventFilter{
		Types: job.ParseFilterList("claimed"),
		Users: job.ParseFilterList("bob"),
	}
	out := job.FilterEvents(events, filter)
	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}
	if out[0].EventType != "claimed" || out[0].Actor != "bob" {
		t.Errorf("got %+v", out[0])
	}
}

func TestTail_Hides_Heartbeat_Default(t *testing.T) {
	events := []job.EventEntry{
		{EventType: "heartbeat", Actor: "alice"},
		{EventType: "claimed", Actor: "alice"},
	}
	out := job.FilterEvents(events, job.EventFilter{})
	if len(out) != 1 || out[0].EventType != "claimed" {
		t.Errorf("default should hide heartbeat: %+v", out)
	}
}

func TestTail_Events_Heartbeat_OptIn(t *testing.T) {
	events := []job.EventEntry{
		{EventType: "heartbeat", Actor: "alice"},
		{EventType: "claimed", Actor: "alice"},
	}
	out := job.FilterEvents(events, job.EventFilter{Types: job.ParseFilterList("heartbeat")})
	if len(out) != 1 || out[0].EventType != "heartbeat" {
		t.Errorf("--events heartbeat should opt in: %+v", out)
	}
}

func TestTail_UntilClose_AlreadyDone_ExitsImmediately(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	job.MustDone(t, db, id)

	var buf bytes.Buffer
	ctx := context.Background()
	err := job.RunTailUntilClose(ctx, db, id, []string{id}, 0, 20*time.Millisecond, false, "md", job.EventFilter{}, &buf)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	want := "Closed: " + id + " (already done)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestTail_UntilClose_AlreadyCanceled_ExitsImmediately(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, _, _, err := job.RunCancel(db, []string{id}, "nope", false, false, false, "alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	var buf bytes.Buffer
	err := job.RunTailUntilClose(context.Background(), db, id, []string{id}, 0, 20*time.Millisecond, false, "md", job.EventFilter{}, &buf)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	want := "Closed: " + id + " (already canceled)\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestTail_UntilClose_BlocksUntilDone(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	var mu sync.Mutex
	var sw safeWriter
	sw.buf = &buf
	sw.mu = &mu

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, id, []string{id}, 0, 20*time.Millisecond, true, "md", job.EventFilter{}, &sw)
	}()

	// Give goroutine a moment to enter the poll loop.
	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("job.RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for job.RunTailUntilClose to return")
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "Closed: "+id+" (done)") {
		t.Errorf("missing close line:\n%s", out)
	}
}

func TestTail_UntilClose_BlocksUntilCanceled(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, id, []string{id}, 0, 20*time.Millisecond, true, "md", job.EventFilter{}, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, _, _, err := job.RunCancel(db, []string{id}, "nope", false, false, false, "alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("job.RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "Closed: "+id+" (canceled)") {
		t.Errorf("missing close line:\n%s", out)
	}
}

func TestTail_UntilClose_Multi_Conjunction(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	c := job.MustAdd(t, db, "", "C")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, a, []string{a, b, c}, 0, 20*time.Millisecond, true, "md", job.EventFilter{}, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{a}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done a: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{b}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done b: %v", err)
	}

	// After 1.5s, c is still open - still blocking.
	select {
	case err := <-done:
		t.Fatalf("job.RunTailUntilClose returned early with err=%v", err)
	case <-time.After(500 * time.Millisecond):
	}

	if _, _, err := job.RunDone(db, []string{c}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done c: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("job.RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestTail_UntilClose_Quiet_SuppressesStream(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, id, []string{id}, 0, 20*time.Millisecond, true, "md", job.EventFilter{}, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	// Fire a non-terminal event.
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("job.RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	mu.Lock()
	out := buf.String()
	mu.Unlock()

	if strings.Contains(out, "claimed") {
		t.Errorf("quiet mode should suppress claim event:\n%s", out)
	}
	if strings.Contains(out, "Tailing events for") {
		t.Errorf("quiet mode should suppress preamble:\n%s", out)
	}
	if !strings.Contains(out, "Closed: "+id+" (done)") {
		t.Errorf("should still emit close line:\n%s", out)
	}
}

func TestTail_UntilClose_EventsFilter_DoesNotHideTermination(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := job.EventFilter{Types: job.ParseFilterList("claimed")}

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, id, []string{id}, 0, 20*time.Millisecond, false, "md", filter, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("job.RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout; filter should not block terminal detection")
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "Closed: "+id+" (done)") {
		t.Errorf("missing close line:\n%s", out)
	}
}

func TestTail_Timeout_FiresAndExitCode2(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	ctx := context.Background()
	err := job.RunTailUntilClose(ctx, db, id, []string{id}, 100*time.Millisecond, 20*time.Millisecond, true, "md", job.EventFilter{}, &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, job.ErrTailTimeout) {
		t.Errorf("errors.Is(err, job.ErrTailTimeout) = false: %v", err)
	}
	if !strings.Contains(buf.String(), "Timeout:") {
		t.Errorf("missing Timeout summary line:\n%s", buf.String())
	}
}

func TestTail_Timeout_NotFired_ExitsCleanlyOnClose(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(context.Background(), db, id, []string{id}, 3*time.Second, 20*time.Millisecond, true, "md", job.EventFilter{}, &sw)
	}()
	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil err, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

func TestTail_Timeout_QuietJSON_ShapeOnExpiry(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	var buf bytes.Buffer
	err := job.RunTailUntilClose(context.Background(), db, id, []string{id}, 100*time.Millisecond, 20*time.Millisecond, true, "json", job.EventFilter{}, &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, job.ErrTailTimeout) {
		t.Errorf("errors.Is(err, job.ErrTailTimeout) = false: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if out == "" {
		t.Fatal("expected JSON output")
	}
	lines := strings.Split(out, "\n")
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if got["timeout"] != true {
		t.Errorf("timeout field: %v", got["timeout"])
	}
	arr, ok := got["still_open"].([]any)
	if !ok || len(arr) != 1 || arr[0] != id {
		t.Errorf("still_open: %v", got["still_open"])
	}
}

// P8 red: RunTail with empty shortID streams events from all trees.
func TestRunTail_EmptyAnchor_GlobalStream(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	seen := map[string]bool{}
	initialDrained := make(chan struct{})
	bothSeen := make(chan struct{})
	done := make(chan struct{})

	go func() {
		sawInitial := false
		job.RunTail(ctx, db, "", 10*time.Millisecond, func(events []job.EventEntry) error {
			mu.Lock()
			for _, e := range events {
				seen[e.ShortID] = true
			}
			haveBoth := seen[a] && seen[b]
			mu.Unlock()
			if !sawInitial {
				sawInitial = true
				close(initialDrained)
			}
			if haveBoth {
				select {
				case <-bothSeen:
				default:
					close(bothSeen)
				}
				cancel()
			}
			return nil
		})
		close(done)
	}()

	<-initialDrained
	// Fire a fresh event in a different tree than the first one we saw.
	if err := job.RunClaim(db, b, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	select {
	case <-bothSeen:
	case <-time.After(4 * time.Second):
		t.Fatalf("timeout; seen=%v want {%s,%s}", seen, a, b)
	}
	<-done
}

// P8 red: `job tail` with no arg is accepted; `job tail all` is a synonym.
// We exercise this via `--until-close=<id> --timeout` so the command
// terminates deterministically. The global stream should include events
// from sibling trees (not just the --until-close target).
func TestTail_NoArg_GlobalStream_WithUntilClose(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	b := job.MustAdd(t, db, "", "Beta")
	db.Close()

	// Tail globally, block until b closes. Pre-close b so we exit promptly.
	db2 := openTestDB(t, dbFile)
	if _, _, err := job.RunDone(db2, []string{b}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done b: %v", err)
	}
	db2.Close()

	stdout, _, err := runCLI(t, dbFile, "tail", "--until-close="+b, "--timeout", "3s")
	if err != nil {
		t.Fatalf("tail (no arg) --until-close=%s: %v\n%s", b, err, stdout)
	}
	// Preamble + already-done path should have fired; both ids should be
	// referenceable but at minimum b must close cleanly.
	if !strings.Contains(stdout, "Closed: "+b) {
		t.Errorf("expected close line for %s:\n%s", b, stdout)
	}
	_ = a
}

func TestTail_All_Synonym(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	b := job.MustAdd(t, db, "", "Beta")
	if _, _, err := job.RunDone(db, []string{b}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done b: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "tail", "all", "--until-close="+b, "--timeout", "3s")
	if err != nil {
		t.Fatalf("tail all --until-close=%s: %v\n%s", b, err, stdout)
	}
	if !strings.Contains(stdout, "Closed: "+b) {
		t.Errorf("expected close line for %s:\n%s", b, stdout)
	}
}

// P8 red: existing `job tail <id>` still scopes to that tree.
func TestTail_SpecificID_Unchanged(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "tail", id, "--until-close=_", "--timeout", "3s")
	if err != nil {
		t.Fatalf("tail %s: %v\n%s", id, err, stdout)
	}
	if !strings.Contains(stdout, "Closed: "+id) {
		t.Errorf("expected close line:\n%s", stdout)
	}
}

// P8 red: `tail --until-close=<id>` still watches the named task even when
// streaming globally (no positional anchor).
func TestTail_Global_UntilCloseNamedTask(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "Alpha")
	target := job.MustAdd(t, db, "", "Target")

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// Empty positional → global stream, watch only target.
		done <- job.RunTailUntilClose(ctx, db, "", []string{target}, 0, 20*time.Millisecond, false, "md", job.EventFilter{}, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	// Fire an event on a sibling tree — it should stream.
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{target}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done target: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "Closed: "+target+" (done)") {
		t.Errorf("missing close line for %s:\n%s", target, out)
	}
	// Sibling event from a different tree should appear in the global stream.
	if !strings.Contains(out, a) {
		t.Errorf("expected sibling tree %s event in global stream:\n%s", a, out)
	}
}

// P8 red: --events filter composes with global scope.
func TestTail_Global_EventsFilter(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "Alpha")
	_ = a

	var buf bytes.Buffer
	var mu sync.Mutex
	sw := safeWriter{buf: &buf, mu: &mu}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	filter := job.EventFilter{Types: job.ParseFilterList("claimed")}

	done := make(chan error, 1)
	go func() {
		done <- job.RunTailUntilClose(ctx, db, "", []string{a}, 0, 20*time.Millisecond, false, "md", filter, &sw)
	}()

	time.Sleep(100 * time.Millisecond)
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, _, err := job.RunDone(db, []string{a}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTailUntilClose: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	mu.Lock()
	out := buf.String()
	mu.Unlock()
	if !strings.Contains(out, "claimed") {
		t.Errorf("claimed event should be in output:\n%s", out)
	}
	if strings.Contains(out, "created:") {
		t.Errorf("created event should be filtered out by --events=claimed:\n%s", out)
	}
}

// safeWriter is a mutex-guarded bytes.Buffer used by the tail tests so the
// test goroutine and the tail goroutine don't race on the buffer.
type safeWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
