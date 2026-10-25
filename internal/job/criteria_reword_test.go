package job

import (
	"database/sql"
	"strings"
	"testing"
)

// Rewording a criterion replaces its label in place: same short id, same
// state, same position, and a criterion_reworded event that carries both
// wordings so `job log` keeps the old one.

func rewordFixture(t *testing.T) (*sql.DB, string, []Criterion) {
	t.Helper()
	db := SetupTestDB(t)
	task := MustAdd(t, db, "", "With criteria")
	crits, err := RunAddCriteria(db, task, []Criterion{{Label: "tests pass"}, {Label: "docs updated"}}, TestActor)
	if err != nil {
		t.Fatal(err)
	}
	return db, task, crits
}

func TestRunRewordCriterion_ByShortIDKeepsStateAndIdentity(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunSetCriterion(db, task, crits[0].ShortID, CriterionPassed, TestActor); err != nil {
		t.Fatal(err)
	}

	old, err := RunRewordCriterion(db, task, crits[0].ShortID, "unit and e2e tests pass", TestActor)
	if err != nil {
		t.Fatalf("reword: %v", err)
	}
	if old != "tests pass" {
		t.Errorf("returned old label = %q, want %q", old, "tests pass")
	}

	got, err := GetCriteria(db, MustGet(t, db, task).ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("criteria = %d, want 2 (a reword must not add a row)", len(got))
	}
	if got[0].Label != "unit and e2e tests pass" {
		t.Errorf("label = %q, want the new wording", got[0].Label)
	}
	if got[0].State != CriterionPassed {
		t.Errorf("state = %q, want passed: a reword leaves the state alone", got[0].State)
	}
	if got[0].ShortID != crits[0].ShortID || got[0].SortKey != crits[0].SortKey {
		t.Errorf("identity changed: %+v, was %+v", got[0], crits[0])
	}
	if got[1].Label != "docs updated" {
		t.Errorf("the other criterion reads %q", got[1].Label)
	}
}

func TestRunRewordCriterion_ByVerbatimLabel(t *testing.T) {
	db, task, _ := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, "docs updated", "the reference page is updated", TestActor); err != nil {
		t.Fatalf("reword by label: %v", err)
	}
	got, err := GetCriteria(db, MustGet(t, db, task).ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Label != "the reference page is updated" || got[1].State != CriterionPending {
		t.Errorf("criterion = %+v", got[1])
	}
}

func TestRunRewordCriterion_RecordsOldAndNewWording(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, crits[1].ShortID, "docs and help updated", TestActor); err != nil {
		t.Fatal(err)
	}
	detail, err := GetLatestEventDetail(db, MustGet(t, db, task).ID, string(EventCriterionReworded))
	if err != nil {
		t.Fatal(err)
	}
	if detail == nil {
		t.Fatal("no criterion_reworded event recorded")
	}
	if detail["short_id"] != crits[1].ShortID || detail["old_label"] != "docs updated" || detail["new_label"] != "docs and help updated" {
		t.Errorf("detail = %v", detail)
	}
}

func TestRunRewordCriterion_UnknownLabelNamesTheLabelAndTask(t *testing.T) {
	db, task, _ := rewordFixture(t)
	_, err := RunRewordCriterion(db, task, "zz9", "anything", TestActor)
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	for _, want := range []string{`"zz9"`, task, "job show " + task} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	detail, _ := GetLatestEventDetail(db, MustGet(t, db, task).ID, string(EventCriterionReworded))
	if detail != nil {
		t.Errorf("a failed reword recorded an event: %v", detail)
	}
}

func TestRunRewordCriterion_UnknownTask(t *testing.T) {
	db := SetupTestDB(t)
	if _, err := RunRewordCriterion(db, "nope1", "abc", "x", TestActor); err == nil || !strings.Contains(err.Error(), "nope1") {
		t.Errorf("err = %v, want one naming the missing task", err)
	}
}

func TestRunRewordCriterion_EmptyTextIsRefused(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, crits[0].ShortID, "   ", TestActor); err == nil {
		t.Fatal("expected an error for empty new text")
	}
	got, _ := GetCriteria(db, MustGet(t, db, task).ID)
	if got[0].Label != "tests pass" {
		t.Errorf("label = %q after a refused reword", got[0].Label)
	}
}

func TestRunRewordCriterion_TrimsTheNewText(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, crits[0].ShortID, "  padded  ", TestActor); err != nil {
		t.Fatal(err)
	}
	got, _ := GetCriteria(db, MustGet(t, db, task).ID)
	if got[0].Label != "padded" {
		t.Errorf("label = %q, want it trimmed as criteria_added trims", got[0].Label)
	}
}

func TestRunRewordCriterion_SameTextRecordsNothing(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, crits[0].ShortID, "tests pass", TestActor); err != nil {
		t.Fatalf("an identical reword should succeed as a no-op: %v", err)
	}
	detail, _ := GetLatestEventDetail(db, MustGet(t, db, task).ID, string(EventCriterionReworded))
	if detail != nil {
		t.Errorf("an identical reword recorded an event: %v", detail)
	}
}

// A later criterion_state on the reworded criterion still lands, and must not
// put the old wording back.
func TestRunRewordCriterion_LaterStateChangeKeepsNewWording(t *testing.T) {
	db, task, crits := rewordFixture(t)
	if _, err := RunRewordCriterion(db, task, crits[0].ShortID, "new words", TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := RunSetCriterion(db, task, "new words", CriterionFailed, TestActor); err != nil {
		t.Fatalf("set by the new wording: %v", err)
	}
	got, _ := GetCriteria(db, MustGet(t, db, task).ID)
	if got[0].Label != "new words" || got[0].State != CriterionFailed {
		t.Errorf("criterion = %+v", got[0])
	}
}

func TestApplyCriterionReworded_ResolvesByShortIDAndKeepsState(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")
	mustApply(t, db, applyEnv(t, EventCriteriaAdded, a, 900, CriteriaAddedPayload{
		Criteria: []CriterionEntry{{Label: "tests pass", State: "skipped", ShortID: "aB3", SortKey: "V0001"}},
	}))
	reword := applyEnv(t, EventCriterionReworded, a, 901, CriterionRewordedPayload{
		ShortID: "aB3", OldLabel: "tests pass", NewLabel: "all tests pass",
	})
	mustApply(t, db, reword)
	// Replaying the same line is harmless: it writes the same label again.
	mustApply(t, db, reword)

	got, err := GetCriteria(db, MustGet(t, db, a).ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "all tests pass" || got[0].State != CriterionSkipped {
		t.Errorf("criteria = %+v", got)
	}
}

func TestApplyCriterionReworded_UnknownCriterionIsANoOp(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Task")
	mustApply(t, db, applyEnv(t, EventCriterionReworded, a, 901, CriterionRewordedPayload{
		ShortID: "zzz", OldLabel: "never", NewLabel: "existed",
	}))
	got, _ := GetCriteria(db, MustGet(t, db, a).ID)
	if len(got) != 0 {
		t.Errorf("criteria = %+v, want none", got)
	}
}

func TestFormatEventDescription_CriterionReworded(t *testing.T) {
	got := FormatEventDescription(string(EventCriterionReworded),
		`{"short_id":"aB3","old_label":"tests pass","new_label":"all tests pass"}`)
	want := `criterion reworded: "tests pass" → "all tests pass"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
