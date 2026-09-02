package job

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Adoption turns a cache that predates the store into one the log reproduces:
// every legacy row becomes a history-only line, one snapshot carries the state
// those rows used to imply, and the cache is rebuilt from the result. The tests
// here all ask the same question in different ways — is the rebuilt cache the
// same database?

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// legacyCache builds a database through the handlers and then demotes it to a
// pre-store one: every event row loses its position and the .jobs/ store beside
// it is removed, which is exactly the shape of a cache written before the log
// existed.
func legacyCache(t *testing.T, dir string, drive func(db *sql.DB)) string {
	t.Helper()
	path := filepath.Join(dir, "legacy.db")
	db := mustOpenAt(t, path)
	drive(db)
	if _, err := db.Exec("UPDATE events SET rep = '', seq = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM log_watermarks"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(eventlog.StoreDir(path)); err != nil {
		t.Fatal(err)
	}
	return path
}

// copyTree duplicates a directory of ordinary files, so a test can dump one
// copy of a legacy database and adopt the other.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyTree(t, s, d)
			continue
		}
		b, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	s, err := dumpHistory(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// dumpPristine reads a legacy cache without adopting it. OpenDBForRecovery is
// the one open that does not reconcile with the store.
func dumpPristine(t *testing.T, path string) string {
	t.Helper()
	db, err := OpenDBForRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return mustDump(t, db)
}

func quietNotices(t *testing.T) {
	t.Helper()
	orig := StoreNotices
	StoreNotices = io.Discard
	t.Cleanup(func() { StoreNotices = orig })
}

func countLegacyRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE rep = ''").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// The whole point: a legacy cache and the cache rebuilt from its adoption hold
// the same content. The synthetic database carries every table adoption has to
// carry — a purged task (whose tombstone is an orphan event with no task), a
// soft-deleted task, a live claim, criteria, labels, blocks and provenance.
func TestAdopt_SyntheticDatabaseRebuildsIdentically(t *testing.T) {
	clock := newMergeClock(t)
	quietNotices(t)
	root := t.TempDir()

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	path := legacyCache(t, src, func(db *sql.DB) {
		a := MustAdd(t, db, "", "Alpha root")
		b := MustAdd(t, db, a, "Beta child")
		c := MustAdd(t, db, a, "Gamma child")
		if _, err := RunLabelAdd(db, a, []string{"store", "migration"}, TestActor); err != nil {
			t.Fatal(err)
		}
		crit := []Criterion{{Label: "The dump matches"}, {Label: "The log is untouched"}}
		if _, err := RunAddCriteria(db, b, crit, TestActor); err != nil {
			t.Fatal(err)
		}
		if err := RunBlock(db, c, b, TestActor); err != nil {
			t.Fatal(err)
		}
		if err := RunSetFoundIn(db, c, a, TestActor); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Minute)
		doomed := MustAdd(t, db, a, "Purged entirely")
		if _, _, _, err := RunCancel(db, []string{doomed}, "no longer wanted", false, true, true, TestActor); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Minute)
		gone := MustAdd(t, db, a, "Soft deleted")
		if _, _, _, err := RunCancel(db, []string{gone}, "obsolete", false, false, false, TestActor); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Minute)
		MustClaim(t, db, c, "2h")
	})

	other := filepath.Join(root, "other")
	copyTree(t, src, other)
	before := dumpPristine(t, filepath.Join(other, "legacy.db"))

	clock.advance(time.Hour)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("open (adopting): %v", err)
	}
	defer db.Close()

	if got := mustDump(t, db); got != before {
		t.Errorf("adoption changed the database.\n--- before ---\n%s\n--- after ---\n%s", before, got)
	}
	if n := countLegacyRows(t, db); n != 0 {
		t.Errorf("expected no unpositioned rows after adoption, got %d", n)
	}
	if _, err := os.Stat(path + adoptBackupSuffix); err != nil {
		t.Errorf("expected a backup at %s: %v", path+adoptBackupSuffix, err)
	}
	if sync := StoreSyncOf(db); sync == nil || sync.State != StoreInSync {
		t.Errorf("expected the adopted cache to be in sync, got %+v", sync)
	}
}

// The real thing: this repo's own database, which is where the legacy rows the
// design was written for actually live.
func TestAdopt_ThisRepositorysDatabase(t *testing.T) {
	quietNotices(t)
	live := repoDatabasePath()
	if live == "" {
		t.Skip("this repository's .jobs.db is not present")
	}
	root := t.TempDir()

	src := filepath.Join(root, "src")
	copyRepoDatabase(t, live, src)
	path := filepath.Join(src, ".jobs.db")
	skipUnlessLegacy(t, path)
	other := filepath.Join(root, "other")
	copyTree(t, src, other)

	before := dumpPristine(t, filepath.Join(other, ".jobs.db"))

	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("open (adopting): %v", err)
	}
	defer db.Close()

	if got := mustDump(t, db); got != before {
		t.Errorf("adoption changed this repository's database (dumps differ; first difference at %s)", firstDiff(before, got))
	}
	if n := countLegacyRows(t, db); n != 0 {
		t.Errorf("expected no unpositioned rows after adoption, got %d", n)
	}
	if _, err := os.Stat(path + adoptBackupSuffix); err != nil {
		t.Errorf("expected a backup at %s: %v", path+adoptBackupSuffix, err)
	}
}

// `job log` is the surface a legacy row is read through. Adoption must not
// move a single byte of it, so this drives the built binary rather than the
// library.
func TestAdopt_JobLogIsUnchanged(t *testing.T) {
	quietNotices(t)
	live := repoDatabasePath()
	if live == "" {
		t.Skip("this repository's .jobs.db is not present")
	}
	bin := buildJobBinary(t)
	root := t.TempDir()

	src := filepath.Join(root, "src")
	copyRepoDatabase(t, live, src)
	path := filepath.Join(src, ".jobs.db")
	skipUnlessLegacy(t, path)

	before := runJob(t, bin, []string{"JOBS_NO_ADOPT=1"}, "log", "--db", path)
	after := runJob(t, bin, nil, "log", "--db", path)
	if before == after {
		return
	}
	// One reordering is allowed, and only one: `job log` orders by created_at
	// and then by row id, while a rebuild orders by (ts, rep, seq). A legacy
	// row's ts is its created_at in whole seconds, so where a legacy row and a
	// positioned one were written in the SAME second, the legacy row now sorts
	// first whatever order their row ids were in. That can only happen while
	// the codebase still writes unpositioned rows — every legacy row in a
	// finished database predates the store entirely — and it never moves an
	// event out of the second it was rendered in.
	if normalizeLogOrder(before) != normalizeLogOrder(after) {
		t.Errorf("job log changed across adoption (first difference at %s)", firstDiff(before, after))
	}
}

// normalizeLogOrder sorts the lines within each rendered timestamp, so a
// comparison sees what `job log` says and when, but not the order of two events
// that share a second.
func normalizeLogOrder(out string) string {
	lines := strings.Split(out, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		stamp := logStamp(lines[i])
		j := i
		for j < len(lines) && logStamp(lines[j]) == stamp {
			j++
		}
		group := append([]string(nil), lines[i:j]...)
		if stamp != "" {
			sort.Strings(group)
		}
		result = append(result, group...)
		i = j
	}
	return strings.Join(result, "\n")
}

func logStamp(line string) string {
	if !strings.HasPrefix(line, "[") {
		return ""
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return ""
	}
	return line[:end+1]
}

// A translation that would change state is a bug in adoption, and the answer
// to a bug in adoption is to do nothing at all: no log line, no backup, no new
// cache.
func TestAdopt_ADifferenceAbortsAndTouchesNothing(t *testing.T) {
	newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	path := legacyCache(t, dir, func(db *sql.DB) {
		a := MustAdd(t, db, "", "Alpha root")
		MustAdd(t, db, a, "Beta child")
	})

	// The snapshot is what carries the state forward; drop a task from it and
	// the rebuilt cache cannot match.
	orig := adoptMutateSnapshot
	adoptMutateSnapshot = func(p *SnapshotPayload) {
		if len(p.Tasks) > 0 {
			p.Tasks = p.Tasks[:len(p.Tasks)-1]
		}
	}
	t.Cleanup(func() { adoptMutateSnapshot = orig })

	before := dumpPristine(t, path)
	if _, err := adopt(path); err == nil {
		t.Fatal("expected adoption to abort on a difference")
	}

	if _, err := os.Stat(path + adoptBackupSuffix); !os.IsNotExist(err) {
		t.Errorf("aborted adoption should leave no backup")
	}
	if _, err := os.Stat(path + adoptCandidateSuffix); !os.IsNotExist(err) {
		t.Errorf("aborted adoption should leave no candidate cache")
	}
	files, err := eventlog.Files(eventlog.StoreDir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("aborted adoption wrote log files: %v", files)
	}
	if got := dumpPristine(t, path); got != before {
		t.Errorf("aborted adoption changed the cache")
	}
}

// Once adopted, a database is an ordinary one: the next open is the hot path.
func TestAdopt_SecondOpenIsAPlainOpen(t *testing.T) {
	newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	path := legacyCache(t, dir, func(db *sql.DB) {
		MustAdd(t, db, "", "Alpha root")
	})

	first, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	sync := StoreSyncOf(second)
	if sync == nil || sync.State != StoreInSync {
		t.Fatalf("expected the second open to be in sync, got %+v", sync)
	}
	status, err := RunStatus(second, TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if status.Store == nil || status.Store.State != StoreInSync {
		t.Errorf("job status should report a cache in sync, got %+v", status.Store)
	}
}

// The log carries the legacy rows as history and the snapshot as state, and
// applying a legacy line writes no state at all.
func TestAdopt_LegacyLinesAreHistoryOnly(t *testing.T) {
	newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	path := legacyCache(t, dir, func(db *sql.DB) {
		MustAdd(t, db, "", "Alpha root")
	})
	if _, err := adopt(path); err != nil {
		t.Fatal(err)
	}

	events, err := eventlog.ReadAll(eventlog.StoreDir(path))
	if err != nil {
		t.Fatal(err)
	}
	var legacy, snapshots int
	for _, e := range events {
		if e.Legacy {
			legacy++
		}
		if EventType(e.Type) == EventSnapshot {
			snapshots++
		}
	}
	if legacy == 0 {
		t.Error("expected the log to carry legacy lines")
	}
	if snapshots != 1 {
		t.Errorf("expected exactly one snapshot line, got %d", snapshots)
	}
	// This log held nothing before adoption, so there is nothing for the
	// snapshot to sit in front of and it lands last.
	if last := events[len(events)-1]; EventType(last.Type) != EventSnapshot {
		t.Errorf("the snapshot should sort last, got %s", last.Type)
	}

}

// apply's own rule, exercised directly: a legacy envelope is recorded and
// nothing else, whatever its type would otherwise do. The rebuild has its own
// path for these, so without this test the rule in apply is never run.
func TestApply_LegacyEnvelopeWritesNoState(t *testing.T) {
	newMergeClock(t)
	db := mustOpenAt(t, filepath.Join(t.TempDir(), "probe.db"))

	created, err := json.Marshal(CreatedPayload{ShortID: "AbCdEf", Title: "Would be a task"})
	if err != nil {
		t.Fatal(err)
	}
	e := eventlog.Envelope{
		V: eventlog.Version, Rep: "aaaaaa", Seq: 1, TS: 1_700_000_000_000,
		Actor: TestActor, Type: eventlog.Type(EventCreated), Task: "AbCdEf",
		Data: created, Legacy: true,
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(tx, e); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var tasks, evts int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM events").Scan(&evts); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Errorf("a legacy created event made %d task rows, want none", tasks)
	}
	if evts != 1 {
		t.Errorf("a legacy event should still be recorded: %d event rows", evts)
	}
}

// ---------------------------------------------------------------------------
// helpers for the real-database tests
// ---------------------------------------------------------------------------

// repoDatabasePath finds this repository's own .jobs.db by walking up from the
// test's working directory. It is gitignored, so a worktree does not have one
// and the tests that want it skip.
// repoDatabasePath finds this repository's database. Once the live cache has
// been adopted it holds no legacy rows, so the backup adoption kept beside it
// (.jobs.db.pre-adopt) is preferred: that file is the legacy fixture the
// design was written for, and it stays one for as long as it exists.
func repoDatabasePath() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		for _, name := range []string{".jobs.db" + adoptBackupSuffix, ".jobs.db"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// skipUnlessLegacy skips the test when the copied cache holds no unpositioned
// rows: there is nothing for adoption to do, so the test would prove nothing.
func skipUnlessLegacy(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM events WHERE rep = '')").Scan(&n); err != nil || n == 0 {
		t.Skipf("%s holds no legacy rows; nothing to adopt", path)
	}
}

// copyRepoDatabase takes a consistent copy of a live cache and the store
// beside it.
//
// The cache is copied with VACUUM INTO rather than by reading its bytes: it is
// written to while the test runs, and a byte copy of a WAL database catches it
// mid-transaction. The store is copied second, because a write reaches the log
// file before the cache commits — so a log copied after the cache is always a
// superset of it, never a subset.
func copyRepoDatabase(t *testing.T, live, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dst, ".jobs.db")
	src, err := sql.Open("sqlite", live)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec("VACUUM INTO ?", target); err != nil {
		src.Close()
		t.Fatalf("copy %s: %v", live, err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	store := eventlog.StoreDir(live)
	if info, err := os.Stat(store); err == nil && info.IsDir() {
		copyTree(t, store, filepath.Join(dst, eventlog.StoreDirName))
	}
	// The store beside a pre-adopt backup already carries that backup's
	// adoption. Cutting every log file at its first legacy line restores the
	// store exactly as it was the moment before adoption ran, which is the
	// fixture this test is for.
	if filepath.Base(live) != ".jobs.db" {
		truncateLogsBeforeAdoption(t, filepath.Join(dst, eventlog.StoreDirName))
		os.Remove(filepath.Join(dst, eventlog.StoreDirName, "local.json"))
	}
}

// truncateLogsBeforeAdoption rewrites each replica file under storeDir to the
// lines that precede its first legacy envelope.
func truncateLogsBeforeAdoption(t *testing.T, storeDir string) {
	t.Helper()
	files, err := eventlog.Files(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		var kept []byte
		for line := range bytes.SplitSeq(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n")) {
			if bytes.Contains(line, []byte(`"legacy":true`)) {
				break
			}
			kept = append(kept, line...)
			kept = append(kept, '\n')
		}
		if len(kept) == 0 {
			if err := os.Remove(f.Path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(f.Path, kept, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func buildJobBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "job")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/job")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build job: %v\n%s", err, out)
	}
	return bin
}

func runJob(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("job %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String()
}

// firstDiff names where two renderings part company, which is far more useful
// in a failure than two thousand lines of context.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(al), len(bl)) {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "line " + itoa(i+1) + ":\n  before: " + x + "\n  after:  " + y
		}
	}
	return "nowhere"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// what the note tells the reader to do next
// ---------------------------------------------------------------------------

// adoptedIn drives one adoption through the open that performs it and returns
// everything it printed.
func adoptedIn(t *testing.T, dir string) string {
	t.Helper()
	path := legacyCache(t, dir, func(db *sql.DB) {
		MustAdd(t, db, "", "Alpha root")
	})
	notices := captureNotices(t)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	return notices.String()
}

// The house rule is that a write says what it made and names the next command.
// Adoption makes a store out of a cache, and the reader has to know the store
// is `.jobs/log`, that it belongs in git, that the cache and its sidecars do
// not, and that the backup is disposable — an agent that adopted a real
// database had to piece all four together from `git status`
// (project/2026-09-02-adoption-note-and-gitignore-dry-run.md).
func TestAdopt_NoticeNamesTheStoreTheCommitAndGitignore(t *testing.T) {
	newMergeClock(t)
	dir := t.TempDir()
	mustMarkGitRepo(t, dir)

	out := adoptedIn(t, dir)

	for _, want := range []string{
		".jobs/log",
		"commit",
		"job gitignore",
		adoptBackupSuffix,
		"delete",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("adoption note missing %q:\n%s", want, out)
		}
	}
}

// Advice that cannot be acted on is noise: once both patterns are ignored,
// the note still names the store and the commit but stops naming the verb.
func TestAdopt_NoticeDropsGitignoreOnceNothingIsMissing(t *testing.T) {
	newMergeClock(t)
	dir := t.TempDir()
	mustMarkGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(GitignoreHint()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := adoptedIn(t, dir)

	if strings.Contains(out, "job gitignore") {
		t.Errorf("nothing is unignored, so the note should not name the verb:\n%s", out)
	}
	if !strings.Contains(out, ".jobs/log") || !strings.Contains(out, "commit") {
		t.Errorf("the note should still name the store and the commit:\n%s", out)
	}
}

// Outside a repository there is nothing to commit and no .gitignore to fix,
// so the second note is not printed at all — `init`'s hint has the same rule.
func TestAdopt_NoticeOmitsGitAdviceOutsideARepository(t *testing.T) {
	newMergeClock(t)
	dir := t.TempDir()

	out := adoptedIn(t, dir)

	if !strings.Contains(out, "adopted this database into the store") {
		t.Fatalf("expected the adoption note:\n%s", out)
	}
	for _, unwanted := range []string{"commit", "job gitignore"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("outside a repository the note should not say %q:\n%s", unwanted, out)
		}
	}
}

// mustMarkGitRepo gives dir the .git a repository check looks for. A worktree
// carries it as a file, so a file is enough.
func mustMarkGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
