package job

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The store format on the hot path.
//
// The rebuild reads every file and refuses one declaring a format this binary
// does not know. But a cache a newer binary has already rebuilt in place has
// watermarks that match the files, so an older binary opening it never reads
// a line — it stats, finds the cache in sync, and appends under its older
// vocabulary. The cache holds every `replica` event it applied, so the open
// reads their declarations from there instead.

// applyAsNewerBinary does to r's cache what a newer binary's rebuild would:
// applies every line of rep's file and advances rep's watermark to the file's
// end, so the next open finds the cache in sync without reading the file.
func (r *replica) applyAsNewerBinary(rep string) {
	r.t.Helper()
	path := eventlog.LogPath(eventlog.StoreDir(r.cache()), rep)
	events, err := eventlog.ReadFile(path)
	if err != nil {
		r.t.Fatalf("read %s: %v", path, err)
	}
	size, err := logFileSize(path)
	if err != nil {
		r.t.Fatal(err)
	}
	db, err := sql.Open("sqlite", r.cache())
	if err != nil {
		r.t.Fatalf("open raw cache: %v", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		r.t.Fatal(err)
	}
	defer tx.Rollback()
	for _, e := range events {
		if err := apply(tx, e); err != nil {
			r.t.Fatalf("apply %s: %v", e.Type, err)
		}
	}
	if err := setWatermark(tx, rep, size); err != nil {
		r.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		r.t.Fatal(err)
	}
}

// appendReplicaLine appends one more `replica` event declaring format to rep's
// file, as a later writer of that file would.
func (r *replica) appendReplicaLine(rep string, format StoreFormatVersion) {
	r.t.Helper()
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(r.cache()), r.cache(), rep)
	if err != nil {
		r.t.Fatalf("open appender for %s: %v", rep, err)
	}
	defer ap.Close()
	payload, err := json.Marshal(ReplicaPayload{Label: "forged", Format: format})
	if err != nil {
		r.t.Fatal(err)
	}
	e := eventlog.Envelope{
		TS:    CurrentNowFunc().UnixMilli(),
		Actor: "sam",
		Type:  eventlog.Type(EventReplica),
		Data:  payload,
	}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		r.t.Fatalf("append: %v", err)
	}
}

func wantFormatAhead(t *testing.T, err error, rep string) {
	t.Helper()
	if err == nil {
		t.Fatal("opening a cache built from a log ahead of this binary succeeded; it must refuse")
	}
	var ahead *StoreFormatAheadError
	if !errors.As(err, &ahead) {
		t.Fatalf("error is %T (%v), want *StoreFormatAheadError", err, err)
	}
	if ahead.LogFormat != StoreFormat+1 || ahead.BinaryFormat != StoreFormat {
		t.Fatalf("error carries log %d / binary %d, want %d / %d",
			ahead.LogFormat, ahead.BinaryFormat, StoreFormat+1, StoreFormat)
	}
	if !strings.Contains(err.Error(), rep+".jsonl") {
		t.Fatalf("message %q does not name the file %s.jsonl", err.Error(), rep)
	}
}

// A cache a newer binary rebuilt is in sync, and still refused.
func TestHotPathRefusesACacheBuiltFromALogAheadOfTheBinary(t *testing.T) {
	r := newReplicaWithWork(t)
	rep := r.forgeReplicaFile(StoreFormat + 1)
	r.applyAsNewerBinary(rep)

	wantFormatAhead(t, r.tryOpen(), rep)
}

// The refusal reads the highest format the file ever declared, as the
// rebuild's does: a later line declaring less does not undo what the newer
// binary wrote before it.
func TestHotPathRefusesACacheWhoseFileWasDeclaredDownward(t *testing.T) {
	r := newReplicaWithWork(t)
	rep := r.forgeReplicaFile(StoreFormat + 1)
	r.appendReplicaLine(rep, 1)
	r.applyAsNewerBinary(rep)

	wantFormatAhead(t, r.tryOpen(), rep)
}

// A replica the cache holds with no file on disk is written back out from the
// cache. A binary too old for what that cache holds refuses first, rather than
// copying lines it cannot read into the log.
func TestOpenRefusesBeforeBootstrappingAReplicaAheadOfTheBinary(t *testing.T) {
	r := newReplicaWithWork(t)
	rep := r.forgeReplicaFile(StoreFormat + 1)
	r.applyAsNewerBinary(rep)
	path := eventlog.LogPath(eventlog.StoreDir(r.cache()), rep)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	wantFormatAhead(t, r.tryOpen(), rep)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the refused open wrote %s back out (stat err %v)", path, err)
	}
}
