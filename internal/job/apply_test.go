package job

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The events table is the cache's copy of the log's lines, so every row it
// holds carries the envelope's position. Rows written before the store
// existed keep rep '' and seq 0 with ts derived from created_at; they are
// history, and apply is never called on them.

func TestEventEnvelopeMigration_BackfillsLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// A database at the pre-0010 shape: open it, then hand-write a row the
	// way recordEvent used to, with no position.
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	root := MustAdd(t, db, "", "Root")
	var rootID int64
	if err := db.QueryRow("SELECT id FROM tasks WHERE short_id = ?", root).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO events (task_id, event_type, actor, detail, created_at, rep, seq, ts) VALUES (?,?,?,?,?,'',0,0)",
		rootID, "focus_set", "alice", `{"task":"x"}`, int64(1700000000),
	); err != nil {
		t.Fatal(err)
	}
	// The migration's backfill runs at open time; simulate the row arriving
	// before it by applying the same UPDATE the migration carries.
	if _, err := db.Exec("UPDATE events SET ts = created_at * 1000 WHERE ts = 0"); err != nil {
		t.Fatal(err)
	}

	var rep string
	var seq, ts, createdAt int64
	if err := db.QueryRow(
		"SELECT rep, seq, ts, created_at FROM events WHERE event_type = 'focus_set'",
	).Scan(&rep, &seq, &ts, &createdAt); err != nil {
		t.Fatal(err)
	}
	if rep != "" || seq != 0 {
		t.Errorf("legacy row should keep rep '' and seq 0, got rep %q seq %d", rep, seq)
	}
	if ts != createdAt*1000 {
		t.Errorf("legacy ts = %d, want created_at*1000 = %d", ts, createdAt*1000)
	}
	db.Close()
}

// cachedEnvelopes is what the store and adoption read the cache back with,
// and it must skip legacy rows: they were never replayable, which is exactly
// why apply is never called on them.
func TestCachedEnvelopes_SkipsLegacyRows(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	var rootID int64
	if err := db.QueryRow("SELECT id FROM tasks WHERE short_id = ?", root).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO events (task_id, event_type, actor, detail, created_at, rep, seq, ts) VALUES (?,?,?,?,?,'',0,?)",
		rootID, "focus_set", "alice", `{}`, int64(1700000000), int64(1700000000000),
	); err != nil {
		t.Fatal(err)
	}

	events, err := cachedEnvelopes(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "focus_set" {
			t.Fatalf("cachedEnvelopes returned a legacy row: %+v", e)
		}
		if e.Rep == "" {
			t.Fatalf("cachedEnvelopes returned an unpositioned row: %+v", e)
		}
	}
	if len(events) == 0 {
		t.Fatal("expected the created event")
	}
}

// Every write in this family goes through apply, so the recorder is what
// gives the checkout its replica id — minted once and kept in local.json.
func TestRecorder_MintsAndReusesTheReplicaID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	MustAdd(t, db, "", "First")
	state, err := LoadLocalState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !eventlog.ValidReplicaID(state.Rep) {
		t.Fatalf("local.json rep = %q, want a minted replica id", state.Rep)
	}
	if state.LastSeen == 0 {
		t.Error("local.json should carry the clock watermark after a write")
	}

	MustAdd(t, db, "", "Second")
	again, err := LoadLocalState(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Rep != state.Rep {
		t.Errorf("replica id changed between commands: %q then %q", state.Rep, again.Rep)
	}
	if again.LastSeen < state.LastSeen {
		t.Errorf("clock went backwards: %d then %d", state.LastSeen, again.LastSeen)
	}
}

// apply is a total function: an event for a type it does not own, or for a
// task that no longer exists, records the row and touches no state.
func TestApply_IsTotal(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	env := eventlog.Envelope{
		V: eventlog.Version, Rep: "aaaaaa", Seq: 1, TS: 1700000000000,
		Actor: "alice", Type: "teleported", Task: "nosuch",
		Data: json.RawMessage(`{"text":"unknown to this cache"}`),
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(tx, env); err != nil {
		t.Fatalf("apply should tolerate an unknown type: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("unknown event changed the tasks table: %d then %d", before, got)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = 'teleported'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("unknown event should still be recorded once, got %d rows", n)
	}
}
