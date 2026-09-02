package job

import (
	"database/sql"
	"testing"
)

// keySnapshot maps every task's short id to its sort key.
func keySnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query("SELECT short_id, sort_key FROM tasks")
	if err != nil {
		t.Fatalf("snapshot keys: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var short, key string
		if err := rows.Scan(&short, &key); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[short] = key
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// assertOnlyChanged fails if any task other than those named has a
// different sort key than it had in `before`. Fractional keys exist so
// that placing one row never rewrites another's.
func assertOnlyChanged(t *testing.T, before, after map[string]string, changed ...string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, c := range changed {
		allowed[c] = true
	}
	for short, key := range before {
		newKey, ok := after[short]
		if !ok {
			continue
		}
		if key != newKey && !allowed[short] {
			t.Errorf("task %s: sort key changed from %q to %q; no sibling's key may move", short, key, newKey)
		}
	}
}

func childOrder(t *testing.T, db *sql.DB, parentShortID string) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT t.short_id FROM tasks t JOIN tasks p ON p.id = t.parent_id
		WHERE p.short_id = ? AND t.deleted_at IS NULL
		ORDER BY t.sort_key`, parentShortID)
	if err != nil {
		t.Fatalf("child order: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func TestAddBefore_LeavesEverySiblingKeyAlone(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	a := MustAdd(t, db, parent, "A")
	b := MustAdd(t, db, parent, "B")
	c := MustAdd(t, db, parent, "C")

	before := keySnapshot(t, db)
	res, err := RunAdd(db, parent, "New", "", b, nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd --before: %v", err)
	}
	after := keySnapshot(t, db)
	assertOnlyChanged(t, before, after, res.ShortID)

	want := []string{a, res.ShortID, b, c}
	got := childOrder(t, db, parent)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("child order: got %v, want %v", got, want)
		}
	}
}

func TestMove_LeavesEverySiblingKeyAlone(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	a := MustAdd(t, db, parent, "A")
	b := MustAdd(t, db, parent, "B")
	c := MustAdd(t, db, parent, "C")
	d := MustAdd(t, db, parent, "D")

	before := keySnapshot(t, db)
	if err := RunMove(db, d, "before", b, TestActor); err != nil {
		t.Fatalf("RunMove: %v", err)
	}
	after := keySnapshot(t, db)
	assertOnlyChanged(t, before, after, d)

	want := []string{a, d, b, c}
	got := childOrder(t, db, parent)
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("child order: got %v, want %v", got, want)
		}
	}
}

func TestReparent_LeavesEverySiblingKeyAlone(t *testing.T) {
	db := SetupTestDB(t)
	src := MustAdd(t, db, "", "Source")
	dst := MustAdd(t, db, "", "Destination")
	movee := MustAdd(t, db, src, "Movee")
	stay := MustAdd(t, db, src, "Stay")
	first := MustAdd(t, db, dst, "First")
	second := MustAdd(t, db, dst, "Second")

	before := keySnapshot(t, db)
	if err := RunReparent(db, movee, dst, "after", first, TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}
	after := keySnapshot(t, db)
	assertOnlyChanged(t, before, after, movee)

	if got := childOrder(t, db, src); len(got) != 1 || got[0] != stay {
		t.Fatalf("source children: got %v, want [%s]", got, stay)
	}
	want := []string{first, movee, second}
	got := childOrder(t, db, dst)
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("destination order: got %v, want %v", got, want)
		}
	}
}

func TestAddCriteria_LeavesExistingCriterionKeysAlone(t *testing.T) {
	db := SetupTestDB(t)
	task := MustAdd(t, db, "", "Task")
	if _, err := RunAddCriteria(db, task, []Criterion{{Label: "first"}, {Label: "second"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	pt := MustGet(t, db, task)
	beforeCriteria, err := GetCriteria(db, pt.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}

	if _, err := RunAddCriteria(db, task, []Criterion{{Label: "third"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria (second call): %v", err)
	}
	afterCriteria, err := GetCriteria(db, pt.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	if len(afterCriteria) != 3 {
		t.Fatalf("got %d criteria, want 3", len(afterCriteria))
	}
	for i, c := range beforeCriteria {
		if afterCriteria[i].ShortID != c.ShortID || afterCriteria[i].SortKey != c.SortKey {
			t.Errorf("criterion %d (%q): key changed from %q to %q",
				i, c.Label, c.SortKey, afterCriteria[i].SortKey)
		}
	}
	if !(afterCriteria[1].SortKey < afterCriteria[2].SortKey) {
		t.Errorf("appended criterion key %q does not sort after %q",
			afterCriteria[2].SortKey, afterCriteria[1].SortKey)
	}
}
