package job

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Rebuilding the cache from the log.
//
// Drop every state table, sort the union of every replica file by
// (ts, rep, seq), apply in order, and record each file's size as the
// watermark — all in one transaction, so a cache never records that it applied
// a file it did not.
//
// Two things happen to the event stream before it reaches apply, and both stay
// out of apply itself, which must remain a dumb total function. A `rekeyed`
// event renames one replica's task, so every envelope from that replica naming
// the old id is rewritten to name the new one. And two `created` events for
// one short id from different replicas are a collision: the rebuild fails and
// names both, because no automatic remap is safe once an id is in notes and
// commit messages (project/2026-09-01-git-native-event-log.md, "Short ids
// under independent minting").

// CollisionError reports one short id created independently on two replicas.
type CollisionError struct {
	ShortID  string
	KeepRep  string
	KeepName string
	LoseRep  string
	LoseName string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf(
		"short id %s was created on two replicas: %s holds %q and %s holds %q.\n"+
			"The earlier replica keeps the id. To give the other task a fresh one, run:\n"+
			"    job rekey %s:%s",
		e.ShortID, e.KeepRep, e.KeepName, e.LoseRep, e.LoseName, e.LoseRep, e.ShortID,
	)
}

// RebuildReport is what `job rebuild` did.
type RebuildReport struct {
	Rep     string
	Files   int
	Events  int
	Repairs []string
	// Refused is set when the legacy rule stopped the rebuild.
	Refused bool
	Notice  string
}

// RunRebuild forces a full rebuild of the cache from the log.
//
// It refuses a cache that still holds pre-store history, for the same reason
// the open path does: those rows carry state no log line reproduces, and
// dropping the tables would destroy it. Adoption is what lifts the refusal.
func RunRebuild(db *sql.DB) (*RebuildReport, error) {
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	local, err := LoadLocalState(path)
	if err != nil {
		return nil, err
	}
	report := &RebuildReport{Rep: local.Rep}

	legacy, err := hasLegacyRows(db)
	if err != nil {
		return nil, err
	}
	if legacy {
		report.Refused = true
		report.Notice = legacyNotice
		return report, nil
	}

	marks, err := readWatermarks(db)
	if err != nil {
		return nil, err
	}
	repairs, err := rebuildAndReconcile(db, path, marks, local.Rep)
	if err != nil {
		return nil, err
	}
	report.Repairs = repairs

	files, err := eventlog.Files(eventlog.StoreDir(path))
	if err != nil {
		return nil, err
	}
	report.Files = len(files)
	if report.Events, err = StoreEventCount(path); err != nil {
		return nil, err
	}
	return report, nil
}

// rebuildAndReconcile replays the log into the cache and, when the replay
// ingested another replica's events, repairs the invariants a single replica
// would have kept.
func rebuildAndReconcile(db *sql.DB, path string, prevMarks map[string]int64, ourRep string) ([]string, error) {
	foreign, err := foreignFilesGrew(path, prevMarks, ourRep)
	if err != nil {
		return nil, err
	}
	if err := rebuildStore(db, path); err != nil {
		return nil, err
	}
	if !foreign {
		return nil, nil
	}
	// The lock is released by now: reconcile appends through commit, which
	// takes it, and the lock does not nest.
	return reconcile(db)
}

// foreignFilesGrew reports whether any replica file that is not ours is longer
// than the offset the cache applied — the signal that this open ingested
// somebody else's events.
func foreignFilesGrew(path string, marks map[string]int64, ourRep string) (bool, error) {
	files, err := eventlog.Files(eventlog.StoreDir(path))
	if err != nil {
		return false, err
	}
	for _, f := range files {
		if f.Rep == ourRep {
			continue
		}
		if mark, ok := marks[f.Rep]; !ok || f.Size != mark {
			return true, nil
		}
	}
	return false, nil
}

// rebuildStore replays every log file into the cache under the store lock.
func rebuildStore(db *sql.DB, path string) error {
	lock, err := eventlog.AcquireLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()

	files, err := eventlog.Files(eventlog.StoreDir(path))
	if err != nil {
		return err
	}
	var events []eventlog.Envelope
	for _, f := range files {
		evs, err := eventlog.ReadFile(f.Path)
		if err != nil {
			return err
		}
		// Before anything is applied: a file from the future stops the whole
		// rebuild, because applying the rest would leave a cache that looks
		// complete and is not (store_format.go).
		if err := checkStoreFormat(f.Path, evs); err != nil {
			return err
		}
		events = append(events, evs...)
	}
	eventlog.Sort(events)

	applyRekeys(events)
	if err := detectCollisions(events); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rebuildFromInTx(tx, events); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM log_watermarks"); err != nil {
		return err
	}
	for _, f := range files {
		if err := setWatermark(tx, f.Rep, f.Size); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return observeLogClock(path, events)
}

// observeLogClock raises the clock's high-water mark to the newest event in
// the log.
//
// The hybrid clock is max(wall, last_seen + 1) where last_seen is the largest
// ts this replica has READ or written — reading is half of it. Without this a
// replica that pulls a log stamped ahead of its own wall clock mints events
// that sort BEFORE the events which caused them, and the global order stops
// meaning anything: reconcile's `released` can sort ahead of the claim it
// released, and the next merge reads the pair as a fresh conflict and flips
// the holder. Foreign events reach this replica only through a rebuild, so
// this is the one place that has to observe them.
//
// events is already sorted by (ts, rep, seq), so the last one is the newest.
// It runs with the store lock held, which is why it reads and writes
// local.json directly: UpdateLocalState takes the same lock, and flock does
// not nest.
func observeLogClock(path string, events []eventlog.Envelope) error {
	if len(events) == 0 {
		return nil
	}
	newest := events[len(events)-1].TS
	state, err := LoadLocalState(path)
	if err != nil {
		return err
	}
	if state.LastSeen >= newest {
		return nil
	}
	state.LastSeen = newest
	return state.Save(path)
}

// RekeyedPayload gives one replica's task a fresh short id after a
// cross-replica collision. It names the replica whose events are remapped, so
// every machine that reads the log converges on the same rename without a
// second decision.
type RekeyedPayload struct {
	Rep   string `json:"rep"`
	OldID string `json:"old_id"`
	NewID string `json:"new_id"`
}

// applyRekeys rewrites, in place, every envelope from a rekeyed replica that
// names the old id — the task field and any reference inside the payload.
//
// The rewrite is a pre-pass over the whole ordered stream rather than a fold
// that starts at the `rekeyed` event's position. It has to be: the event the
// rekey exists to disentangle is the `created`, which is always earlier, so a
// mapping that only applied from the rekey's position forward would leave the
// collision in place and the rebuild would still fail.
func applyRekeys(events []eventlog.Envelope) {
	renames := map[string]map[string]string{}
	for _, e := range events {
		if EventType(e.Type) != EventRekeyed {
			continue
		}
		var p RekeyedPayload
		if err := decodeEventPayload(e, &p); err != nil || p.Rep == "" || p.OldID == "" || p.NewID == "" {
			continue
		}
		if renames[p.Rep] == nil {
			renames[p.Rep] = map[string]string{}
		}
		renames[p.Rep][p.OldID] = p.NewID
	}
	if len(renames) == 0 {
		return
	}
	for i := range events {
		e := &events[i]
		if EventType(e.Type) == EventRekeyed {
			continue
		}
		m := renames[e.Rep]
		if len(m) == 0 {
			continue
		}
		if to, ok := m[e.Task]; ok {
			e.Task = to
		}
		if len(e.Data) == 0 {
			continue
		}
		var decoded any
		if err := json.Unmarshal(e.Data, &decoded); err != nil {
			continue
		}
		rewritten, changed := rewriteStrings(decoded, m)
		if !changed {
			continue
		}
		if raw, err := json.Marshal(rewritten); err == nil {
			e.Data = raw
		}
	}
}

// rewriteStrings replaces every string in a decoded payload that is exactly a
// renamed short id. Short ids are opaque tokens and never appear as a
// substring of another id, so an exact match is the whole rule.
func rewriteStrings(v any, m map[string]string) (any, bool) {
	switch t := v.(type) {
	case string:
		if to, ok := m[t]; ok {
			return to, true
		}
		return t, false
	case []any:
		changed := false
		for i, item := range t {
			next, c := rewriteStrings(item, m)
			t[i] = next
			changed = changed || c
		}
		return t, changed
	case map[string]any:
		changed := false
		for k, item := range t {
			next, c := rewriteStrings(item, m)
			t[k] = next
			changed = changed || c
		}
		return t, changed
	default:
		return v, false
	}
}

// detectCollisions fails the rebuild when one short id was created on two
// replicas. The first `created` in global order keeps the id.
func detectCollisions(events []eventlog.Envelope) error {
	type origin struct{ rep, title string }
	seen := map[string]origin{}
	for _, e := range events {
		if EventType(e.Type) != EventCreated {
			continue
		}
		var p CreatedPayload
		if err := decodeEventPayload(e, &p); err != nil {
			return err
		}
		if p.ShortID == "" {
			continue
		}
		prior, ok := seen[p.ShortID]
		if !ok {
			seen[p.ShortID] = origin{rep: e.Rep, title: p.Title}
			continue
		}
		if prior.rep != e.Rep {
			return &CollisionError{
				ShortID:  p.ShortID,
				KeepRep:  prior.rep,
				KeepName: prior.title,
				LoseRep:  e.Rep,
				LoseName: p.Title,
			}
		}
	}
	return nil
}
