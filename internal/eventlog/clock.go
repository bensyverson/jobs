package eventlog

import (
	"sync"
	"time"
)

// Clock is a hybrid logical clock in milliseconds. Now returns
// max(wallclock, lastSeen+1), so a replica's own events never repeat or go
// backwards; Observe raises lastSeen when a read event carries a future
// timestamp, so cause sorts before effect even when two machines' clocks
// disagree.
//
// lastSeen is persisted by the caller (it lives in local.json, which this
// package does not own): Load it at open, Save it after writing.
//
// A Clock is safe for concurrent use.
type Clock struct {
	mu       sync.Mutex
	wall     func() time.Time
	lastSeen int64
}

// NewClock returns a clock reading the system wall clock.
func NewClock() *Clock { return NewClockWith(time.Now) }

// NewClockWith returns a clock reading wall for the current time. It exists so
// tests can move time freely, including backwards.
func NewClockWith(wall func() time.Time) *Clock {
	return &Clock{wall: wall}
}

// Now returns the next timestamp for an event minted by this replica and
// advances lastSeen to it.
func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w := c.wall().UnixMilli(); w > c.lastSeen {
		c.lastSeen = w
	} else {
		c.lastSeen++
	}
	return c.lastSeen
}

// Observe raises lastSeen to ts if ts is in the future relative to it. Call it
// for every event read from any replica's log.
func (c *Clock) Observe(ts int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts > c.lastSeen {
		c.lastSeen = ts
	}
}

// Load sets lastSeen from persisted state. It never lowers it.
func (c *Clock) Load(lastSeen int64) { c.Observe(lastSeen) }

// Save returns the lastSeen value to persist.
func (c *Clock) Save() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSeen
}
