package job

import (
	"testing"
)

// P8 red: GetEventsForTaskTree with empty shortID anchors on all top-level
// tasks (parent_id IS NULL) and walks down. Exercises the full database.
func TestGetEventsForTaskTree_EmptyAnchor_ReturnsAllTrees(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Alpha")
	b := MustAdd(t, db, "", "Beta")
	aChild := MustAdd(t, db, a, "AlphaChild")

	events, err := GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("GetEventsForTaskTree(\"\"): %v", err)
	}

	seen := map[string]bool{}
	for _, e := range events {
		seen[e.ShortID] = true
	}
	for _, id := range []string{a, b, aChild} {
		if !seen[id] {
			t.Errorf("missing events for %s in global log; seen=%v", id, seen)
		}
	}

	// Chronological order: ids are sortable by CreatedAt then id.
	for i := 1; i < len(events); i++ {
		if events[i-1].CreatedAt > events[i].CreatedAt {
			t.Errorf("events out of chronological order at %d", i)
		}
	}
}

// Empty anchor with since filter still filters globally.
func TestGetEventsForTaskTreeSince_EmptyAnchor(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Alpha")
	b := MustAdd(t, db, "", "Beta")

	// Push a's events into the past, keep b's after cutoff.
	if _, err := db.Exec(
		"UPDATE events SET created_at = 100 WHERE task_id = (SELECT id FROM tasks WHERE short_id = ?)", a,
	); err != nil {
		t.Fatalf("update a: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE events SET created_at = 500 WHERE task_id = (SELECT id FROM tasks WHERE short_id = ?)", b,
	); err != nil {
		t.Fatalf("update b: %v", err)
	}

	events, err := getEventsForTaskTreeSince(db, "", 300)
	if err != nil {
		t.Fatalf("getEventsForTaskTreeSince(\"\", 300): %v", err)
	}
	for _, e := range events {
		if e.ShortID == a {
			t.Errorf("event for a should have been filtered out by since=300: %+v", e)
		}
	}
	if len(events) == 0 {
		t.Errorf("expected at least one event for b")
	}
}

// Empty anchor with afterID is the basis for global `tail`.
func TestGetEventsAfterPosition_EmptyAnchor(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Alpha")
	_ = a

	// Snapshot current event ids globally.
	all, err := GetEventsForTaskTree(db, "")
	if err != nil {
		t.Fatalf("GetEventsForTaskTree(\"\"): %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected events")
	}
	lastPos := all[len(all)-1].Position()

	// New tree after snapshot — must show up in global after-position stream.
	b := MustAdd(t, db, "", "Beta")

	more, err := GetEventsAfterPosition(db, "", lastPos)
	if err != nil {
		t.Fatalf("GetEventsAfterPosition(\"\", %v): %v", lastPos, err)
	}
	var sawB bool
	for _, e := range more {
		if e.ShortID == b {
			sawB = true
		}
	}
	if !sawB {
		t.Errorf("expected new event for %s in global after-position stream; got %+v", b, more)
	}
}
