package job

import (
	"database/sql"
	"math/rand"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The determinism test.
//
// The log is the record and the cache is disposable, so the only thing that
// makes that true is this: applying the same events in the same global order
// must yield the same tables, whatever order they arrived in. This drives a
// realistic sequence through the real handlers, reads the events back as
// envelopes, shuffles them, rebuilds a fresh database from the shuffle, and
// compares full logical dumps.
//
// It is the test that catches an apply that reads the clock, mints an id, or
// derives a cascade — every one of those makes the rebuild differ.

// applyDump renders every state table in a stable, row-id-free form. Row ids
// are minted by the cache and differ per machine, so neither they nor
// anything derived from them appears. Borrowed from logicalDump in
// merge_test.go and extended with the columns this family writes.
func applyDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	var b strings.Builder
	queries := []struct{ name, query string }{
		{"tasks", `SELECT t.short_id, COALESCE(p.short_id,''), t.title, t.description, t.status,
			t.sort_key, COALESCE(t.claimed_by,''), COALESCE(t.claim_expires_at,0),
			COALESCE(t.completion_note,'<null>'), t.created_at, t.updated_at,
			COALESCE(t.deleted_at,0), t.kind
			FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id ORDER BY t.short_id`},
		{"labels", `SELECT t.short_id, l.name FROM task_labels l JOIN tasks t ON t.id = l.task_id
			ORDER BY t.short_id, l.name`},
		{"blocks", `SELECT br.short_id, bd.short_id FROM blocks b
			JOIN tasks br ON br.id = b.blocker_id JOIN tasks bd ON bd.id = b.blocked_id
			ORDER BY br.short_id, bd.short_id`},
		{"criteria", `SELECT t.short_id, COALESCE(c.short_id,''), c.label, c.state, c.sort_key,
			c.created_at, c.updated_at FROM task_criteria c JOIN tasks t ON t.id = c.task_id
			ORDER BY t.short_id, c.short_id, c.label`},
		{"found_in", `SELECT t.short_id, s.short_id FROM found_in f
			JOIN tasks t ON t.id = f.task_id JOIN tasks s ON s.id = f.source_id ORDER BY t.short_id`},
		{"users", `SELECT name FROM users ORDER BY name`},
		{"events", `SELECT e.rep, e.seq, e.ts, e.created_at, e.event_type, e.actor,
			COALESCE(t.short_id,''), COALESCE(e.detail,'')
			FROM events e LEFT JOIN tasks t ON t.id = e.task_id
			ORDER BY e.ts, e.rep, e.seq`},
	}
	for _, q := range queries {
		b.WriteString("== " + q.name + "\n")
		rows, err := db.Query(q.query)
		if err != nil {
			t.Fatalf("%s: %v", q.name, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			cells := make([]any, len(cols))
			vals := make([]sql.NullString, len(cols))
			for i := range cells {
				cells[i] = &vals[i]
			}
			if err := rows.Scan(cells...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			parts := make([]string, len(vals))
			for i, v := range vals {
				parts[i] = v.String
			}
			b.WriteString(strings.Join(parts, "|") + "\n")
		}
		rows.Close()
	}
	return b.String()
}

// driveTaskFamily runs one realistic sequence through the handlers: roots and
// children, --before placement, edit, note, move, reparent, done with a
// cascade that auto-closes an ancestor, reopen, cancel, and purge.
func driveTaskFamily(t *testing.T, db *sql.DB) {
	t.Helper()
	const actor = "agent-determinism"

	alpha := MustAdd(t, db, "", "Alpha root")
	beta := MustAdd(t, db, "", "Beta root")
	a1 := MustAddDesc(t, db, alpha, "Alpha one", "first child")
	a2 := MustAdd(t, db, alpha, "Alpha two")

	// --before places a new sibling ahead of an existing one.
	res, err := RunAdd(db, alpha, "Alpha zero", "", a1, nil, actor)
	if err != nil {
		t.Fatal(err)
	}
	a0 := res.ShortID

	b1 := MustAdd(t, db, beta, "Beta one")
	b1a := MustAdd(t, db, b1, "Beta one child")

	newTitle := "Alpha one, renamed"
	newDesc := "a described child"
	if err := RunEdit(db, a1, &newTitle, &newDesc, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunNote(db, a1, "a note on the work", nil, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunMove(db, a2, "before", a0, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunReparent(db, a2, beta, "after", b1, actor); err != nil {
		t.Fatal(err)
	}

	// Close Beta's subtree: b1a closes, then b1 auto-closes as its last open
	// child went, and a1/a0 close explicitly.
	if _, _, err := RunDone(db, []string{b1a}, false, "child done", nil, actor, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunDone(db, []string{a1}, false, "with a completion note", nil, actor, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RunReopen(db, a1, false, actor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunDone(db, []string{alpha}, true, "cascade close", nil, actor, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RunReopen(db, alpha, true, actor); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := RunCancel(db, []string{a0}, "not needed", false, false, false, actor); err != nil {
		t.Fatal(err)
	}

	// A leaf purge (event recorded on the parent) and a root purge (an orphan
	// event) exercise both shapes.
	scratch := MustAdd(t, db, alpha, "Scratch leaf")
	if _, _, _, err := RunCancel(db, []string{scratch}, "typo", false, true, false, actor); err != nil {
		t.Fatal(err)
	}
	orphanRoot := MustAdd(t, db, "", "Root to purge")
	if _, _, _, err := RunCancel(db, []string{orphanRoot}, "wrong tree", false, true, false, actor); err != nil {
		t.Fatal(err)
	}

	// A tail of writes nothing later overwrites. Every apply stamps
	// updated_at, so a write whose task is touched again afterwards leaves no
	// trace of its own timestamp in the dump — these three do, which is what
	// makes the comparison able to see a move, a note or an edit that read
	// the clock instead of the event.
	moved := MustAdd(t, db, "", "Moved and left alone")
	anchor := MustAdd(t, db, "", "Anchor")
	if err := RunMove(db, moved, "after", anchor, actor); err != nil {
		t.Fatal(err)
	}
	noted := MustAdd(t, db, "", "Noted and left alone")
	if err := RunNote(db, noted, "the last word", nil, actor); err != nil {
		t.Fatal(err)
	}
	edited := MustAdd(t, db, "", "Edited and left alone")
	tailTitle := "Edited, and left alone"
	if err := RunEdit(db, edited, &tailTitle, nil, actor); err != nil {
		t.Fatal(err)
	}
}

// TestApplyDeterminism_ShuffledLogRebuildsIdentically is the criterion:
// rebuilding a shuffled log yields a dump identical to the original.
func TestApplyDeterminism_ShuffledLogRebuildsIdentically(t *testing.T) {
	source := SetupTestDB(t)
	driveTaskFamily(t, source)

	events, err := cachedEnvelopes(source)
	if err != nil {
		t.Fatalf("read envelopes: %v", err)
	}
	if len(events) < 20 {
		t.Fatalf("expected a substantial log, got %d envelopes", len(events))
	}
	for _, e := range events {
		if e.Rep == "" || e.Seq == 0 || e.TS == 0 {
			t.Fatalf("envelope %+v is missing its position", e)
		}
	}

	rng := rand.New(rand.NewSource(20260901))
	shuffled := append([]eventlog.Envelope(nil), events...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	// Move the wall clock a year forward before rebuilding. The dumps carry
	// every timestamp, so an apply that read the clock instead of the event's
	// ts now lands a year off and the comparison fails — without this the
	// rebuild runs in the same second as the original and a clock-reading
	// apply would pass by luck.
	restore := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = restore })
	CurrentNowFunc = func() time.Time { return time.Now().AddDate(1, 0, 0) }

	rebuiltPath := filepath.Join(t.TempDir(), "rebuilt.db")
	rebuilt, err := CreateDB(rebuiltPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()

	if err := rebuildFrom(rebuilt, shuffled); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	want := applyDump(t, source)
	got := applyDump(t, rebuilt)
	if want != got {
		t.Errorf("rebuild from a shuffled log differs.\n--- original ---\n%s\n--- rebuilt ---\n%s", want, got)
	}
}

// Rebuilding twice from the same log must land on the same tables, and a
// second shuffle must not change the answer either.
func TestApplyDeterminism_RebuildIsStableAcrossShuffles(t *testing.T) {
	source := SetupTestDB(t)
	driveTaskFamily(t, source)

	events, err := cachedEnvelopes(source)
	if err != nil {
		t.Fatal(err)
	}

	var dumps []string
	for seed := range 3 {
		shuffled := append([]eventlog.Envelope(nil), events...)
		rng := rand.New(rand.NewSource(int64(seed) + 1))
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		db, err := CreateDB(filepath.Join(t.TempDir(), "rebuilt.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := rebuildFrom(db, shuffled); err != nil {
			t.Fatalf("rebuild seed %d: %v", seed, err)
		}
		dumps = append(dumps, applyDump(t, db))
		db.Close()
	}
	for i := 1; i < len(dumps); i++ {
		if dumps[i] != dumps[0] {
			t.Errorf("shuffle %d rebuilt differently:\n--- 0 ---\n%s\n--- %d ---\n%s", i, dumps[0], i, dumps[i])
		}
	}
}

// Task timestamps come from the event's ts, not from the clock at apply time
// — that is what makes a rebuild reproduce them.
func TestApplyDeterminism_TaskTimestampsComeFromEventTS(t *testing.T) {
	db := SetupTestDB(t)
	driveTaskFamily(t, db)

	events, err := cachedEnvelopes(db)
	if err != nil {
		t.Fatal(err)
	}
	eventlog.Sort(events)

	// created_at is the ts of the task's created event; updated_at is the ts
	// of the last event that touched it.
	wantCreated := map[string]int64{}
	wantUpdated := map[string]int64{}
	for _, e := range events {
		switch EventType(e.Type) {
		case EventCreated:
			var p CreatedPayload
			if err := decodeEventPayload(e, &p); err != nil {
				t.Fatal(err)
			}
			wantCreated[p.ShortID] = e.TS / 1000
			wantUpdated[p.ShortID] = e.TS / 1000
		case EventEdited, EventNoted, EventMoved, EventReparented,
			EventDone, EventReopened, EventCanceled, EventReleased:
			if e.Task != "" {
				wantUpdated[e.Task] = e.TS / 1000
			}
		}
	}

	rows, err := db.Query("SELECT short_id, created_at, updated_at FROM tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var seen int
	for rows.Next() {
		var sid string
		var created, updated int64
		if err := rows.Scan(&sid, &created, &updated); err != nil {
			t.Fatal(err)
		}
		seen++
		if want, ok := wantCreated[sid]; ok && want != created {
			t.Errorf("task %s created_at = %d, want its created event's ts/1000 = %d", sid, created, want)
		}
		if want, ok := wantUpdated[sid]; ok && want != updated {
			t.Errorf("task %s updated_at = %d, want its last event's ts/1000 = %d", sid, updated, want)
		}
	}
	if seen == 0 {
		t.Fatal("no tasks survived the sequence")
	}
}

// A closed task's completion timestamp is the close event's ts. The tasks
// table has no separate closed column — updated_at is stamped by the close —
// so this asserts on that, per task, against the done/canceled event.
func TestApplyDeterminism_CloseStampsUpdatedAtFromEventTS(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	if _, _, err := RunDone(db, []string{leaf}, false, "done", nil, TestActor, false, ""); err != nil {
		t.Fatal(err)
	}

	events, err := cachedEnvelopes(db)
	if err != nil {
		t.Fatal(err)
	}
	closeTS := map[string]int64{}
	for _, e := range events {
		if EventType(e.Type) == EventDone {
			closeTS[e.Task] = e.TS / 1000
		}
	}
	if len(closeTS) != 2 {
		t.Fatalf("expected a done for the leaf and its auto-closed parent, got %v", closeTS)
	}
	for sid, ts := range closeTS {
		task := MustGet(t, db, sid)
		if task.Status != "done" {
			t.Errorf("%s status = %s, want done", sid, task.Status)
		}
		if task.UpdatedAt != ts {
			t.Errorf("%s updated_at = %d, want the done event's ts/1000 = %d", sid, task.UpdatedAt, ts)
		}
	}
}

// One checkout is one replica, and its seq counts from 1 with no gaps. A
// reader has to be able to tell a truncated log from a complete one.
func TestApplyDeterminism_SeqIsGaplessPerReplica(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	child := MustAdd(t, db, root, "Child")
	if err := RunNote(db, child, "a note", nil, TestActor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RunDone(db, []string{child}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatal(err)
	}

	events, err := cachedEnvelopes(db)
	if err != nil {
		t.Fatal(err)
	}
	byRep := map[string][]uint64{}
	for _, e := range events {
		byRep[e.Rep] = append(byRep[e.Rep], e.Seq)
	}
	if len(byRep) != 1 {
		t.Fatalf("one checkout is one replica; got %d: %v", len(byRep), byRep)
	}
	for rep, seqs := range byRep {
		slices.Sort(seqs)
		for i, s := range seqs {
			if s != uint64(i+1) {
				t.Fatalf("replica %s seq %d at position %d; seq must be gapless from 1: %v", rep, s, i, seqs)
			}
		}
	}
}

// Purge is the one operation that leaves the cache's seq with holes: it
// erases the purged subtree's event rows, and those rows are this replica's
// own events. The holes are a property of the cache, not of the record — the
// log file is append-only, so the store leaf's reader still sees a gapless
// run and a truncated file is still detectable. What must hold here is that
// no seq is ever reused or handed out backwards.
func TestApplyDeterminism_PurgeLeavesSeqUniqueAndIncreasing(t *testing.T) {
	db := SetupTestDB(t)
	driveTaskFamily(t, db)

	events, err := cachedEnvelopes(db)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint64]bool{}
	var last uint64
	for _, e := range events {
		if seen[e.Seq] {
			t.Fatalf("seq %d handed out twice", e.Seq)
		}
		seen[e.Seq] = true
		if e.Seq <= last {
			t.Fatalf("seq went backwards: %d after %d", e.Seq, last)
		}
		last = e.Seq
	}

	// A purge erased rows, so the highest seq outruns the count that survives.
	if last <= uint64(len(events)) {
		t.Fatalf("expected the purge to erase rows: highest seq %d, %d rows", last, len(events))
	}
}
