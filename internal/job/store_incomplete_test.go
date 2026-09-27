package job

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// A bootstrap that refuses to write a replica's file leaves the cache holding
// that replica's events with no file behind them. Nothing may then treat the
// store as in sync, mint a seq the cache already holds, or replay the log over
// the cache: each of those loses or duplicates history the cache alone has.

// refusedBootstrap is one way the bootstrap declines to write a file.
type refusedBootstrap struct {
	name string
	// spoil turns this replica's cached run into one the bootstrap refuses.
	spoil func(t *testing.T, db *sql.DB, rep string)
}

var refusedBootstraps = []refusedBootstrap{
	{
		name: "gapped",
		// Purge erases event rows, so a cache can hold seq 1 and 3 but not 2.
		spoil: func(t *testing.T, db *sql.DB, rep string) {
			t.Helper()
			if _, err := db.Exec("DELETE FROM events WHERE rep = ? AND seq = 2", rep); err != nil {
				t.Fatalf("open a gap: %v", err)
			}
		},
	},
	{
		name: "under-declared",
		// A format-1 declaration ahead of a line format 1 does not have.
		spoil: func(t *testing.T, db *sql.DB, rep string) {
			t.Helper()
			stale, err := json.Marshal(ReplicaPayload{Label: "old-label"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE events SET detail = ? WHERE rep = ? AND event_type = ?", string(stale), rep, string(EventReplica)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE events SET event_type = ? WHERE rep = ? AND event_type = ?", string(EventImported), rep, string(EventCreated)); err != nil {
				t.Fatal(err)
			}
		},
	},
}

// storeWithUnwritableRun builds a cache holding this replica's events with no
// log file and no watermark, spoiled so the bootstrap will not write them out,
// and returns it reopened.
func storeWithUnwritableRun(t *testing.T, how refusedBootstrap) (db *sql.DB, dir, rep string, sync *StoreSync) {
	t.Helper()
	dir = t.TempDir()
	db = storeAt(t, dir)
	for _, title := range []string{"first", "second", "third"} {
		if _, err := RunAdd(db, "", title, "", "", nil, "ben"); err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
	}
	rep = repOf(t, dir)
	how.spoil(t, db, rep)
	cache := filepath.Join(dir, ".jobs.db")
	if err := os.RemoveAll(eventlog.LogDir(eventlog.StoreDir(cache))); err != nil {
		t.Fatalf("remove log dir: %v", err)
	}
	if _, err := db.Exec("DELETE FROM log_watermarks"); err != nil {
		t.Fatalf("clear watermarks: %v", err)
	}
	db, sync = reopenStore(t, db, dir)
	if _, err := os.Stat(eventlog.LogPath(eventlog.StoreDir(cache), rep)); !os.IsNotExist(err) {
		t.Fatalf("precondition: the bootstrap wrote a file for a %s run", how.name)
	}
	return db, dir, rep, sync
}

// Zero files against zero watermarks is not "in sync" when the cache holds
// events for a replica that has no file.
func TestARefusedBootstrapReportsTheLogIncomplete(t *testing.T) {
	for _, how := range refusedBootstraps {
		t.Run(how.name, func(t *testing.T) {
			notices := captureNotices(t)
			_, _, rep, sync := storeWithUnwritableRun(t, how)
			if sync.State != StoreIncomplete {
				t.Fatalf("state = %q, want %q", sync.State, StoreIncomplete)
			}
			want := filepath.Join(".jobs", "log", rep+".jsonl")
			if !strings.Contains(notices.String(), want) {
				t.Fatalf("the notice does not name the file to restore (%s):\n%s", want, notices.String())
			}
		})
	}
}

// The next write would mint seq 1 from the empty file while the cache already
// holds seq 1: refused, with nothing appended and nothing applied.
func TestAWriteIsRefusedWhileTheCacheHoldsSeqsTheFileDoesNot(t *testing.T) {
	for _, how := range refusedBootstraps {
		t.Run(how.name, func(t *testing.T) {
			db, dir, rep, _ := storeWithUnwritableRun(t, how)
			before := applyDump(t, db)

			_, err := RunAdd(db, "", "would reuse seq 1", "", "", nil, "ben")
			var ahead *CacheAheadOfLogError
			if !errors.As(err, &ahead) {
				t.Fatalf("add err = %v, want a CacheAheadOfLogError", err)
			}
			if ahead.Rep != rep || ahead.FileSeq != 0 || ahead.CacheSeq == 0 {
				t.Fatalf("error = %+v", ahead)
			}
			if !strings.Contains(err.Error(), rep+".jsonl") {
				t.Fatalf("the error does not name the file to restore: %v", err)
			}
			cache := filepath.Join(dir, ".jobs.db")
			if _, err := os.Stat(eventlog.LogPath(eventlog.StoreDir(cache), rep)); !os.IsNotExist(err) {
				t.Fatalf("the refused write created a log file")
			}
			if after := applyDump(t, db); after != before {
				t.Fatalf("the refused write changed the cache")
			}
		})
	}
}

// Rekey appends to the file without applying, so it needs the same guard.
func TestAppendOwnEventIsRefusedWhileTheCacheHoldsSeqsTheFileDoesNot(t *testing.T) {
	db, dir, rep, _ := storeWithUnwritableRun(t, refusedBootstraps[0])
	cache := filepath.Join(dir, ".jobs.db")
	err := appendOwnEvent(db, cache, "ben", EventRekeyed, "", RekeyedPayload{Rep: rep, OldID: "aaaaaa", NewID: "bbbbbb"})
	if _, ok := errors.AsType[*CacheAheadOfLogError](err); !ok {
		t.Fatalf("appendOwnEvent err = %v, want a CacheAheadOfLogError", err)
	}
	if _, err := os.Stat(eventlog.LogPath(eventlog.StoreDir(cache), rep)); !os.IsNotExist(err) {
		t.Fatalf("the refused append created a log file")
	}
}

// `job rebuild` replays only the files, so while a replica's file is missing
// it would drop every event the cache holds for it.
func TestRunRebuildRefusesWhileAReplicasFileIsMissing(t *testing.T) {
	db, _, rep, _ := storeWithUnwritableRun(t, refusedBootstraps[0])
	before := applyDump(t, db)
	report, err := RunRebuild(db)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !report.Refused {
		t.Fatalf("rebuild did not refuse: %+v", report)
	}
	if !strings.Contains(report.Notice, rep+".jsonl") {
		t.Fatalf("notice does not name the missing file: %q", report.Notice)
	}
	if after := applyDump(t, db); after != before {
		t.Fatalf("the refused rebuild changed the cache")
	}
}
