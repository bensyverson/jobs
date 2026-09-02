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

	// label is the name `job init --replica-name` parked in local.json for
	// the replica event this checkout has not written yet. Empty means the
	// default label — hostname and checkout path.
	label string

	// seq is the last sequence number handed out, primed from this replica's
	// log file at the start of the batch.
	seq uint64
}

// newRecorderLocked reads the replica id and the clock watermark from
// local.json beside cachePath, minting a replica id the first time this
// checkout writes.
//
// The caller holds the store lock: the whole span from here to the append and
// the transaction's commit runs under it, so the read-modify-write of
// local.json must not take it again.
func newRecorderLocked(path string) (*recorder, error) {
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
		state.Rep = minted
		rep = minted
		if err := state.Save(path); err != nil {
			return nil, err
		}
	}
	// CurrentNowFunc rather than time.Now: it is the seam the whole package's
	// tests move time with, and task timestamps now come from this clock.
	clock := eventlog.NewClockWith(func() time.Time { return CurrentNowFunc() })
	clock.Load(state.LastSeen)
	return &recorder{rep: rep, clock: clock, cachePath: path, label: state.ReplicaName}, nil
}

// envelope mints the next envelope for this replica. task is a short id, or
// "" for an event that belongs to no task (a purged root's tombstone).
func (r *recorder) envelope(typ EventType, task, actor string, payload any) (eventlog.Envelope, error) {
	seq := r.nextSeq()
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
// The count is primed from the replica's LOG FILE, not from MAX(seq) in the
// cache, and under the store lock the batch holds. The file is the record and
// the appender re-scans it on every batch; the cache's seq has holes, because
// purge erases the purged subtree's event rows. Priming from anything but the
// file would mint a seq the appender then overwrote.
func (r *recorder) nextSeq() uint64 {
	r.seq++
	return r.seq
}

// primeSeq sets the last seq handed out, read from the log file by the caller
// under the store lock.
func (r *recorder) primeSeq(last uint64) { r.seq = last }

// persistLocked writes the clock's watermark back to local.json. Called once
// per batch, after the transaction commits and while the store lock is still
// held, so a crash mid-command cannot leave the clock ahead of the events it
// stamped.
func (r *recorder) persistLocked() error {
	s, err := LoadLocalState(r.cachePath)
	if err != nil {
		return err
	}
	if s.Rep == "" {
		s.Rep = r.rep
	}
	if v := r.clock.Save(); v > s.LastSeen {
		s.LastSeen = v
	}
	return s.Save(r.cachePath)
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

	// needReplica is set by commit when this replica has never announced
	// itself. The announcement is emitted lazily, ahead of the first real
	// event of the batch, so a command that decides to write nothing leaves
	// the log untouched — and so a fresh replica's announcement is seq 1 of
	// its file, before anything it describes.
	needReplica bool
}

// emit mints an envelope for the event and applies it.
func (b *eventBatch) emit(tx dbtx, typ EventType, task, actor string, payload any) error {
	if b.needReplica {
		// Cleared first: the announcement is emitted through this same
		// method, and the flag is what stops it recurring.
		b.needReplica = false
		if err := b.emit(tx, EventReplica, "", actor, newReplicaPayload(b.rec.cachePath, b.rec.label)); err != nil {
			return err
		}
	}
	e, err := b.rec.envelope(typ, task, actor, payload)
	if err != nil {
		return err
	}
	if err := apply(tx, e); err != nil {
		return err
	}
	b.events = append(b.events, e)
	return nil
}

// commit runs one command as a batch of events. The whole span — mint the
// recorder, open the transaction, let fn validate and emit, append to the log
// file, advance the watermark, commit, persist the clock — runs under one
// store lock, so parallel `job` processes on this machine serialize there.
//
// The order is the design's, not an accident. The log is the record, so the
// events reach .jobs/log/<rep>.jsonl BEFORE the transaction that applies them
// commits: a failure to append rolls the whole command back, and a crash
// between the append and the commit leaves the file longer than the watermark,
// which is exactly what the next open rebuilds from. The reverse order would
// let the cache hold a change no log line describes, and no later open could
// tell.
//
// commit never nests: AcquireLock opens a fresh descriptor each time, so two
// locks in one process contend exactly as two processes do.
func commit(db *sql.DB, fn func(tx dbtx, b *eventBatch) error) error {
	path, err := CachePathOf(db)
	if err != nil {
		return err
	}
	lock, err := eventlog.AcquireLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()

	rec, err := newRecorderLocked(path)
	if err != nil {
		return err
	}
	appender, err := eventlog.OpenAppender(eventlog.StoreDir(path), path, rec.rep)
	if err != nil {
		return err
	}
	defer appender.Close()

	// The seq comes from the file, under the lock, so the number minted here
	// and the number AppendLocked assigns are the same number; the check after
	// the append asserts that rather than trusting it.
	last, err := appender.LastSeqLocked()
	if err != nil {
		return err
	}
	rec.primeSeq(last)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// One EXISTS per write, answered false for the whole life of a replica
	// after its first: has this checkout ever said who it is?
	needReplica, err := replicaEventMissing(tx, rec.rep)
	if err != nil {
		return err
	}

	batch := &eventBatch{rec: rec, needReplica: needReplica}
	if err := fn(tx, batch); err != nil {
		return err
	}

	if len(batch.events) > 0 {
		minted := make([]uint64, len(batch.events))
		refs := make([]*eventlog.Envelope, len(batch.events))
		for i := range batch.events {
			minted[i] = batch.events[i].Seq
			refs[i] = &batch.events[i]
		}
		if err := appender.AppendLocked(refs); err != nil {
			return err
		}
		for i, e := range batch.events {
			if e.Seq != minted[i] {
				return fmt.Errorf("log and cache disagree on seq for replica %s: applied %d, appended %d", rec.rep, minted[i], e.Seq)
			}
		}
		size, err := logFileSize(appender.Path())
		if err != nil {
			return err
		}
		if err := setWatermark(tx, rec.rep, size); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return rec.persistLocked()
}
