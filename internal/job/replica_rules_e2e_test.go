package job

import (
	"database/sql"
	"slices"
	"testing"
)

// One test per row of the merge-rule table in
// project/2026-09-01-git-native-event-log.md ("Merge rule per event type").
//
// Each runs on a fresh pair of replicas, diverges them deliberately, exchanges
// the log files, and asserts the rule's outcome on BOTH sides — a rule that
// only holds on the machine that happened to pull second is not a merge rule.
//
// This file is the task lifecycle: created, edited, done, canceled, reopened,
// noted, purged, and the cascade neither machine can emit. The claims and
// relations rows are in replica_relations_e2e_test.go; the harness, the
// `snapshot` row and the short-id collision are in replica_e2e_test.go.

// `created`: idempotent by short id, so each side's task arrives whole on the
// other — description, parent, labels and criteria included. (The collision
// half of this row is TestTwoReplicas_CreatedCollisionFailsThenRekeyConverges.)
func TestMergeRule_Created(t *testing.T) {
	p := newPair(t)
	var shared string
	p.seed(func(db *sql.DB) { shared = MustAdd(t, db, "", "Shared plan") })

	var fromA, fromB string
	p.A.do(func(db *sql.DB) {
		fromA = MustAddDesc(t, db, shared, "Made on A", "A's description")
		if _, err := RunLabelAdd(db, fromA, []string{"alpha"}, "ben"); err != nil {
			t.Fatal(err)
		}
		if _, err := RunAddCriteria(db, fromA, []Criterion{{Label: "A's criterion"}}, "ben"); err != nil {
			t.Fatal(err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		fromB = MustAddDesc(t, db, shared, "Made on B", "B's description")
		if _, err := RunLabelAdd(db, fromB, []string{"beta"}, "sam"); err != nil {
			t.Fatal(err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, fromA, "title", "Made on A")
		wantField(t, db, fromA, "description", "A's description")
		wantField(t, db, fromB, "title", "Made on B")
		wantField(t, db, fromB, "description", "B's description")
		if got := labelsOf(t, db, fromA); !slices.Equal(got, []string{"alpha"}) {
			t.Fatalf("labels on A's task = %v", got)
		}
		if got := labelsOf(t, db, fromB); !slices.Equal(got, []string{"beta"}) {
			t.Fatalf("labels on B's task = %v", got)
		}
		var crit int
		if err := db.QueryRow(`SELECT COUNT(*) FROM task_criteria c JOIN tasks t ON t.id = c.task_id
			WHERE t.short_id = ?`, fromA).Scan(&crit); err != nil {
			t.Fatal(err)
		}
		if crit != 1 {
			t.Fatalf("A's criterion did not arrive: %d rows", crit)
		}
		// One created event, one row, however many files carry it.
		var rows int
		if err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE short_id IN (?, ?)", fromA, fromB).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 2 {
			t.Fatalf("created is not idempotent by short id: %d rows", rows)
		}
	})
}

// `edited`: per field, the later (ts, rep) wins. Two machines editing
// different fields both survive; two editing the same field resolve to the
// later one, on both machines.
func TestMergeRule_Edited(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAddDesc(t, db, "", "original title", "original description") })

	p.A.do(func(db *sql.DB) { mustEdit(t, db, task, "title from A", "", "ben") })
	p.tick()
	p.B.do(func(db *sql.DB) { mustEdit(t, db, task, "", "description from B", "sam") })
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "title", "title from A")
		wantField(t, db, task, "description", "description from B")
	})

	// Now the same field from both sides: the later edit wins everywhere.
	p.tick()
	p.A.do(func(db *sql.DB) { mustEdit(t, db, task, "A got there first", "", "ben") })
	p.tick()
	p.B.do(func(db *sql.DB) { mustEdit(t, db, task, "B edited later", "", "sam") })
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "title", "B edited later")
		wantField(t, db, task, "description", "description from B")
	})
}

// `done` against `noted`: not a conflict at all. One machine closes a task
// while the other writes a note on it, at the same instant; both apply.
func TestMergeRule_DoneVersusNote(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "closed and annotated") })

	// Deliberately no tick between them: the two events share a ts and are
	// ordered by rep alone, which is the closest two machines can come to
	// acting at the same time.
	p.A.do(func(db *sql.DB) {
		if _, _, err := RunDone(db, []string{task}, false, "shipped", nil, "ben", false, ""); err != nil {
			t.Fatalf("done on A: %v", err)
		}
	})
	p.B.do(func(db *sql.DB) {
		if err := RunNote(db, task, "one more thing", nil, "sam"); err != nil {
			t.Fatalf("note on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "status", "done")
		if got := notesOn(t, db, task); !slices.Contains(got, "one more thing") {
			t.Fatalf("the note is not in the history: %v", got)
		}
	})
}

// `done`, `canceled`, `reopened`: status is whatever the latest transition
// says, and a repeated done is a no-op.
func TestMergeRule_DoneCanceledReopened(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "closed then reopened") })

	p.A.do(func(db *sql.DB) {
		if _, _, err := RunDone(db, []string{task}, false, "", nil, "ben", false, ""); err != nil {
			t.Fatalf("done on A: %v", err)
		}
	})
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) { wantField(t, db, task, "status", "done") })

	// A closes it again; the handler recognises it is already closed and the
	// log gains nothing.
	p.tick()
	p.A.do(func(db *sql.DB) {
		closed, already, err := RunDone(db, []string{task}, false, "", nil, "ben", false, "")
		if err != nil {
			t.Fatalf("repeat done: %v", err)
		}
		if len(closed) != 0 || !slices.Contains(already, task) {
			t.Fatalf("a repeated done was not a no-op: closed %v, already %v", closed, already)
		}
	})
	// Meanwhile B reopens it, later.
	p.tick()
	p.B.do(func(db *sql.DB) {
		if _, err := RunReopen(db, task, false, "sam"); err != nil {
			t.Fatalf("reopen on B: %v", err)
		}
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, task, "status", "available")
		if n := countEvents(t, db, task, EventDone, ""); n != 1 {
			t.Fatalf("done events on %s = %d, want 1 — the repeat should have written nothing", task, n)
		}
	})
}

// `purged`: a tombstone. A child added under a purged root on the other
// machine is purged by reconcile on BOTH machines, and the repair is a real
// log entry, not a cache-only fixup.
func TestMergeRule_PurgedVersusAddChild(t *testing.T) {
	p := newPair(t)
	var root string
	p.seed(func(db *sql.DB) { root = MustAdd(t, db, "", "doomed root") })

	p.A.do(func(db *sql.DB) {
		if _, _, _, err := RunCancel(db, []string{root}, "wrong tree", false, true, true, "ben"); err != nil {
			t.Fatalf("purge on A: %v", err)
		}
	})
	p.tick()
	var child string
	p.B.do(func(db *sql.DB) { child = MustAdd(t, db, root, "added while apart") })
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		if taskExists(t, db, root) {
			t.Fatalf("the purged root came back")
		}
		if taskExists(t, db, child) {
			t.Fatalf("the child of a purged root survived")
		}
	})
	for _, r := range []*replica{p.A, p.B} {
		if n := r.countLogEvents(EventPurged, child); n == 0 {
			t.Fatalf("%s repaired the cache but wrote no purge to its log", r.name)
		}
	}
}

// `noted`: append-only, never conflicts. Notes from both machines interleave
// by log position, identically on both.
func TestMergeRule_Noted(t *testing.T) {
	p := newPair(t)
	var task string
	p.seed(func(db *sql.DB) { task = MustAdd(t, db, "", "much discussed") })

	p.A.do(func(db *sql.DB) {
		if err := RunNote(db, task, "first, from A", nil, "ben"); err != nil {
			t.Fatal(err)
		}
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if err := RunNote(db, task, "second, from B", nil, "sam"); err != nil {
			t.Fatal(err)
		}
	})
	p.tick()
	p.A.do(func(db *sql.DB) {
		if err := RunNote(db, task, "third, from A", nil, "ben"); err != nil {
			t.Fatal(err)
		}
	})
	p.exchange()

	want := []string{"first, from A", "second, from B", "third, from A"}
	p.bothSides(func(t *testing.T, db *sql.DB) {
		if got := notesOn(t, db, task); !slices.Equal(got, want) {
			t.Fatalf("notes = %v, want %v", got, want)
		}
	})
}

// The cascade the design gives up on inside apply: a parent whose two children
// closed on two different machines. Neither machine could emit the parent's
// close, because neither saw both. Reconcile appends it — on both machines,
// and into the log, not just the cache.
func TestMergeRule_CascadeSplitAcrossReplicas(t *testing.T) {
	p := newPair(t)
	var parent, one, two string
	p.seed(func(db *sql.DB) {
		parent = MustAdd(t, db, "", "the parent")
		one = MustAdd(t, db, parent, "child one")
		two = MustAdd(t, db, parent, "child two")
	})

	p.A.do(func(db *sql.DB) {
		if _, _, err := RunDone(db, []string{one}, false, "", nil, "ben", false, ""); err != nil {
			t.Fatalf("close on A: %v", err)
		}
		wantField(t, db, parent, "status", "available")
	})
	p.tick()
	p.B.do(func(db *sql.DB) {
		if _, _, err := RunDone(db, []string{two}, false, "", nil, "sam", false, ""); err != nil {
			t.Fatalf("close on B: %v", err)
		}
		wantField(t, db, parent, "status", "available")
	})
	p.exchange()

	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, parent, "status", "done")
		if n := countEvents(t, db, parent, EventDone, reconcileActor); n == 0 {
			t.Fatalf("the parent closed with no reconcile event to explain it")
		}
	})
	for _, r := range []*replica{p.A, p.B} {
		found := false
		for _, e := range r.logEvents() {
			if EventType(e.Type) == EventDone && e.Task == parent && e.Actor == reconcileActor {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s closed the parent in its cache but wrote no event to its log", r.name)
		}
	}

	// Idempotent: a second exchange carries the other side's repair and
	// changes nothing.
	p.exchange()
	p.bothSides(func(t *testing.T, db *sql.DB) {
		wantField(t, db, parent, "status", "done")
	})
}
