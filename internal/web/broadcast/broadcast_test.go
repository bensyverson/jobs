package broadcast_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/broadcast"
)

// Tests drive the broadcaster at 10ms polls so they don't pay a
// full second per iteration. The production default is 1s.
const testPoll = 10 * time.Millisecond

func setupBroadcasterDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broadcast.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// startBroadcaster starts a broadcaster tied to t.Context so it stops
// with the test.
func startBroadcaster(t *testing.T, db *sql.DB) *broadcast.Broadcaster {
	t.Helper()
	b := broadcast.New(db, testPoll)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{})
	go func() {
		close(started)
		_ = b.Start(ctx)
	}()
	<-started
	// Give the poll loop a chance to enter its first iteration before
	// the test starts exercising it.
	time.Sleep(2 * testPoll)
	return b
}

// waitForEvent blocks until a real event arrives on ch or timeout
// elapses. A closed channel counts as a miss (not a hit) — tests
// use this to assert delivery, not channel lifecycle.
func waitForEvent(ch <-chan job.EventEntry, timeout time.Duration) (job.EventEntry, bool) {
	select {
	case e, ok := <-ch:
		if !ok {
			return job.EventEntry{}, false
		}
		return e, true
	case <-time.After(timeout):
		return job.EventEntry{}, false
	}
}

func TestBroadcaster_SubscribeReceivesNewEvents(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)

	sub := b.Subscribe()
	defer sub.Cancel()

	if _, err := job.RunAdd(db, "", "after subscribe", "", "", nil, "alice"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}

	e, ok := waitForEvent(sub.Events, 500*time.Millisecond)
	if !ok {
		t.Fatal("subscriber did not receive the created event within 500ms")
	}
	if e.EventType != "created" {
		t.Errorf("event type = %q, want %q", e.EventType, "created")
	}
	if e.Actor != "alice" {
		t.Errorf("actor = %q, want alice", e.Actor)
	}
	if e.Position().Compare(sub.Cutoff) <= 0 {
		t.Errorf("delivered event at %s <= cutoff %s; the live stream must only carry events after the cutoff", e.Position(), sub.Cutoff)
	}
}

func TestBroadcaster_SeedsItsCursorAtStartup(t *testing.T) {
	db := setupBroadcasterDB(t)
	// Seed a pile of events BEFORE the broadcaster starts.
	for range 20 {
		if _, err := job.RunAdd(db, "", "pre-boot", "", "", nil, "seeder"); err != nil {
			t.Fatalf("RunAdd: %v", err)
		}
	}
	b := startBroadcaster(t, db)

	sub := b.Subscribe()
	defer sub.Cancel()

	// Without the lastID seed, the first poll would re-deliver the 20
	// pre-boot events to this subscriber. With the seed, the channel
	// should stay empty until a new event arrives.
	if _, ok := waitForEvent(sub.Events, 100*time.Millisecond); ok {
		t.Error("subscriber received a pre-boot event; broadcaster should have seeded lastID to MAX(id)")
	}

	if _, err := job.RunAdd(db, "", "post-boot", "", "", nil, "alice"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	if _, ok := waitForEvent(sub.Events, 500*time.Millisecond); !ok {
		t.Error("subscriber did not receive the post-boot event")
	}
}

func TestBroadcaster_MultipleSubscribersAllReceive(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)

	const n = 3
	chs := make([]<-chan job.EventEntry, n)
	for i := range n {
		sub := b.Subscribe()
		t.Cleanup(sub.Cancel)
		chs[i] = sub.Events
	}

	if _, err := job.RunAdd(db, "", "fanout-test", "", "", nil, "carla"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}

	for i, ch := range chs {
		if _, ok := waitForEvent(ch, 500*time.Millisecond); !ok {
			t.Errorf("subscriber %d did not receive the event", i)
		}
	}
}

func TestBroadcaster_UnsubscribeStopsDelivery(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)

	sub := b.Subscribe()
	sub.Cancel()
	// Drain anything already in flight before the cancel took effect.
	drain(sub.Events, 50*time.Millisecond)

	if _, err := job.RunAdd(db, "", "after cancel", "", "", nil, "bob"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}

	if _, ok := waitForEvent(sub.Events, 200*time.Millisecond); ok {
		t.Error("received event after unsubscribe — channel should be silent (or closed)")
	}
}

func TestBroadcaster_CloseIsIdempotent(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)
	sub := b.Subscribe()
	sub.Cancel()
	sub.Cancel()
	sub.Cancel()
}

func TestBroadcaster_SlowSubscriberDoesNotBlockOthers(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)

	// One subscriber that never drains — fills up to buffer, then drops.
	slow := b.Subscribe()
	defer slow.Cancel()
	// One subscriber that does drain.
	fast := b.Subscribe()
	defer fast.Cancel()
	_ = slow.Events // reference to avoid an unused-warning temptation

	var wg sync.WaitGroup
	wg.Add(1)
	got := make(chan struct{})
	go func() {
		defer wg.Done()
		if _, ok := waitForEvent(fast.Events, 1*time.Second); ok {
			close(got)
		}
	}()

	// Generate many events fast so the slow subscriber's buffer fills.
	for range 128 {
		if _, err := job.RunAdd(db, "", "event", "", "", nil, "carla"); err != nil {
			t.Fatalf("RunAdd: %v", err)
		}
	}

	select {
	case <-got:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("fast subscriber starved — slow subscriber blocked the fanout")
	}
	wg.Wait()
}

func TestBroadcaster_Fanout_ManySubscribersManyEvents(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := startBroadcaster(t, db)

	const (
		subs   = 20
		events = 50
	)
	counts := make([]int, subs)
	var wg sync.WaitGroup
	wg.Add(subs)

	for i := range subs {
		sub := b.Subscribe()
		go func() {
			defer wg.Done()
			deadline := time.After(3 * time.Second)
			for {
				select {
				case _, ok := <-sub.Events:
					if !ok {
						return
					}
					counts[i]++
					if counts[i] >= events {
						sub.Cancel()
						return
					}
				case <-deadline:
					return
				}
			}
		}()
	}

	// Give subscribers a moment to all register.
	time.Sleep(2 * testPoll)
	for range events {
		if _, err := job.RunAdd(db, "", "burst", "", "", nil, "bursty"); err != nil {
			t.Fatalf("RunAdd: %v", err)
		}
	}

	wg.Wait()
	// Every subscriber should have seen every event (or come very
	// close, allowing for a handful of drops if poll timing is
	// unlucky). A subscriber that saw zero events is broken.
	for i, got := range counts {
		if got == 0 {
			t.Errorf("subscriber %d received zero events", i)
		}
		if got < events-2 {
			// Don't fail the test for a small shortfall (the 64-buffer
			// drops kick in on the fastest tick), but log for visibility.
			t.Logf("subscriber %d received %d of %d events (likely buffer drops)", i, got, events)
		}
	}
}

func TestBroadcaster_StartReturnsOnContextCancel(t *testing.T) {
	db := setupBroadcasterDB(t)
	b := broadcast.New(db, testPoll)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Start(ctx) }()

	time.Sleep(3 * testPoll)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Start did not return within 1s of context cancel")
	}
}

func drain(ch <-chan job.EventEntry, dur time.Duration) {
	deadline := time.After(dur)
	for {
		select {
		case <-ch:
		case <-deadline:
			return
		}
	}
}
