package job

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Two replicas, end to end.
//
// Everything else in this package tests one seam. These tests stand two whole
// stores up in two directories, write on both, carry the log files across as a
// git pull would, and ask what the design promises: that both machines end up
// holding the same database, and that every row of the merge-rule table in
// project/2026-09-01-git-native-event-log.md resolves the way it says on BOTH
// sides.
//
// The harness is here; the merge-rule table is in replica_rules_e2e_test.go.

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// replica is one checkout on one machine: a directory holding .jobs/log and a
// cache built from it.
type replica struct {
	t    *testing.T
	name string
	dir  string
}

func (r *replica) cache() string { return filepath.Join(r.dir, ".jobs.db") }

// do is one `job` invocation: open the cache — which is what syncs it with the
// log — run the body, close. Every write in these tests goes through a handler,
// so nothing here depends on a shape only the tests know.
func (r *replica) do(fn func(db *sql.DB)) *StoreSync {
	r.t.Helper()
	db, err := OpenDB(r.cache())
	if err != nil {
		r.t.Fatalf("open %s: %v", r.name, err)
	}
	defer db.Close()
	sync := StoreSyncOf(db)
	if sync == nil {
		r.t.Fatalf("open %s recorded no store sync", r.name)
	}
	if fn != nil {
		fn(db)
	}
	return sync
}

// tryOpen opens the cache and hands back the error instead of failing, for the
// cases where refusing to build is the correct answer.
func (r *replica) tryOpen() error {
	r.t.Helper()
	db, err := OpenDB(r.cache())
	if db != nil {
		db.Close()
	}
	return err
}

// dump renders this replica's whole cache in a row-id-free form.
func (r *replica) dump() string {
	r.t.Helper()
	db, err := OpenDB(r.cache())
	if err != nil {
		r.t.Fatalf("open %s: %v", r.name, err)
	}
	defer db.Close()
	return applyDump(r.t, db)
}

// logEvents reads this replica's raw log files — what another machine would
// pull. A repair that is only in the cache is not a repair the log carries.
func (r *replica) logEvents() []eventlog.Envelope {
	r.t.Helper()
	events, err := eventlog.ReadAll(eventlog.StoreDir(r.cache()))
	if err != nil {
		r.t.Fatalf("read %s's log: %v", r.name, err)
	}
	eventlog.Sort(events)
	return events
}

// forgeCreated appends a `created` event for a chosen short id to a brand new
// replica file in this replica's store, and returns that replica's id.
//
// This is the one thing driving the library twice cannot produce: two machines
// minting the same short id while apart. generateShortID checks the local
// table, so a second store never repeats the first store's id by accident.
func (r *replica) forgeCreated(shortID, title string) string {
	r.t.Helper()
	rep, err := eventlog.NewReplicaID()
	if err != nil {
		r.t.Fatalf("mint replica id: %v", err)
	}
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(r.cache()), r.cache(), rep)
	if err != nil {
		r.t.Fatalf("open appender for %s: %v", rep, err)
	}
	defer ap.Close()
	payload, err := json.Marshal(CreatedPayload{ShortID: shortID, Title: title, SortKey: "zzzzzz"})
	if err != nil {
		r.t.Fatalf("marshal: %v", err)
	}
	e := eventlog.Envelope{
		TS:    CurrentNowFunc().UnixMilli(),
		Actor: "sam",
		Type:  eventlog.Type(EventCreated),
		Task:  shortID,
		Data:  payload,
	}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		r.t.Fatalf("append: %v", err)
	}
	return rep
}

// pair is two replicas of one repo, plus the clock that decides which of two
// concurrent events is "later". The real clock's one-second resolution would
// tie almost every pair of writes a test makes.
type pair struct {
	t     *testing.T
	clock *mergeClock
	A, B  *replica
}

func newPair(t *testing.T) *pair {
	t.Helper()
	quietNotices(t)
	p := &pair{t: t, clock: newMergeClock(t)}
	p.A = &replica{t: t, name: "A", dir: t.TempDir()}
	p.B = &replica{t: t, name: "B", dir: t.TempDir()}
	return p
}

// tick moves the shared wall clock forward, so the next write is
// unambiguously later than the last one.
func (p *pair) tick() { p.clock.advance(time.Minute) }

// seed writes the history both replicas share before they diverge — the state
// of the repo at the commit they both cloned.
func (p *pair) seed(fn func(db *sql.DB)) {
	p.t.Helper()
	p.A.do(fn)
	carryLog(p.t, p.A.dir, p.B.dir)
	p.B.do(nil)
	p.tick()
}

// exchange is `git pull` on both machines followed by one command on each: the
// log files are copied whole in both directions, then each cache is opened,
// which replays the union and reconciles.
func (p *pair) exchange() (syncA, syncB *StoreSync) {
	p.t.Helper()
	carryLog(p.t, p.A.dir, p.B.dir)
	carryLog(p.t, p.B.dir, p.A.dir)
	return p.A.do(nil), p.B.do(nil)
}

// ---------------------------------------------------------------------------
// reading state back
// ---------------------------------------------------------------------------

// bothSides runs one assertion against each replica, naming which side failed.
func (p *pair) bothSides(check func(t *testing.T, db *sql.DB)) {
	p.t.Helper()
	for _, r := range []*replica{p.A, p.B} {
		db, err := OpenDB(r.cache())
		if err != nil {
			p.t.Fatalf("open %s: %v", r.name, err)
		}
		p.t.Run("on "+r.name, func(t *testing.T) { check(t, db) })
		db.Close()
	}
}

func taskField(t *testing.T, db *sql.DB, shortID, column string) string {
	t.Helper()
	var v sql.NullString
	err := db.QueryRow("SELECT "+column+" FROM tasks WHERE short_id = ?", shortID).Scan(&v)
	if err == sql.ErrNoRows {
		t.Fatalf("task %s is not in the cache", shortID)
	}
	if err != nil {
		t.Fatalf("read %s.%s: %v", shortID, column, err)
	}
	return v.String
}

func wantField(t *testing.T, db *sql.DB, shortID, column, want string) {
	t.Helper()
	if got := taskField(t, db, shortID, column); got != want {
		t.Fatalf("%s.%s = %q, want %q", shortID, column, got, want)
	}
}

func taskExists(t *testing.T, db *sql.DB, shortID string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE short_id = ?", shortID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", shortID, err)
	}
	return n > 0
}

func blockerCount(t *testing.T, db *sql.DB, blockedShortID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM blocks b JOIN tasks bd ON bd.id = b.blocked_id
		WHERE bd.short_id = ?`, blockedShortID).Scan(&n); err != nil {
		t.Fatalf("blockers of %s: %v", blockedShortID, err)
	}
	return n
}

// notesOn returns the note text on a task in the order `show` renders them.
func notesOn(t *testing.T, db *sql.DB, shortID string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT COALESCE(e.detail,'') FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'noted'
		ORDER BY e.ts, e.rep, CASE WHEN e.rep = '' THEN e.id ELSE e.seq END`, shortID)
	if err != nil {
		t.Fatalf("notes on %s: %v", shortID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var detail string
		if err := rows.Scan(&detail); err != nil {
			t.Fatal(err)
		}
		var p NotedPayload
		if err := json.Unmarshal([]byte(detail), &p); err != nil {
			t.Fatalf("decode note %q: %v", detail, err)
		}
		out = append(out, p.Text)
	}
	return out
}

// countEvents counts the cached events matching a task, type and optional
// detail substring. "" for shortID matches an event with no task.
func countEvents(t *testing.T, db *sql.DB, shortID string, typ EventType, contains string) int {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		WHERE COALESCE(t.short_id,'') = ? AND e.event_type = ? AND COALESCE(e.detail,'') LIKE ?`,
		shortID, string(typ), "%"+contains+"%").Scan(&n)
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

// countLogEvents is countEvents against the raw log files rather than the
// cache — the record, not the projection of it.
func (r *replica) countLogEvents(typ EventType, contains string) int {
	r.t.Helper()
	n := 0
	for _, e := range r.logEvents() {
		if EventType(e.Type) != typ {
			continue
		}
		if contains != "" && !strings.Contains(string(e.Data), contains) {
			continue
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// 1. exchange, then identical dumps
// ---------------------------------------------------------------------------

// The whole promise in one test: two machines write for a while, exchange log
// files, and hold the same database — with no conflict to resolve, because
// most concurrent work simply composes.
func TestTwoReplicas_ExchangeThenIdenticalDumps(t *testing.T) {
	p := newPair(t)

	var shared, sharedChild string
	p.seed(func(db *sql.DB) {
		shared = MustAdd(t, db, "", "Shared plan")
		sharedChild = MustAdd(t, db, shared, "Shared child")
	})

	// A works: a root of its own, a child under the shared root, a note, two
	// labels, criteria, a claim, and one close.
	var aRoot, aOne, aTwo string
	p.A.do(func(db *sql.DB) {
		aRoot = MustAdd(t, db, "", "Alpha root")
		aOne = MustAdd(t, db, aRoot, "Alpha one")
		aTwo = MustAdd(t, db, aRoot, "Alpha two")
		MustAdd(t, db, shared, "Alpha under the shared root")
		if err := RunNote(db, sharedChild, "a note from A", nil, "ben"); err != nil {
			t.Fatalf("note on A: %v", err)
		}
		if _, err := RunLabelAdd(db, shared, []string{"store", "sync"}, "ben"); err != nil {
			t.Fatalf("label on A: %v", err)
		}
		if _, err := RunAddCriteria(db, aOne, []Criterion{{Label: "The dumps match"}}, "ben"); err != nil {
			t.Fatalf("criteria on A: %v", err)
		}
		if err := RunClaim(db, aOne, "4h", "", "ben", false); err != nil {
			t.Fatalf("claim on A: %v", err)
		}
		if _, _, err := RunDone(db, []string{aTwo}, false, "done over here", nil, "ben", false, ""); err != nil {
			t.Fatalf("done on A: %v", err)
		}
	})
	p.tick()

	// B works, entirely independently.
	var bRoot, bOne, bTwo string
	p.B.do(func(db *sql.DB) {
		bRoot = MustAdd(t, db, "", "Beta root")
		bOne = MustAdd(t, db, bRoot, "Beta one")
		bTwo = MustAdd(t, db, bRoot, "Beta two")
		MustAdd(t, db, shared, "Beta under the shared root")
		if err := RunNote(db, sharedChild, "a note from B", nil, "sam"); err != nil {
			t.Fatalf("note on B: %v", err)
		}
		if _, err := RunLabelAdd(db, sharedChild, []string{"web"}, "sam"); err != nil {
			t.Fatalf("label on B: %v", err)
		}
		if _, err := RunAddCriteria(db, bOne, []Criterion{{Label: "B agrees"}}, "sam"); err != nil {
			t.Fatalf("criteria on B: %v", err)
		}
		if err := RunClaim(db, bOne, "4h", "", "sam", false); err != nil {
			t.Fatalf("claim on B: %v", err)
		}
		if _, _, err := RunDone(db, []string{bTwo}, false, "done over there", nil, "sam", false, ""); err != nil {
			t.Fatalf("done on B: %v", err)
		}
	})
	p.tick()

	syncA, syncB := p.exchange()
	if syncA.State != StoreRebuilt || syncB.State != StoreRebuilt {
		t.Fatalf("states after the exchange: A %q, B %q — both should have rebuilt", syncA.State, syncB.State)
	}
	// Nothing here is a conflict, so nothing needs repairing. If reconcile did
	// append, each side appended its own copy and the dumps below would differ
	// for a reason that has nothing to do with the merge.
	if len(syncA.Repairs) != 0 || len(syncB.Repairs) != 0 {
		t.Fatalf("reconcile repaired a happy path: A %v, B %v", syncA.Repairs, syncB.Repairs)
	}

	if a, b := p.A.dump(), p.B.dump(); a != b {
		t.Fatalf("the two caches differ after the exchange:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}

	// Every side of both machines' work is present on both.
	p.bothSides(func(t *testing.T, db *sql.DB) {
		for _, id := range []string{shared, sharedChild, aRoot, aOne, aTwo, bRoot, bOne, bTwo} {
			if !taskExists(t, db, id) {
				t.Fatalf("task %s did not survive the exchange", id)
			}
		}
		wantField(t, db, aTwo, "status", "done")
		wantField(t, db, bTwo, "status", "done")
		wantField(t, db, aOne, "claimed_by", "ben")
		wantField(t, db, bOne, "claimed_by", "sam")
		if got := labelsOf(t, db, shared); len(got) != 2 {
			t.Fatalf("labels on the shared root = %v", got)
		}
		if got := notesOn(t, db, sharedChild); len(got) != 2 {
			t.Fatalf("notes on the shared child = %v, want both machines'", got)
		}
	})

	// And a second open is the hot path on both: a stat per file, no rebuild.
	for _, r := range []*replica{p.A, p.B} {
		if s := r.do(nil); s.State != StoreInSync {
			t.Fatalf("second open of %s: %q, want %q", r.name, s.State, StoreInSync)
		}
	}
}

// ---------------------------------------------------------------------------
// snapshot: a legacy database adopted on one machine travels through git
// ---------------------------------------------------------------------------

// The `snapshot` row of the merge-rule table. A machine adopts its legacy
// .jobs.db, which writes one snapshot event carrying the whole state; a second
// machine clones the log alone and must build the same database from it.
func TestTwoReplicas_AdoptedSnapshotClonesIdentically(t *testing.T) {
	clock := newMergeClock(t)
	quietNotices(t)

	dirA, dirB := t.TempDir(), t.TempDir()
	legacy := legacyCache(t, dirA, func(db *sql.DB) {
		root := MustAdd(t, db, "", "Adopted root")
		child := MustAdd(t, db, root, "Adopted child")
		if _, err := RunLabelAdd(db, root, []string{"store"}, TestActor); err != nil {
			t.Fatal(err)
		}
		if _, err := RunAddCriteria(db, child, []Criterion{{Label: "It clones"}}, TestActor); err != nil {
			t.Fatal(err)
		}
		if err := RunBlock(db, root, child, TestActor); err != nil {
			t.Fatal(err)
		}
		clock.advance(time.Minute)
		MustClaim(t, db, child, "2h")
	})

	clock.advance(time.Hour)
	adopted, err := OpenDB(legacy)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	want := applyDump(t, adopted)
	adopted.Close()

	// The clone carries .jobs/log and nothing else.
	carryLog(t, dirA, dirB)
	cloned, err := OpenDB(filepath.Join(dirB, ".jobs.db"))
	if err != nil {
		t.Fatalf("open the clone: %v", err)
	}
	defer cloned.Close()

	if got := applyDump(t, cloned); got != want {
		t.Fatalf("the clone differs from the adopted machine:\n--- adopted ---\n%s\n--- clone ---\n%s", want, got)
	}
}

// ---------------------------------------------------------------------------
// created: a collision, and the way out
// ---------------------------------------------------------------------------

// `created` is idempotent by short id, so the same task arriving from two files
// makes one row — but the SAME id for two DIFFERENT tasks cannot be merged, and
// is not. The rebuild fails naming both replicas and both titles, and `job
// rekey` is what converges the two machines afterwards.
func TestTwoReplicas_CreatedCollisionFailsThenRekeyConverges(t *testing.T) {
	p := newPair(t)

	var mine string
	p.A.do(func(db *sql.DB) { mine = MustAdd(t, db, "", "the original") })
	repA := repOf(t, p.A.dir)
	p.tick()

	// B mints the same id while apart.
	repB := p.B.forgeCreated(mine, "the impostor")

	carryLog(t, p.A.dir, p.B.dir)
	carryLog(t, p.B.dir, p.A.dir)

	for _, r := range []*replica{p.A, p.B} {
		err := r.tryOpen()
		if err == nil {
			t.Fatalf("%s built a cache from two created events for %s", r.name, mine)
		}
		msg := err.Error()
		for _, want := range []string{repA, repB, "the original", "the impostor", mine, "job rekey"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("%s's collision error does not name %q:\n%s", r.name, want, msg)
			}
		}
	}

	// A human rules on A: the later replica's task gets a fresh id.
	recovery, err := OpenDBForRecovery(p.A.cache())
	if err != nil {
		t.Fatalf("open for recovery: %v", err)
	}
	res, err := RunRekey(recovery, repB+":"+mine, "ben")
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	recovery.Close()

	// The ruling rides the log to the other machine, which converges without a
	// second decision.
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, mine, "title", "the original")
		wantField(t, db, res.NewID, "title", "the impostor")
	})
	if a, b := p.A.dump(), p.B.dump(); a != b {
		t.Fatalf("the two caches differ after the rekey:\n--- A ---\n%s\n--- B ---\n%s", a, b)
	}
}

// ---------------------------------------------------------------------------
// the clock the whole merge order rests on
// ---------------------------------------------------------------------------

// newestTS is the timestamp of the last event one replica wrote.
func (r *replica) newestTS(rep string) int64 {
	r.t.Helper()
	var newest int64
	for _, e := range r.logEvents() {
		if rep != "" && e.Rep != rep {
			continue
		}
		if e.TS > newest {
			newest = e.TS
		}
	}
	return newest
}

// "`ts` is a hybrid logical clock … where `last_seen` is the largest ts this
// replica has READ or written. It makes cause sort before effect even when two
// machines' clocks disagree."
//
// So an event a replica mints after pulling somebody else's log must sort after
// everything in that log, however far behind this machine's wall clock is. A
// replica whose clock only counts its own writes mints events that sort before
// the events that caused them, and the merge order stops meaning anything —
// reconcile in particular emits a `released` for a claim it can then sort
// before.
func TestTwoReplicas_TheClockAdvancesPastEventsItReads(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "written on two clocks") })

	// A's machine is an hour ahead of B's.
	p.clock.advance(time.Hour)
	p.A.do(func(db *sql.DB) {
		if err := RunNote(db, task, "from the machine that is ahead", nil, "ben"); err != nil {
			t.Fatal(err)
		}
	})
	repA := repOf(t, p.A.dir)
	newestFromA := p.A.newestTS(repA)

	// B pulls, then writes with its own, slower clock.
	p.clock.advance(-time.Hour)
	carryLog(t, p.A.dir, p.B.dir)
	p.B.do(func(db *sql.DB) {
		if err := RunNote(db, task, "answering, on the slow machine", nil, "sam"); err != nil {
			t.Fatal(err)
		}
	})
	repB := repOf(t, p.B.dir)

	if got := p.B.newestTS(repB); got <= newestFromA {
		t.Fatalf("B's reply is stamped %d, at or before the %d it was replying to: "+
			"the clock did not observe the events it read", got, newestFromA)
	}
}

// mustEdit is RunEdit with the pointer dance the handler wants.
func mustEdit(t *testing.T, db *sql.DB, shortID, title, desc, actor string) {
	t.Helper()
	var tp, dp *string
	if title != "" {
		tp = &title
	}
	if desc != "" {
		dp = &desc
	}
	if err := RunEdit(db, shortID, tp, dp, actor); err != nil {
		t.Fatalf("edit %s: %v", shortID, err)
	}
}
