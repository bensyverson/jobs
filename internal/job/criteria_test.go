package job

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInsertAndGetCriteria(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertCriteria(tx, pt.ID, []Criterion{
		{Label: "Tests pass"},
		{Label: "Docs updated", State: CriterionSkipped},
	}); err != nil {
		tx.Rollback()
		t.Fatalf("insertCriteria: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := GetCriteria(db, pt.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d criteria, want 2", len(got))
	}
	if got[0].Label != "Tests pass" || got[0].State != CriterionPending {
		t.Errorf("first: got %+v", got[0])
	}
	if got[1].Label != "Docs updated" || got[1].State != CriterionSkipped {
		t.Errorf("second: got %+v", got[1])
	}
	if !(got[0].SortKey < got[1].SortKey) {
		t.Errorf("sort order not ascending: %q %q", got[0].SortKey, got[1].SortKey)
	}
}

func TestInsertCriteria_RejectsEmptyLabel(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, _ := db.Begin()
	defer tx.Rollback()

	if _, err := insertCriteria(tx, pt.ID, []Criterion{{Label: "  "}}); err == nil {
		t.Fatal("expected error on empty label")
	}
}

func TestInsertCriteria_RejectsInvalidState(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, _ := db.Begin()
	defer tx.Rollback()

	if _, err := insertCriteria(tx, pt.ID, []Criterion{{Label: "X", State: "bogus"}}); err == nil {
		t.Fatal("expected error on bogus state")
	}
}

func TestSetCriterionState(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, _ := db.Begin()
	if _, err := insertCriteria(tx, pt.ID, []Criterion{{Label: "Tests pass"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx2, _ := db.Begin()
	resolved, prior, err := SetCriterionState(tx2, pt.ID, "Tests pass", CriterionPassed)
	if err != nil {
		tx2.Rollback()
		t.Fatalf("SetCriterionState: %v", err)
	}
	if prior != CriterionPending {
		t.Errorf("prior = %q, want pending", prior)
	}
	if resolved.Label != "Tests pass" || resolved.ShortID == "" {
		t.Errorf("resolved: got %+v, want non-empty short_id and matching label", resolved)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	got, _ := GetCriteria(db, pt.ID)
	if got[0].State != CriterionPassed {
		t.Errorf("state = %q, want passed", got[0].State)
	}
}

func TestSetCriterionState_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, _ := db.Begin()
	defer tx.Rollback()
	if _, _, err := SetCriterionState(tx, pt.ID, "nope", CriterionPassed); err == nil {
		t.Fatal("expected error for missing criterion")
	}
}

func TestImport_WithCriteria_BareStrings(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Verification gate\n" +
		"    criteria:\n" +
		"      - Tests pass\n" +
		"      - Docs updated\n" +
		"      - Manual smoke test\n" +
		"```\n"

	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	gate, _ := GetTaskByShortID(db, res.Tasks[0].ID)
	if gate == nil {
		t.Fatal("gate not found")
	}
	got, err := GetCriteria(db, gate.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("criteria: got %d, want 3", len(got))
	}
	wantLabels := []string{"Tests pass", "Docs updated", "Manual smoke test"}
	for i, c := range got {
		if c.Label != wantLabels[i] || c.State != CriterionPending {
			t.Errorf("criterion[%d] = %+v, want label=%q pending", i, c, wantLabels[i])
		}
	}
}

func TestImport_WithCriteria_RejectsMappingForm(t *testing.T) {
	db := SetupTestDB(t)
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Gate\n" +
		"    criteria:\n" +
		"      - label: Tests pass\n" +
		"        state: passed\n" +
		"```\n"
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RunImport(db, path, "", false, "alice"); err == nil {
		t.Fatal("expected import error: criteria entries must be bare strings")
	}
}

func TestRunAddCriteria_AppendsAndRecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Gate")
	task := MustGet(t, db, id)

	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "A"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "B"}, {Label: "C"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	got, _ := GetCriteria(db, task.ID)
	if len(got) != 3 {
		t.Fatalf("got %d criteria, want 3", len(got))
	}
	if got[0].Label != "A" || got[1].Label != "B" || got[2].Label != "C" {
		t.Errorf("order wrong: %+v", got)
	}

	detail, err := GetLatestEventDetail(db, task.ID, "criteria_added")
	if err != nil || detail == nil {
		t.Fatalf("expected criteria_added event")
	}
	if list, ok := detail["criteria"].([]any); !ok || len(list) != 2 {
		t.Errorf("event should record the most recent batch (2 entries): %+v", detail)
	}
}

func TestRunSetCriterion_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Gate")
	task := MustGet(t, db, id)
	if _, err := RunAddCriteria(db, id, []Criterion{{Label: "Tests pass"}}, TestActor); err != nil {
		t.Fatal(err)
	}

	prior, err := RunSetCriterion(db, id, "Tests pass", CriterionPassed, TestActor)
	if err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	if prior != CriterionPending {
		t.Errorf("prior = %q, want pending", prior)
	}

	detail, _ := GetLatestEventDetail(db, task.ID, "criterion_state")
	if detail == nil {
		t.Fatal("expected criterion_state event")
	}
	if detail["label"] != "Tests pass" || detail["state"] != "passed" || detail["prior"] != "pending" {
		t.Errorf("event detail wrong: %+v", detail)
	}
}

func TestCountPendingCriteria(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Gate")
	pt := MustGet(t, db, parent)

	tx, _ := db.Begin()
	if _, err := insertCriteria(tx, pt.ID, []Criterion{
		{Label: "A"},
		{Label: "B", State: CriterionPassed},
		{Label: "C"},
	}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	n, err := CountPendingCriteria(db, pt.ID)
	if err != nil {
		t.Fatalf("CountPendingCriteria: %v", err)
	}
	if n != 2 {
		t.Errorf("pending = %d, want 2", n)
	}
}
