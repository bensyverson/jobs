// Package broadcast wraps the single-process event poll so every
// connected dashboard browser can share one DB tail. A [Broadcaster]
// polls once per interval and fans each batch of new events out to
// every subscriber's channel.
//
// Startup seed: on [Broadcaster.Start] the newest event's log position is
// captured and used as the initial poll cursor, so the first poll
// only delivers events that arrived after startup. Without this seed
// the first poll would re-deliver every event already in the DB —
// fine for a no-subscribers-yet broadcaster, but surprising for any
// code path that reads Subscribe() as "future events."
//
// Subscription handoff: [Broadcaster.Subscribe] returns the channel
// plus a Cutoff snapshot captured under the same lock that registered
// the subscriber. Callers that splice a backfill into a live stream
// use Cutoff as the backfill's upper bound and dedup anything on the
// channel at or before it. The cursor is the log position, never the
// cache's row id: a rebuild renumbers row ids, so an id cursor would
// replay or skip whatever a pull displaced.
//
// Backpressure: fanout uses a non-blocking send with a per-subscriber
// buffered channel. If a subscriber can't keep up, the broadcaster
// drops the event for that subscriber rather than blocking the fleet.
// The subscriber's recovery strategy is to reconnect with
// ?since=<position>. Dropped events are counted in [Broadcaster.Dropped].
//
// Lifecycle: [Broadcaster.Start] blocks until ctx cancel, then closes
// every subscriber's channel so consumers see a closed-channel signal
// rather than silence. Subscribe cancel is idempotent — double-cancel
// must not panic.
package broadcast

import (
	"context"
	"database/sql"
	"log"
	"sync"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
)

// DefaultBufferSize is how many events can queue per subscriber
// before the broadcaster starts dropping their deliveries. Tuned so
// a 1-Hz burst of ~60 events can land without drop on a browser that
// renders one batch per rAF tick.
const DefaultBufferSize = 64

// DefaultPollInterval matches `job tail` — see project vision §6.2,
// "1-Hz poll-based broadcaster."
const DefaultPollInterval = time.Second

// Broadcaster polls the events table and fans new events out to
// every live subscriber. Construct with [New], launch with [Start],
// and register consumers with [Broadcaster.Subscribe].
type Broadcaster struct {
	db           *sql.DB
	pollInterval time.Duration

	// cursor is the log position of the newest event the poll loop has
	// delivered. Updated monotonically from the poll goroutine; guarded by
	// mu (a Position is three fields, so it cannot ride an atomic).
	cursor eventlog.Position

	mu          sync.Mutex
	subscribers map[int64]*subscriber
	nextSubID   int64
	dropped     int64
}

type subscriber struct {
	id     int64
	ch     chan job.EventEntry
	cancel chan struct{}
	once   sync.Once
}

// Subscription is what [Broadcaster.Subscribe] returns: a channel of
// future events, a LastID cutoff for splicing with a backfill, and an
// idempotent Cancel.
type Subscription struct {
	Events <-chan job.EventEntry
	Cutoff eventlog.Position
	Cancel func()
}

// New constructs a broadcaster. pollInterval <= 0 falls back to
// [DefaultPollInterval].
func New(db *sql.DB, pollInterval time.Duration) *Broadcaster {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &Broadcaster{
		db:           db,
		pollInterval: pollInterval,
		subscribers:  make(map[int64]*subscriber),
	}
}

// Start runs the poll loop until ctx is canceled. Returns nil on
// clean shutdown; DB errors from the seed or any poll bubble up.
// Safe to call only once per broadcaster.
func (b *Broadcaster) Start(ctx context.Context) error {
	seed, err := job.GetHeadPosition(b.db)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.cursor = seed
	b.mu.Unlock()

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	defer b.closeAll()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if err := b.pollOnce(); err != nil {
			log.Printf("broadcast: poll error: %v", err)
			// Transient DB errors shouldn't kill the loop; try again
			// next tick. Unrecoverable errors (closed DB) will keep
			// failing until the caller cancels ctx.
		}
	}
}

func (b *Broadcaster) pollOnce() error {
	b.mu.Lock()
	cursor := b.cursor
	b.mu.Unlock()
	events, err := job.GetEventsAfterPosition(b.db, "", cursor)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	b.fanout(events)
	b.mu.Lock()
	b.cursor = events[len(events)-1].Position()
	b.mu.Unlock()
	return nil
}

// Subscribe registers a new consumer. The returned Subscription's
// Events channel receives every event the broadcaster observes after
// Subscribe returned; Cutoff is the broadcaster's log-position cursor at
// subscribe time, suitable as a backfill upper bound.
func (b *Broadcaster) Subscribe() Subscription {
	b.mu.Lock()
	b.nextSubID++
	s := &subscriber{
		id:     b.nextSubID,
		ch:     make(chan job.EventEntry, DefaultBufferSize),
		cancel: make(chan struct{}),
	}
	b.subscribers[s.id] = s
	cutoff := b.cursor
	b.mu.Unlock()

	cancel := func() {
		s.once.Do(func() {
			b.mu.Lock()
			delete(b.subscribers, s.id)
			b.mu.Unlock()
			close(s.cancel)
			close(s.ch)
		})
	}
	return Subscription{Events: s.ch, Cutoff: cutoff, Cancel: cancel}
}

// fanout delivers a batch to every subscriber. Sends are non-blocking;
// a full buffer drops the event for just that subscriber.
func (b *Broadcaster) fanout(events []job.EventEntry) {
	b.mu.Lock()
	targets := make([]*subscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	for _, s := range targets {
		for _, ev := range events {
			select {
			case <-s.cancel:
				goto nextSubscriber
			default:
			}
			select {
			case s.ch <- ev:
			default:
				b.mu.Lock()
				b.dropped++
				drops := b.dropped
				b.mu.Unlock()
				if drops%64 == 1 {
					log.Printf("broadcast: dropped event (subscriber %d buffer full, total drops: %d)", s.id, drops)
				}
			}
		}
	nextSubscriber:
	}
}

// closeAll unregisters and closes every subscriber. Called on Start
// shutdown so consumers see a closed-channel signal.
func (b *Broadcaster) closeAll() {
	b.mu.Lock()
	targets := make([]*subscriber, 0, len(b.subscribers))
	for _, s := range b.subscribers {
		targets = append(targets, s)
	}
	b.subscribers = make(map[int64]*subscriber)
	b.mu.Unlock()

	for _, s := range targets {
		s.once.Do(func() {
			close(s.cancel)
			close(s.ch)
		})
	}
}

// Dropped returns the total number of events dropped across all
// subscribers since the broadcaster started.
func (b *Broadcaster) Dropped() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}
