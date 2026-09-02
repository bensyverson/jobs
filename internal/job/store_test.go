package job

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The store: the log files are the record and .jobs.db is a cache of them.
//
// These tests exercise the four things that has to mean on every open — a
// fresh clone builds, a cache in sync costs a stat, a file that grew is
// replayed, and a legacy cache is left alone — plus the append-before-apply
// discipline that makes a crash mid-command recoverable.

// storeAt creates a cache (and its store) in dir and closes it at the end of
// the test.
func storeAt(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := CreateDB(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("create store in %s: %v", dir, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// reopenStore closes db and opens the same cache again, returning the sync
// result the open produced.
func reopenStore(t *testing.T, db *sql.DB, dir string) (*sql.DB, *StoreSync) {
	t.Helper()
	db.Close()
	next, err := OpenDB(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("reopen %s: %v", dir, err)
	}
	t.Cleanup(func() { next.Close() })
	sync := StoreSyncOf(next)
	if sync == nil {
		t.Fatalf("reopen %s recorded no store sync", dir)
	}
	return next, sync
}

// repOf returns the replica id local.json holds for the cache in dir.
func repOf(t *testing.T, dir string) string {
	t.Helper()
	s, err := LoadLocalState(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("load local state in %s: %v", dir, err)
	}
	if s.Rep == "" {
		t.Fatalf("no replica id in %s", dir)
	}
	return s.Rep
}

// carryLog copies every log file from one store to another, the way a git
// pull would.
func carryLog(t *testing.T, fromDir, toDir string) {
	t.Helper()
	files, err := eventlog.Files(eventlog.StoreDir(filepath.Join(fromDir, ".jobs.db")))
	if err != nil {
		t.Fatalf("list %s: %v", fromDir, err)
	}
	dest := eventlog.LogDir(eventlog.StoreDir(filepath.Join(toDir, ".jobs.db")))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dest, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		target := filepath.Join(dest, f.Rep+eventlog.LogExt)
		// Each file is append-only and written by exactly one replica, so a
		// merge never shortens one. Copying a stale shorter copy over a longer
		// local one is something git would not do, and would destroy history.
		if info, err := os.Stat(target); err == nil && info.Size() >= int64(len(raw)) {
			continue
		}
		if err := os.WriteFile(target, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", dest, err)
		}
	}
}

// captureNotices redirects the store's notice writer for one test.
func captureNotices(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := StoreNotices
	StoreNotices = &buf
	t.Cleanup(func() { StoreNotices = prev })
	return &buf
}

// watermarkOf reads the cache's applied offset for a replica.
func watermarkOf(t *testing.T, db *sql.DB, rep string) (int64, bool) {
	t.Helper()
	var off int64
	err := db.QueryRow("SELECT offset FROM log_watermarks WHERE rep = ?", rep).Scan(&off)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	return off, true
}

func logSize(t *testing.T, dir, rep string) int64 {
	t.Helper()
	info, err := os.Stat(eventlog.LogPath(eventlog.StoreDir(filepath.Join(dir, ".jobs.db")), rep))
	if err != nil {
		t.Fatalf("stat log for %s: %v", rep, err)
	}
	return info.Size()
}

func TestCommitAppendsToTheLogBeforeTheWatermarkAdvances(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)

	if _, err := RunAdd(db, "", "first", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rep := repOf(t, dir)

	size := logSize(t, dir, rep)
	if size == 0 {
		t.Fatalf("the replica's log file is empty after a write")
	}
	mark, ok := watermarkOf(t, db, rep)
	if !ok {
		t.Fatalf("no watermark for %s after a write", rep)
	}
	if mark != size {
		t.Fatalf("watermark %d != log size %d", mark, size)
	}

	evs, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(filepath.Join(dir, ".jobs.db")), rep))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(evs) == 0 || evs[0].Type != eventlog.Type(EventCreated) {
		t.Fatalf("log does not open with the created event: %+v", evs)
	}
	if evs[0].Seq != 1 {
		t.Fatalf("first line seq = %d, want 1", evs[0].Seq)
	}
}

// A crash between append and apply leaves the file longer than the watermark.
// The next open notices and replays.
func TestAppendWithoutApplyHealsOnTheNextOpen(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "applied", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rep := repOf(t, dir)
	cache := filepath.Join(dir, ".jobs.db")

	// Append a created event the cache never sees, exactly as a process
	// killed between the append and the commit would leave it.
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(cache), cache, rep)
	if err != nil {
		t.Fatalf("open appender: %v", err)
	}
	payload, _ := json.Marshal(CreatedPayload{ShortID: "ghost1", Title: "unapplied", SortKey: "zzzzzz"})
	e := eventlog.Envelope{TS: nowMillisForTest(), Actor: "ben", Type: eventlog.Type(EventCreated), Task: "ghost1", Data: payload}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		t.Fatalf("append: %v", err)
	}
	ap.Close()

	db, sync := reopenStore(t, db, dir)
	if sync.State != StoreRebuilt {
		t.Fatalf("state = %q, want %q", sync.State, StoreRebuilt)
	}
	var title string
	if err := db.QueryRow("SELECT title FROM tasks WHERE short_id = 'ghost1'").Scan(&title); err != nil {
		t.Fatalf("the unapplied event was not replayed: %v", err)
	}
	if title != "unapplied" {
		t.Fatalf("title = %q", title)
	}
	if mark, _ := watermarkOf(t, db, rep); mark != logSize(t, dir, rep) {
		t.Fatalf("watermark not advanced to the file size after the rebuild")
	}
}

func TestAForeignFileAppearingTriggersARebuild(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	dbA := storeAt(t, dirA)
	if _, err := RunAdd(dbA, "", "mine", "", "", nil, "ben"); err != nil {
		t.Fatalf("add A: %v", err)
	}
	dbB := storeAt(t, dirB)
	if _, err := RunAdd(dbB, "", "theirs", "", "", nil, "sam"); err != nil {
		t.Fatalf("add B: %v", err)
	}
	dbB.Close()

	carryLog(t, dirB, dirA)
	dbA, sync := reopenStore(t, dbA, dirA)
	if sync.State != StoreRebuilt {
		t.Fatalf("state = %q, want %q", sync.State, StoreRebuilt)
	}
	var n int
	if err := dbA.QueryRow("SELECT COUNT(*) FROM tasks WHERE title IN ('mine','theirs')").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("after ingesting the foreign file the cache holds %d of the 2 tasks", n)
	}
	if sync.Files != 2 {
		t.Fatalf("Files = %d, want 2", sync.Files)
	}
}

// A cache that still holds pre-store history carries state no log line can
// reproduce, so a rebuild would destroy it. Adoption is what fixes that; until
// then the rule is: never rebuild, say so, and keep working.
func TestALegacyCacheIsNeverRebuilt(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	dbA := storeAt(t, dirA)
	if _, err := RunAdd(dbA, "", "mine", "", "", nil, "ben"); err != nil {
		t.Fatalf("add A: %v", err)
	}
	// One row from before the store existed.
	if _, err := dbA.Exec(
		"INSERT INTO events (task_id, event_type, actor, detail, created_at, rep, seq, ts) VALUES (NULL, 'noted', 'ben', '', 1, '', 0, 1000)",
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	dbB := storeAt(t, dirB)
	if _, err := RunAdd(dbB, "", "theirs", "", "", nil, "sam"); err != nil {
		t.Fatalf("add B: %v", err)
	}
	dbB.Close()
	carryLog(t, dirB, dirA)

	notices := captureNotices(t)
	dbA, sync := reopenStore(t, dbA, dirA)
	if sync.State != StoreLegacy {
		t.Fatalf("state = %q, want %q", sync.State, StoreLegacy)
	}
	var n int
	if err := dbA.QueryRow("SELECT COUNT(*) FROM tasks WHERE title = 'theirs'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("a legacy cache ingested a foreign file; it must not rebuild")
	}
	if !strings.Contains(notices.String(), "predates the store") {
		t.Fatalf("no notice about a legacy database: %q", notices.String())
	}
	// The legacy row survives, and own writes still work.
	if _, err := RunAdd(dbA, "", "later", "", "", nil, "ben"); err != nil {
		t.Fatalf("write against a legacy cache: %v", err)
	}
	if err := dbA.QueryRow("SELECT COUNT(*) FROM events WHERE rep = ''").Scan(&n); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if n != 1 {
		t.Fatalf("legacy rows = %d, want 1", n)
	}
}

// `job rebuild` refuses under the same rule, and says why.
func TestRunRebuildRefusesALegacyCache(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "mine", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO events (task_id, event_type, actor, detail, created_at, rep, seq, ts) VALUES (NULL, 'noted', 'ben', '', 1, '', 0, 1000)",
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	report, err := RunRebuild(db)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if !report.Refused {
		t.Fatalf("rebuild did not refuse a legacy cache: %+v", report)
	}
	if !strings.Contains(report.Notice, "predates the store") {
		t.Fatalf("notice = %q", report.Notice)
	}
}

// A cache written before the log files existed — positioned events, no file —
// has its own history written out rather than lost.
func TestBootstrapWritesThisReplicasFileFromTheCache(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "written before the store", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rep := repOf(t, dir)
	before := applyDump(t, db)

	// Erase every trace of the log, leaving the cache exactly as a database
	// written by the apply leaf's build would look.
	cache := filepath.Join(dir, ".jobs.db")
	if err := os.RemoveAll(eventlog.LogDir(eventlog.StoreDir(cache))); err != nil {
		t.Fatalf("remove log dir: %v", err)
	}
	if _, err := db.Exec("DELETE FROM log_watermarks"); err != nil {
		t.Fatalf("clear watermarks: %v", err)
	}

	db, sync := reopenStore(t, db, dir)
	if sync.State != StoreInSync {
		t.Fatalf("state = %q, want %q after a bootstrap", sync.State, StoreInSync)
	}
	evs, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(cache), rep))
	if err != nil {
		t.Fatalf("read bootstrapped log: %v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("the bootstrap wrote no events")
	}
	if mark, ok := watermarkOf(t, db, rep); !ok || mark != logSize(t, dir, rep) {
		t.Fatalf("watermark %d not set to the bootstrapped file's size", mark)
	}
	if after := applyDump(t, db); after != before {
		t.Fatalf("the bootstrap changed the cache")
	}
}

// A clone that carries only .jobs/log builds a working cache on first open.
func TestAFreshCloneBuildsACacheFromTheLogAlone(t *testing.T) {
	src, clone := t.TempDir(), t.TempDir()
	db := storeAt(t, src)
	created, err := RunAdd(db, "", "carried by git", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	db.Close()
	carryLog(t, src, clone)

	fresh, err := OpenDB(filepath.Join(clone, ".jobs.db"))
	if err != nil {
		t.Fatalf("open fresh clone: %v", err)
	}
	defer fresh.Close()
	var title string
	if err := fresh.QueryRow("SELECT title FROM tasks WHERE short_id = ?", created.ShortID).Scan(&title); err != nil {
		t.Fatalf("the clone did not build the task: %v", err)
	}
	if title != "carried by git" {
		t.Fatalf("title = %q", title)
	}
	if s := StoreSyncOf(fresh); s == nil || s.State != StoreRebuilt {
		t.Fatalf("state = %v, want %q", s, StoreRebuilt)
	}
}

func TestStoreLineIsPartOfStatus(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "one", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	s, err := RunStatus(db, "ben")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if s.Store == nil {
		t.Fatalf("status carries no store line")
	}
	if s.Store.Rep != repOf(t, dir) {
		t.Fatalf("store rep = %q", s.Store.Rep)
	}
	if s.Store.Files != 1 || s.Store.Events == 0 {
		t.Fatalf("store files/events = %d/%d", s.Store.Files, s.Store.Events)
	}
	var buf bytes.Buffer
	RenderStatus(&buf, s)
	if !strings.Contains(buf.String(), "Store: ") {
		t.Fatalf("no store line rendered:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), string(StoreInSync)) {
		t.Fatalf("store line does not name the cache state:\n%s", buf.String())
	}
}

// nowMillisForTest is the clock the store's own tests stamp hand-made
// envelopes with.
func nowMillisForTest() int64 { return CurrentNowFunc().UnixMilli() }
