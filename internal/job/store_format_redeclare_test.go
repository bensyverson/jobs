package job

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Re-declaring the store format.
//
// A file declares its format only through `replica` events, so a file opened
// by an older binary goes on declaring that older format however many newer
// event types a newer binary appends to it — and an older binary then replays
// it happily, applying the types it does not know as no-ops. The fix is that a
// binary about to append to a file declaring less than StoreFormat first
// appends a `replica` marker declaring the current format.

// forgeOwnFormat1Store stands up a store whose OWN log file was written before
// the format existed: local.json names the replica, and its file opens with a
// `replica` event carrying no format field, followed by one ordinary event.
func forgeOwnFormat1Store(t *testing.T) (*replica, string) {
	t.Helper()
	quietNotices(t)
	r := &replica{t: t, name: "A", dir: t.TempDir()}
	rep, err := eventlog.NewReplicaID()
	if err != nil {
		t.Fatalf("mint replica id: %v", err)
	}
	if err := UpdateLocalState(r.cache(), func(s *LocalState) error {
		s.Rep = rep
		return nil
	}); err != nil {
		t.Fatalf("write local state: %v", err)
	}
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(r.cache()), r.cache(), rep)
	if err != nil {
		t.Fatalf("open appender: %v", err)
	}
	defer ap.Close()
	announce, err := json.Marshal(ReplicaPayload{Label: "old-label", Host: "old-host"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := json.Marshal(CreatedPayload{ShortID: "Old01", Title: "Old work", SortKey: "mmmmmm"})
	if err != nil {
		t.Fatal(err)
	}
	ts := CurrentNowFunc().UnixMilli()
	evs := []*eventlog.Envelope{
		{TS: ts, Actor: "sam", Type: eventlog.Type(EventReplica), Data: announce},
		{TS: ts + 1, Actor: "sam", Type: eventlog.Type(EventCreated), Task: "Old01", Data: created},
	}
	if err := ap.Append(evs); err != nil {
		t.Fatalf("append: %v", err)
	}
	if got := fileStoreFormat(r.ownEvents(rep)); got != 1 {
		t.Fatalf("the forged file declares format %d, want 1", got)
	}
	return r, rep
}

// ownEvents is one replica's file, in seq order.
func (r *replica) ownEvents(rep string) []eventlog.Envelope {
	r.t.Helper()
	var out []eventlog.Envelope
	for _, e := range r.logEvents() {
		if e.Rep == rep {
			out = append(out, e)
		}
	}
	// logEvents sorts by position, and within one replica the hybrid clock
	// makes that seq order; assert it rather than assume it.
	for i := 1; i < len(out); i++ {
		if out[i].Seq <= out[i-1].Seq {
			r.t.Fatalf("replica %s's events are not in seq order", rep)
		}
	}
	return out
}

func replicaEvents(t *testing.T, evs []eventlog.Envelope) []ReplicaPayload {
	t.Helper()
	var out []ReplicaPayload
	for _, e := range evs {
		if EventType(e.Type) != EventReplica {
			continue
		}
		var p ReplicaPayload
		if err := json.Unmarshal(e.Data, &p); err != nil {
			t.Fatalf("decode replica payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// The first append to a format-1 file is preceded, in the same batch, by a
// marker declaring the current format under the file's existing label.
func TestAppendToAnOlderFormatFileRedeclaresTheFormat(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	var id string
	r.do(func(db *sql.DB) { id = MustAdd(t, db, "", "New work") })

	evs := r.ownEvents(rep)
	if len(evs) != 4 {
		t.Fatalf("own file holds %d events, want 4 (announce, created, marker, created)", len(evs))
	}
	marker, next := evs[2], evs[3]
	if EventType(marker.Type) != EventReplica {
		t.Fatalf("event before the new one is %q, want a replica marker", marker.Type)
	}
	var p ReplicaPayload
	if err := json.Unmarshal(marker.Data, &p); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	if p.Format != StoreFormat {
		t.Fatalf("marker declares format %d, want %d", p.Format, StoreFormat)
	}
	if p.Label != "old-label" {
		t.Fatalf("marker label = %q, want the file's existing %q", p.Label, "old-label")
	}
	if EventType(next.Type) != EventCreated || next.Task != id {
		t.Fatalf("event after the marker is %q on %q, want created on %q", next.Type, next.Task, id)
	}
	if next.Seq != marker.Seq+1 || next.TS <= marker.TS {
		t.Fatalf("marker (seq %d, ts %d) does not immediately precede the event (seq %d, ts %d)",
			marker.Seq, marker.TS, next.Seq, next.TS)
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("file now declares format %d, want %d", got, StoreFormat)
	}
}

// Once declared, the file stays declared: a second write adds no marker.
func TestRedeclaringTheFormatHappensOnce(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "First") })
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "Second") })

	if n := len(replicaEvents(t, r.ownEvents(rep))); n != 2 {
		t.Fatalf("own file holds %d replica events after two writes, want 2 (the original and one marker)", n)
	}
}

// A file this binary opened already declares the current format and never
// gains a marker.
func TestAppendToACurrentFormatFileAddsNoMarker(t *testing.T) {
	r := newReplicaWithWork(t)
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "More work") })

	if n := len(replicaEvents(t, r.logEvents())); n != 1 {
		t.Fatalf("log holds %d replica events, want only the opening one", n)
	}
}

// A rename is itself a replica event at the current format, so it needs no
// marker in front of it.
func TestRenamingAnOlderFormatFileAddsNoSeparateMarker(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	r.do(func(db *sql.DB) {
		if _, err := RunReplicaRename(db, "new-label", TestActor); err != nil {
			t.Fatalf("rename: %v", err)
		}
	})
	got := replicaEvents(t, r.ownEvents(rep))
	if len(got) != 2 {
		t.Fatalf("own file holds %d replica events, want 2 (the original and the rename)", len(got))
	}
	if got[1].Label != "new-label" || got[1].Format != StoreFormat {
		t.Fatalf("last replica event = %+v, want the rename at format %d", got[1], StoreFormat)
	}
}

// The point of the marker: a binary older than the event types now in the
// file refuses it. Before this change it read the file as format 1 and
// replayed it.
func TestAnOlderBinaryRefusesARedeclaredFile(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "New work") })

	older := StoreFormat - 1
	err := checkStoreFormatFor(older, rep+".jsonl", r.ownEvents(rep))
	var ahead *StoreFormatAheadError
	if !errors.As(err, &ahead) {
		t.Fatalf("a format-%d binary got %v, want *StoreFormatAheadError", older, err)
	}
	if ahead.LogFormat != StoreFormat || ahead.BinaryFormat != older {
		t.Fatalf("error carries log %d / binary %d, want %d / %d", ahead.LogFormat, ahead.BinaryFormat, StoreFormat, older)
	}
	if err := checkStoreFormatFor(StoreFormat, rep+".jsonl", r.ownEvents(rep)); err != nil {
		t.Fatalf("this binary refuses its own file: %v", err)
	}
}

// The marker is an ordinary event: the cache that applied it as it was written
// equals the cache rebuilt from the log, and `job replicas` reports the new
// format from either.
func TestRedeclaredFileRebuildsToTheSameCache(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	r.do(func(db *sql.DB) { MustAdd(t, db, "", "New work") })

	before := r.dump()
	beforeInfo := r.localReplicaInfo(rep)
	removeCache(r.cache())
	after := r.dump()
	afterInfo := r.localReplicaInfo(rep)

	if before != after {
		t.Fatalf("rebuild differs from the live cache:\n--- live\n%s\n--- rebuilt\n%s", before, after)
	}
	if beforeInfo != afterInfo {
		t.Fatalf("replica info differs: live %+v, rebuilt %+v", beforeInfo, afterInfo)
	}
	if afterInfo.Format != StoreFormat || afterInfo.Label != "old-label" {
		t.Fatalf("replica info = %+v, want format %d under label old-label", afterInfo, StoreFormat)
	}
}

func (r *replica) localReplicaInfo(rep string) ReplicaInfo {
	r.t.Helper()
	var out ReplicaInfo
	r.do(func(db *sql.DB) {
		list, err := RunReplicas(db)
		if err != nil {
			r.t.Fatalf("RunReplicas: %v", err)
		}
		for _, info := range list {
			if info.Rep == rep {
				out = info
				out.LastEvent = 0
				return
			}
		}
		r.t.Fatalf("replica %s is not listed", rep)
	})
	return out
}
