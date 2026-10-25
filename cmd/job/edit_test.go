package main

import (
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
)

func TestEdit_Title_Only(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Old", "kept")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--title", "New"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Title != "New" || task.Description != "kept" {
		t.Errorf("task: title=%q desc=%q", task.Title, task.Description)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "edited")
	if detail["new_title"] != "New" {
		t.Errorf("event new_title: %v", detail["new_title"])
	}
	if _, ok := detail["new_desc"]; ok {
		t.Errorf("event should omit desc fields when untouched: %v", detail)
	}
}

func TestEdit_Desc_Only(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Title")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--desc", "body"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Title != "Title" || task.Description != "body" {
		t.Errorf("task: title=%q desc=%q", task.Title, task.Description)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "edited")
	if _, ok := detail["new_title"]; ok {
		t.Errorf("event should omit title fields when untouched: %v", detail)
	}
}

func TestEdit_Both(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Old title", "old body")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--title", "Nt", "--desc", "Nd"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Title != "Nt" || task.Description != "Nd" {
		t.Errorf("task: %+v", task)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "edited")
	for _, k := range []string{"old_title", "new_title", "old_desc", "new_desc"} {
		if _, ok := detail[k]; !ok {
			t.Errorf("missing detail key %q in %v", k, detail)
		}
	}
}

func TestEdit_Neither_Error(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--title") || !strings.Contains(err.Error(), "--desc") {
		t.Errorf("err: %v", err)
	}
}

func TestEdit_ClearDesc(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "T", "old")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--desc", ""); err != nil {
		t.Fatalf("edit: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Description != "" {
		t.Errorf("desc should be cleared: %q", task.Description)
	}
}

func TestEdit_Positional_Gone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "New title")
	if err == nil {
		t.Fatal("expected error: positional title no longer accepted")
	}
}

func TestEdit_RewordCriterion_ReplacesTextKeepsState(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "T")
	crits, err := job.RunAddCriteria(db, id, []job.Criterion{{Label: "old one"}, {Label: "old two"}}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunSetCriterion(db, id, crits[0].ShortID, job.CriterionPassed, "alice"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// The short label addresses one; the verbatim label the other. The new
	// text may itself contain "=", so only the first one splits.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id,
		"--reword-criterion", crits[0].ShortID+"=x = y holds",
		"--reword-criterion", "old two=second, reworded"); err != nil {
		t.Fatalf("edit: %v", err)
	}

	db = openTestDB(t, dbFile)
	got, err := job.GetCriteria(db, job.MustGet(t, db, id).ID)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Label != "x = y holds" || got[0].State != job.CriterionPassed {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Label != "second, reworded" || got[1].State != job.CriterionPending {
		t.Errorf("second = %+v", got[1])
	}

	out, _, err := runCLI(t, dbFile, "log", id)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if !strings.Contains(out, `criterion reworded: "old one" → "x = y holds"`) {
		t.Errorf("log does not show old and new wording:\n%s", out)
	}
}

func TestEdit_RewordCriterion_UnknownLabelFails(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "T")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{{Label: "real"}}, "alice"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--reword-criterion", "ghost=new words")
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	if !strings.Contains(err.Error(), `"ghost"`) || !strings.Contains(err.Error(), "job show "+id) {
		t.Errorf("err = %v", err)
	}
}

func TestEdit_RewordCriterion_MalformedValueFails(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "T")
	db.Close()

	for _, raw := range []string{"no-equals", "=text", "label="} {
		_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--reword-criterion", raw)
		if err == nil || !strings.Contains(err.Error(), "label=new text") {
			t.Errorf("%q: err = %v, want the expected form named", raw, err)
		}
	}
}

func TestEdit_RewordCriterion_ListedInTheNoFlagError(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "T")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id)
	if err == nil || !strings.Contains(err.Error(), "--reword-criterion") {
		t.Errorf("the no-flag error should list --reword-criterion: %v", err)
	}
}
