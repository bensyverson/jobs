package job

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Merging into a cache the store has already adopted.
//
// The across-machines playbook runs `job merge --dry-run` first, and that open
// adopts the local legacy cache: its log now carries the legacy history and a
// snapshot. The real merge then writes the other side's tail into the cache as
// unpositioned rows, and the next open has to adopt *that* — translate the new
// rows and pin the merged state with a second snapshot — rather than mistake
// the existing snapshot for a retry of an adoption that already appended.

// adoptedThenMerged builds that situation: two legacy copies of one database,
// the local one adopted before the merge, both written to, then merged. It
// returns the merged cache's history dump (taken before the close) and both
// paths, so the caller can reopen and see what adoption does.
func adoptedThenMerged(t *testing.T) (merged string, localPath, otherPath string) {
	t.Helper()
	clock := newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	here, there := filepath.Join(dir, "here"), filepath.Join(dir, "there")
	for _, d := range []string{here, there} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var rootID string
	localPath = legacyCache(t, here, func(db *sql.DB) {
		rootID = MustAdd(t, db, "", "Shared root")
	})
	otherPath = filepath.Join(there, "legacy.db")
	copyDBFile(t, localPath, otherPath)

	// The dry run's open: the local cache is adopted before any merge.
	local, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("adopt local: %v", err)
	}
	clock.advance(time.Minute)
	MustAdd(t, local, rootID, "Only over here")

	// The other copy is written as a legacy cache, then opened the way merge
	// stages it — through OpenDB, which adopts the staged copy.
	other, err := OpenDB(otherPath)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	clock.advance(time.Minute)
	MustAdd(t, other, rootID, "Only over there")
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}

	clock.advance(time.Minute)
	if _, err := RunMerge(local, otherPath, false); err != nil {
		t.Fatalf("merge: %v", err)
	}
	merged = mustDump(t, local)
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	clock.advance(time.Minute)
	return merged, localPath, otherPath
}

// The Hirewell case, 2026-09-02: the reopen after the merge must adopt the
// merged tail, and the cache it leaves behind is the merged one.
func TestAdopt_MergeIntoAnAdoptedCacheAdoptsTheTail(t *testing.T) {
	merged, localPath, _ := adoptedThenMerged(t)

	reopened, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	if diff, err := os.ReadFile(localPath + adoptDiffSuffix); err == nil {
		t.Fatalf("adoption of the merged cache refused:\n%s", diff)
	}
	if n := countLegacyRows(t, reopened); n != 0 {
		t.Errorf("the merged rows should have been adopted, %d are still unpositioned", n)
	}
	if got := mustDump(t, reopened); got != merged {
		t.Errorf("the adopted cache is not the merged one:\n%s", firstDiff(merged, got))
	}
	if !strings.Contains(mustDump(t, reopened), "Only over there") {
		t.Error("the other side's task did not survive adoption")
	}
	if sync := StoreSyncOf(reopened); sync == nil || sync.State != StoreInSync {
		t.Errorf("expected the reopened cache to be in sync, got %+v", sync)
	}
}

// The merged state lands as a second snapshot, after everything the log held,
// and the transcribed rows as legacy lines. A `job rebuild` from the log alone
// then reproduces the merged cache, which is what makes the log the record.
func TestAdopt_MergeTailIsPinnedByASecondSnapshot(t *testing.T) {
	merged, localPath, _ := adoptedThenMerged(t)

	reopened, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened.Close()

	lines, err := eventlog.ReadAll(eventlog.StoreDir(localPath))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots int
	for _, e := range lines {
		if EventType(e.Type) == EventSnapshot {
			snapshots++
		}
	}
	if snapshots != 2 {
		t.Errorf("the log should hold the adoption snapshot and the merge snapshot, found %d", snapshots)
	}

	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("rebuild from the log: %v", err)
	}
	defer rebuilt.Close()
	if got := mustDump(t, rebuilt); got != merged {
		t.Errorf("a rebuild from the log is not the merged cache:\n%s", firstDiff(merged, got))
	}
}

// A snapshot is one replica's compaction of state it already holds, and a
// replica event names one checkout; neither is shared history. Merge must not
// transcribe the other side's into this cache, where adoption would carry them
// into this replica's log as history that never happened here.
func TestMerge_DoesNotTranscribeTheOtherReplicasBookkeeping(t *testing.T) {
	_, localPath, _ := adoptedThenMerged(t)

	db, err := OpenDBForRecovery(localPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events
		WHERE rep = '' AND event_type IN ('snapshot', 'replica')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("merge transcribed %d of the other replica's bookkeeping events", n)
	}
}

// Once the merge is adopted, this side's events are ordered by log position
// and the other copy's by its own, so the two histories may still agree
// positionally for a long run before they part. That run is not a shared
// prefix to merge against: everything the other side holds is already here,
// and the report has to say so rather than list every task as touched.
func TestMerge_ReMergeAfterAdoptionReportsAlreadyMerged(t *testing.T) {
	_, localPath, otherPath := adoptedThenMerged(t)

	reopened, err := OpenDB(localPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	before := logicalDump(t, reopened)
	report, err := RunMerge(reopened, otherPath, false)
	if err != nil {
		t.Fatalf("re-merge: %v", err)
	}
	if !report.AlreadyMerged {
		t.Errorf("the re-merge should report already merged, got:\n%s", report.Markdown())
	}
	if report.Changed {
		t.Error("a re-merge changes nothing")
	}
	if after := logicalDump(t, reopened); after != before {
		t.Error("the re-merge wrote to the database")
	}
}
