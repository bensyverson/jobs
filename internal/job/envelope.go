package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Minting envelopes for this checkout.
//
// A command handler no longer writes the state tables. It validates against
// the cache, builds the events it means, and hands each to apply — which is
// the only writer (project/2026-09-01-git-native-event-log.md, "Apply").
// This file is the middle step: turning (type, task, actor, payload) into a
// positioned eventlog.Envelope.
//
// The position has three parts. `rep` is this checkout's replica id, minted
// once and kept in .jobs/local.json. `ts` is the hybrid logical clock in
// milliseconds, max(wall, last_seen+1), whose watermark is persisted to the
// same file after every batch. `seq` is per-replica and gapless from 1.

// recorder holds one command's minting state: the replica id, the clock, and
// the path of the cache whose local.json they came from.
type recorder struct {
	rep       string
	clock     *eventlog.Clock
	cachePath string

	// seq is the last sequence number handed out, primed from the cache on
	// first use.
	seq    uint64
	primed bool
}

// newRecorder reads the replica id and the clock watermark from local.json
// beside db's file, minting a replica id the first time this checkout writes.
//
// It runs before the transaction opens: minting takes the store lock, and
// taking a file lock while holding a SQLite write transaction would let two
// processes deadlock across the two locks.
func newRecorder(db dbtx) (*recorder, error) {
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	state, err := LoadLocalState(path)
	if err != nil {
		return nil, err
	}
	rep := state.Rep
	if rep == "" {
		minted, err := eventlog.NewReplicaID()
		if err != nil {
			return nil, err
		}
		// Another process may have minted one between the read and the lock;
		// whoever wrote first wins, so the id never changes under a reader.
		if err := UpdateLocalState(path, func(s *LocalState) error {
			if s.Rep == "" {
				s.Rep = minted
			}
			rep = s.Rep
			return nil
		}); err != nil {
			return nil, err
		}
	}
	// CurrentNowFunc rather than time.Now: it is the seam the whole package's
	// tests move time with, and task timestamps now come from this clock.
	clock := eventlog.NewClockWith(func() time.Time { return CurrentNowFunc() })
	clock.Load(state.LastSeen)
	return &recorder{rep: rep, clock: clock, cachePath: path}, nil
}

// envelope mints the next envelope for this replica. task is a short id, or
// "" for an event that belongs to no task (a purged root's tombstone).
func (r *recorder) envelope(tx dbtx, typ EventType, task, actor string, payload any) (eventlog.Envelope, error) {
	seq, err := r.nextSeq(tx)
	if err != nil {
		return eventlog.Envelope{}, err
	}
	var data json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return eventlog.Envelope{}, fmt.Errorf("marshal %s payload: %w", typ, err)
		}
		data = b
	}
	return eventlog.Envelope{
		V:     eventlog.Version,
		Rep:   r.rep,
		Seq:   seq,
		TS:    r.clock.Now(),
		Actor: actor,
		Type:  eventlog.Type(typ),
		Task:  task,
		Data:  data,
	}, nil
}

// nextSeq hands out the next gapless sequence number for this replica.
//
// It is derived from the cache — MAX(seq) for this rep — because the cache is
// still where this replica's events live. The store leaf (the log files and
// eventlog.Appender) replaces this: the appender re-scans the replica's file
// under the store lock on every batch, which is what stops a second process
// repeating a seq. Priming once per batch rather than once per process is the
// same discipline, one level down.
func (r *recorder) nextSeq(tx dbtx) (uint64, error) {
	if !r.primed {
		var max sql.NullInt64
		if err := tx.QueryRow("SELECT MAX(seq) FROM events WHERE rep = ?", r.rep).Scan(&max); err != nil {
			return 0, fmt.Errorf("read last seq for replica %s: %w", r.rep, err)
		}
		if max.Valid && max.Int64 > 0 {
			r.seq = uint64(max.Int64)
		}
		r.primed = true
	}
	r.seq++
	return r.seq, nil
}

// persist writes the clock's watermark back to local.json. Called once per
// batch, after the transaction commits, so a crash mid-command cannot leave
// the clock ahead of the events it stamped.
func (r *recorder) persist() error {
	return UpdateLocalState(r.cachePath, func(s *LocalState) error {
		if s.Rep == "" {
			s.Rep = r.rep
		}
		if v := r.clock.Save(); v > s.LastSeen {
			s.LastSeen = v
		}
		return nil
	})
}

// eventBatch collects the events one command means, applying each as it is
// emitted.
//
// Applying immediately rather than at the end of the batch is deliberate: the
// cascade planning in `done` and `cancel` reads the tree back to decide
// whether an ancestor's last open child has just closed, exactly as it read
// its own writes before this refactor. The events it emits are still the
// whole record of the command, in order, which is what a rebuild replays.
type eventBatch struct {
	rec    *recorder
	events []eventlog.Envelope
}

// emit mints an envelope for the event and applies it.
func (b *eventBatch) emit(tx dbtx, typ EventType, task, actor string, payload any) error {
	e, err := b.rec.envelope(tx, typ, task, actor, payload)
	if err != nil {
		return err
	}
	if err := apply(tx, e); err != nil {
		return err
	}
	b.events = append(b.events, e)
	return nil
}

// commit runs one command as a batch of events: it mints the recorder, opens
// the transaction, lets fn validate and emit, commits, and persists the clock.
//
// The store lock goes around this whole span once the log files exist — the
// append to .jobs/log/<rep>.jsonl belongs between the recorder and the
// transaction, and the watermark update belongs inside it. Until then, SQLite's
// own locking serializes concurrent `job` processes on one machine.
func commit(db *sql.DB, fn func(tx dbtx, b *eventBatch) error) error {
	rec, err := newRecorder(db)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	batch := &eventBatch{rec: rec}
	if err := fn(tx, batch); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return rec.persist()
}

// batchInTx builds an eventBatch on a transaction that is already open.
//
// commit() is the shape every handler should have, and every handler that has
// been moved onto apply uses it. This exists for the few that still open
// their own transaction — the label and criteria handlers, which the
// relations leaf is moving — because the claims family's auto-extend runs
// inside them and now has to emit a real, positioned heartbeat.
//
// Two things the caller owes it that commit() would have done: persist the
// clock watermark after the transaction commits (batch.persist), and, once
// the log files exist, take the store lock around the whole span. Delete this
// when the last handler is on commit().
func batchInTx(tx dbtx) (*eventBatch, error) {
	rec, err := newRecorder(tx)
	if err != nil {
		return nil, err
	}
	return &eventBatch{rec: rec}, nil
}

// persist writes the batch's clock watermark back to local.json. Only the
// batchInTx callers need it; commit() does this for its own batch.
func (b *eventBatch) persist() error { return b.rec.persist() }
