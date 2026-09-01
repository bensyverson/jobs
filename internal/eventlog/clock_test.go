package eventlog

import (
	"sync"
	"testing"
	"time"
)

// fakeWall is a settable wall clock.
type fakeWall struct {
	mu sync.Mutex
	ms int64
}

func (w *fakeWall) set(ms int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ms = ms
}

func (w *fakeWall) now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.UnixMilli(w.ms)
}

func TestClockFollowsTheWallClockWhenItLeads(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)
	if got := c.Now(); got != 1000 {
		t.Fatalf("Now = %d, want 1000", got)
	}
	w.set(2000)
	if got := c.Now(); got != 2000 {
		t.Fatalf("Now = %d, want 2000", got)
	}
}

func TestClockNeverRepeatsWithinAMillisecond(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)
	var seen []int64
	for range 5 {
		seen = append(seen, c.Now())
	}
	for i, ts := range seen {
		if want := int64(1000 + i); ts != want {
			t.Fatalf("Now #%d = %d, want %d (seen %v)", i, ts, want, seen)
		}
	}
}

func TestClockNeverGoesBackwardsWhenTheWallClockDoes(t *testing.T) {
	w := &fakeWall{ms: 5000}
	c := NewClockWith(w.now)
	first := c.Now()

	w.set(1000) // the machine's clock is set back four seconds
	var prev = first
	for range 10 {
		got := c.Now()
		if got <= prev {
			t.Fatalf("Now = %d after %d; the clock went backwards or repeated", got, prev)
		}
		prev = got
	}
	if prev <= first {
		t.Fatalf("clock ended at %d, not past its pre-rewind value %d", prev, first)
	}
}

func TestObserveAdvancesTheClockPastAFutureEvent(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)

	future := int64(9_000_000)
	c.Observe(future)

	if got := c.Now(); got <= future {
		t.Fatalf("Now = %d, want > %d after observing a future event", got, future)
	}
	if got := c.Save(); got <= future {
		t.Fatalf("Save = %d, want > %d", got, future)
	}
}

func TestObserveIgnoresThePast(t *testing.T) {
	w := &fakeWall{ms: 5000}
	c := NewClockWith(w.now)
	c.Observe(10)
	if got := c.Now(); got != 5000 {
		t.Fatalf("Now = %d, want 5000; a past event must not lower the clock", got)
	}
}

func TestLoadAndSaveCarryLastSeenAcrossProcesses(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)
	c.Observe(50_000)
	saved := c.Save()

	next := NewClockWith(w.now)
	next.Load(saved)
	if got := next.Now(); got != saved+1 {
		t.Fatalf("Now = %d after Load(%d), want %d", got, saved, saved+1)
	}
}

func TestLoadNeverLowersTheClock(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)
	c.Observe(50_000)
	c.Load(10)
	if got := c.Save(); got != 50_000 {
		t.Fatalf("Save = %d, want 50000; Load must not lower lastSeen", got)
	}
}

func TestClockIsSafeForConcurrentUse(t *testing.T) {
	w := &fakeWall{ms: 1000}
	c := NewClockWith(w.now)

	const n = 200
	out := make(chan int64, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			out <- c.Now()
		})
	}
	wg.Wait()
	close(out)

	seen := map[int64]bool{}
	for ts := range out {
		if seen[ts] {
			t.Fatalf("timestamp %d issued twice", ts)
		}
		seen[ts] = true
	}
}

func TestNewClockUsesTheSystemClock(t *testing.T) {
	c := NewClock()
	before := time.Now().UnixMilli()
	got := c.Now()
	after := time.Now().UnixMilli()
	if got < before || got > after+1 {
		t.Fatalf("Now = %d, want within [%d, %d]", got, before, after+1)
	}
}
