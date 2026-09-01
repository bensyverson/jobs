package job

import (
	"strings"
	"testing"
)

// Decision 1 of project/2026-09-01-git-native-event-log.md: task ids mint
// six characters so two replicas minting apart rarely collide.
func TestGenerateShortIDIsSixBase62Characters(t *testing.T) {
	db := SetupTestDB(t)
	for range 20 {
		id := MustAdd(t, db, "", "width probe")
		if len(id) != 6 {
			t.Fatalf("short id %q has %d characters, want 6", id, len(id))
		}
		for _, c := range id {
			if !strings.ContainsRune(base62Chars, c) {
				t.Fatalf("short id %q carries %q, not base62", id, c)
			}
		}
	}
}

// Criterion ids are unique per task, not per table: every lookup and
// every event already carries the task, and a table-wide three-character
// space is the larger cross-replica collision hazard.
func TestCriterionShortIDsAreUniquePerTask(t *testing.T) {
	db := SetupTestDB(t)
	a := MustGet(t, db, MustAdd(t, db, "", "A"))
	b := MustGet(t, db, MustAdd(t, db, "", "B"))
	for _, task := range []*Task{a, b} {
		if _, err := db.Exec(
			`INSERT INTO task_criteria (task_id, short_id, label) VALUES (?, 'abc', 'shared id')`,
			task.ID,
		); err != nil {
			t.Fatalf("task %s cannot hold criterion abc: %v", task.ShortID, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO task_criteria (task_id, short_id, label) VALUES (?, 'abc', 'duplicate within task')`,
		a.ID,
	); err == nil {
		t.Fatal("a second criterion abc on the same task was accepted")
	}
}
