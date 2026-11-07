package main

import (
	"encoding/json"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

func twoTasks(t *testing.T) (dbFile, a, b string) {
	t.Helper()
	dbFile = setupCLI(t)
	db := openTestDB(t, dbFile)
	a = job.MustAdd(t, db, "", "alpha")
	b = job.MustAdd(t, db, "", "beta")
	db.Close()
	return dbFile, a, b
}

func labelsOf(t *testing.T, dbFile, id string) []string {
	t.Helper()
	db := openTestDB(t, dbFile)
	defer db.Close()
	task, err := job.GetTaskByShortID(db, id)
	if err != nil || task == nil {
		t.Fatalf("lookup %s: %v", id, err)
	}
	names, err := job.GetLabels(db, task.ID)
	if err != nil {
		t.Fatalf("labels of %s: %v", id, err)
	}
	return names
}

func TestLabelAddWith_LabelsEveryTask(t *testing.T) {
	dbFile, a, b := twoTasks(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", a, b, "with", "reviewed", "audio")
	if err != nil {
		t.Fatalf("label add ... with: %v", err)
	}
	for _, id := range []string{a, b} {
		if got := strings.Join(labelsOf(t, dbFile, id), ","); got != "audio,reviewed" {
			t.Errorf("labels of %s = %q, want audio,reviewed", id, got)
		}
		if !strings.Contains(stdout, "Labeled: "+id) {
			t.Errorf("expected an ack line for %s, got:\n%s", id, stdout)
		}
	}
}

func TestLabelRemoveWith_UnlabelsEveryTask(t *testing.T) {
	dbFile, a, b := twoTasks(t)
	mustLabel(t, dbFile, a, "reviewed", "keep")
	mustLabel(t, dbFile, b, "reviewed")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "remove", a, b, "with", "reviewed")
	if err != nil {
		t.Fatalf("label remove ... with: %v", err)
	}
	if got := strings.Join(labelsOf(t, dbFile, a), ","); got != "keep" {
		t.Errorf("labels of %s = %q, want keep", a, got)
	}
	if got := labelsOf(t, dbFile, b); len(got) != 0 {
		t.Errorf("labels of %s = %v, want none", b, got)
	}
	if !strings.Contains(stdout, "Unlabeled: "+a) || !strings.Contains(stdout, "Unlabeled: "+b) {
		t.Errorf("expected an ack line per task, got:\n%s", stdout)
	}
}

func TestLabelAddWith_UnknownIDRecordsNothing(t *testing.T) {
	dbFile, a, _ := twoTasks(t)

	_, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", a, "zzzzzz", "with", "reviewed")
	if err == nil {
		t.Fatal("expected an error for an unknown id")
	}
	if !strings.Contains(err.Error(), "zzzzzz") {
		t.Errorf("expected the error to name the unknown id, got: %v", err)
	}
	if got := labelsOf(t, dbFile, a); len(got) != 0 {
		t.Errorf("expected no label recorded on %s, got %v", a, got)
	}
}

func TestLabelWith_MissingSideIsUsageError(t *testing.T) {
	dbFile, a, _ := twoTasks(t)
	for _, args := range [][]string{
		{"label", "add", a, "with"},
		{"label", "add", "with", "reviewed"},
		{"label", "remove", a, "with"},
	} {
		_, _, err := runCLI(t, dbFile, append([]string{"--as", "alice"}, args...)...)
		if err == nil {
			t.Errorf("%v: expected a usage error", args)
			continue
		}
		if !strings.Contains(err.Error(), "usage: job label "+args[1]+" <id> [<id>...] with <name>") {
			t.Errorf("%v: expected the usage line as the remedy, got: %v", args, err)
		}
	}
}

func TestLabelAddWith_JSONIsOneObjectPerTask(t *testing.T) {
	dbFile, a, b := twoTasks(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", a, b, "with", "reviewed", "--format", "json")
	if err != nil {
		t.Fatalf("label add ... with --format json: %v", err)
	}
	var rows []struct {
		ID    string   `json:"id"`
		Added []string `json:"added"`
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if len(rows) != 2 || rows[0].ID != a || rows[1].ID != b {
		t.Fatalf("expected rows for %s and %s in order, got %+v", a, b, rows)
	}
	for _, r := range rows {
		if strings.Join(r.Added, ",") != "reviewed" {
			t.Errorf("row %s added = %v, want [reviewed]", r.ID, r.Added)
		}
	}
}
