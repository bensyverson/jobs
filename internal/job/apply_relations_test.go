package job

import (
	"database/sql"
	"encoding/json"
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The relations, criteria, provenance and kind family through apply.
//
// Two properties matter here and neither is visible from the handlers. The
// set-membership types (blocked/unblocked, labeled/unlabeled) and
// criteria_added must be idempotent, because a rebuild replays them and a
// merge can deliver the same line twice. And apply must never look anything
// up beyond resolving a short id to a row id: every criterion id, label name,
// sort key and kind arrives in the payload.

// applyEnv builds a positioned envelope for a direct apply call. Handlers
// mint these through the recorder; these tests need to hand apply an exact
// event, including one it has already seen.
func applyEnv(t *testing.T, typ EventType, task string, seq uint64, payload any) eventlog.Envelope {
	t.Helper()
	var data json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		data = b
	}
	return eventlog.Envelope{
		V:     eventlog.Version,
		Rep:   "testrep",
		Seq:   seq,
		TS:    1700000000000 + int64(seq),
		Actor: TestActor,
		Type:  eventlog.Type(typ),
		Task:  task,
		Data:  data,
	}
}

// mustApply runs one envelope through apply in its own transaction.
func mustApply(t *testing.T, db *sql.DB, e eventlog.Envelope) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := apply(tx, e); err != nil {
		tx.Rollback()
		t.Fatalf("apply %s: %v", e.Type, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestApplyBlocked_IsIdempotent(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Blocked")
	b := MustAdd(t, db, "", "Blocker")

	p := BlockedPayload{BlockedID: a, BlockerID: b}
	mustApply(t, db, applyEnv(t, EventBlocked, a, 900, p))
	mustApply(t, db, applyEnv(t, EventBlocked, a, 901, p))

	if n := countRows(t, db, "blocks"); n != 1 {
		t.Fatalf("blocks rows = %d, want 1 after replaying the same blocked event", n)
	}
}

func TestApplyUnblocked_MissingPairIsANoOp(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Blocked")
	b := MustAdd(t, db, "", "Blocker")

	// No edge exists; applying unblocked must not error and must not write.
	mustApply(t, db, applyEnv(t, EventUnblocked, a, 900,
		UnblockedPayload{BlockedID: a, BlockerID: b, Reason: UnblockManual}))
	if n := countRows(t, db, "blocks"); n != 0 {
		t.Fatalf("blocks rows = %d, want 0", n)
	}
}

func TestApplyBlocked_UnknownTaskIsANoOp(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Blocked")

	mustApply(t, db, applyEnv(t, EventBlocked, a, 900,
		BlockedPayload{BlockedID: a, BlockerID: "nOsUcH"}))
	if n := countRows(t, db, "blocks"); n != 0 {
		t.Fatalf("blocks rows = %d, want 0 for an event naming a task this cache has not seen", n)
	}
}

func TestApplyLabeled_IsIdempotent(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")

	p := LabeledPayload{Names: []string{"store", "refactor"}, Existing: []string{}}
	mustApply(t, db, applyEnv(t, EventLabeled, a, 900, p))
	mustApply(t, db, applyEnv(t, EventLabeled, a, 901, p))

	if n := countRows(t, db, "task_labels"); n != 2 {
		t.Fatalf("task_labels rows = %d, want 2", n)
	}
}

func TestApplyUnlabeled_AbsentNameIsANoOp(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")

	mustApply(t, db, applyEnv(t, EventUnlabeled, a, 900,
		UnlabeledPayload{Names: []string{"never-set"}, Absent: []string{"never-set"}}))
	if n := countRows(t, db, "task_labels"); n != 0 {
		t.Fatalf("task_labels rows = %d, want 0", n)
	}
}

func TestApplyCriteriaAdded_UsesPayloadShortIDAndIsIdempotent(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")

	p := CriteriaAddedPayload{Criteria: []CriterionEntry{
		{Label: "tests pass", State: "pending", ShortID: "aB3", SortKey: "V0001"},
		{Label: "docs updated", State: "skipped", ShortID: "zZ9", SortKey: "V0002"},
	}}
	mustApply(t, db, applyEnv(t, EventCriteriaAdded, a, 900, p))
	mustApply(t, db, applyEnv(t, EventCriteriaAdded, a, 901, p))

	task := MustGet(t, db, a)
	got, err := GetCriteria(db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("criteria = %d, want 2 after replaying the same criteria_added", len(got))
	}
	if got[0].ShortID != "aB3" || got[0].SortKey != "V0001" {
		t.Errorf("first criterion = %+v, want the payload's short id and sort key", got[0])
	}
	if got[1].State != CriterionSkipped {
		t.Errorf("second criterion state = %q, want skipped", got[1].State)
	}
	// created_at comes from the event, never the clock.
	var createdAt int64
	if err := db.QueryRow(
		"SELECT created_at FROM task_criteria WHERE task_id = ? AND short_id = 'aB3'", task.ID,
	).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if want := (1700000000000 + int64(900)) / 1000; createdAt != want {
		t.Errorf("criterion created_at = %d, want the event's ts/1000 = %d", createdAt, want)
	}
}

func TestApplyCriterionState_ResolvesByShortID(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")
	mustApply(t, db, applyEnv(t, EventCriteriaAdded, a, 900, CriteriaAddedPayload{
		Criteria: []CriterionEntry{{Label: "tests pass", State: "pending", ShortID: "aB3", SortKey: "V0001"}},
	}))
	mustApply(t, db, applyEnv(t, EventCriterionState, a, 901, CriterionStatePayload{
		Label: "a label that has since been edited", State: "passed", Prior: "pending", ShortID: "aB3",
	}))

	task := MustGet(t, db, a)
	got, err := GetCriteria(db, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].State != CriterionPassed {
		t.Errorf("state = %q, want passed", got[0].State)
	}
	if got[0].Label != "tests pass" {
		t.Errorf("label = %q; criterion_state must not rewrite the label", got[0].Label)
	}
}

func TestApplyFoundIn_SetReplacesAndClearedRemoves(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Defect")
	s1 := MustAdd(t, db, "", "Source one")
	s2 := MustAdd(t, db, "", "Source two")

	mustApply(t, db, applyEnv(t, EventFoundInSet, a, 900, FoundInSetPayload{TaskID: a, SourceID: s1}))
	mustApply(t, db, applyEnv(t, EventFoundInSet, a, 901,
		FoundInSetPayload{TaskID: a, SourceID: s2, PreviousSourceID: s1}))

	src, err := GetFoundInSource(db, a)
	if err != nil {
		t.Fatal(err)
	}
	if src == nil || src.ShortID != s2 {
		t.Fatalf("source = %v, want %s", src, s2)
	}

	mustApply(t, db, applyEnv(t, EventFoundInCleared, a, 902, FoundInClearedPayload{TaskID: a, SourceID: s2}))
	if n := countRows(t, db, "found_in"); n != 0 {
		t.Fatalf("found_in rows = %d, want 0", n)
	}
	// Clearing again is a no-op, not an error.
	mustApply(t, db, applyEnv(t, EventFoundInCleared, a, 903, FoundInClearedPayload{TaskID: a, SourceID: s2}))
}

func TestApplyKindChanged_WritesTheKindFromThePayload(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Root")

	mustApply(t, db, applyEnv(t, EventKindChanged, a, 900, KindChangedPayload{From: "task", To: "issue"}))
	if got := MustGet(t, db, a).Kind; got != KindIssue {
		t.Errorf("kind = %q, want issue", got)
	}
	mustApply(t, db, applyEnv(t, EventKindChanged, a, 901, KindChangedPayload{From: "issue", To: "task"}))
	if got := MustGet(t, db, a).Kind; got != KindTask {
		t.Errorf("kind = %q, want task", got)
	}
}

// driveRelationsFamily exercises every handler in this family, so the
// position check below and the determinism test share one sequence.
func driveRelationsFamily(t *testing.T, db *sql.DB) {
	t.Helper()
	const actor = "agent-relations"

	root := MustAdd(t, db, "", "Relations root")
	one := MustAdd(t, db, root, "One")
	two := MustAdd(t, db, root, "Two")
	three := MustAdd(t, db, root, "Three")

	if err := RunBlockMany(db, two, []string{one, three}, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunUnblockMany(db, two, []string{three}, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelAdd(db, one, []string{"store", "refactor"}, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunLabelRemove(db, one, []string{"refactor"}, actor); err != nil {
		t.Fatal(err)
	}
	crits, err := RunAddCriteria(db, two, []Criterion{{Label: "alpha"}, {Label: "beta"}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunSetCriterion(db, two, crits[0].ShortID, CriterionPassed, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunSetFoundIn(db, three, one, actor); err != nil {
		t.Fatal(err)
	}
	if err := RunClearFoundIn(db, three, actor); err != nil {
		t.Fatal(err)
	}

	// An inline-labeled add, and an issue root converted back to a task-tree.
	if _, err := RunAdd(db, root, "Inline labels", "", "", []string{"inline", "store"}, actor); err != nil {
		t.Fatal(err)
	}
	issue, err := RunAddKind(db, "", "An issue root", "", "", nil, actor, KindIssue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunSetKind(db, issue.ShortID, KindTask, actor); err != nil {
		t.Fatal(err)
	}

	// Closing `one` auto-unblocks `two`, which is the unblocked event the
	// close path emits rather than deleting the edge itself.
	if _, _, err := RunDone(db, []string{one}, false, "blocker closed", nil, actor, false, ""); err != nil {
		t.Fatal(err)
	}
	// And a cancel-triggered unblock on a second edge.
	four := MustAdd(t, db, root, "Four")
	five := MustAdd(t, db, root, "Five")
	if err := RunBlockMany(db, five, []string{four}, actor); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := RunCancel(db, []string{four}, "not needed", false, false, false, actor); err != nil {
		t.Fatal(err)
	}
	// Found-in on a task nothing later touches, so its row survives the tail.
	surfaced := MustAdd(t, db, "", "Surfaced late")
	if err := RunSetFoundIn(db, surfaced, root, actor); err != nil {
		t.Fatal(err)
	}
}

// The criterion: no handler in this family writes a state table directly.
// A direct write is invisible to a rebuild, and the marker it leaves is an
// event row with no position — recordEvent stamps rep ” and seq 0.
func TestRelationsHandlers_EmitPositionedEventsOnly(t *testing.T) {
	db := SetupTestDB(t)
	driveRelationsFamily(t, db)

	rows, err := db.Query(
		"SELECT event_type, COUNT(*) FROM events WHERE rep = '' GROUP BY event_type")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int
		if err := rows.Scan(&typ, &n); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%d %s event(s) recorded with no position; the handler still writes state directly", n, typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// A relations sequence rebuilds identically from a shuffled log.
func TestApplyDeterminism_RelationsFamilyRebuildsIdentically(t *testing.T) {
	source := SetupTestDB(t)
	driveRelationsFamily(t, source)

	events, err := cachedEnvelopes(source)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20260902))
	shuffled := append([]eventlog.Envelope(nil), events...)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	restore := CurrentNowFunc
	t.Cleanup(func() { CurrentNowFunc = restore })
	CurrentNowFunc = func() time.Time { return time.Now().AddDate(1, 0, 0) }

	rebuilt, err := CreateDB(filepath.Join(t.TempDir(), "rebuilt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilt.Close()
	if err := rebuildFrom(rebuilt, shuffled); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if want, got := applyDump(t, source), applyDump(t, rebuilt); want != got {
		t.Errorf("rebuild differs.\n--- original ---\n%s\n--- rebuilt ---\n%s", want, got)
	}
}
