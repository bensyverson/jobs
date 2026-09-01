package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// mergeCLIPair seeds one database, copies it, and diverges the copy — the CLI
// counterpart of the library's divergedPair.
func mergeCLIPair(t *testing.T) (localPath, otherPath, sharedID, otherOnlyID string) {
	t.Helper()
	dir := t.TempDir()
	localPath = filepath.Join(dir, "local.jobs.db")
	otherPath = filepath.Join(dir, "other.jobs.db")

	db, err := job.CreateDB(localPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := job.RunAdd(db, "", "Shared root", "", "", nil, "seeder")
	if err != nil {
		t.Fatal(err)
	}
	sharedID = res.ShortID
	db.Close()

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	otherDB, err := job.OpenDB(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	added, err := job.RunAdd(otherDB, "", "Only over there", "", "", nil, "seeder")
	if err != nil {
		t.Fatal(err)
	}
	otherOnlyID = added.ShortID
	if err := job.RunNote(otherDB, sharedID, "a note from over there", nil, "seeder"); err != nil {
		t.Fatal(err)
	}
	otherDB.Close()

	t.Cleanup(resetFlags)
	return localPath, otherPath, sharedID, otherOnlyID
}

func TestMergeCLI_MergesAndReports(t *testing.T) {
	localPath, otherPath, _, otherOnly := mergeCLIPair(t)

	out, _, err := runCLI(t, localPath, "merge", otherPath)
	if err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	if !strings.Contains(out, "# Merge report") {
		t.Errorf("expected a markdown report:\n%s", out)
	}
	if !strings.Contains(out, otherOnly) {
		t.Errorf("report should name the arriving task %s:\n%s", otherOnly, out)
	}

	db := openTestDB(t, localPath)
	task, err := job.GetTaskByShortID(db, otherOnly)
	if err != nil || task == nil {
		t.Fatalf("task %s did not arrive: %v", otherOnly, err)
	}
}

func TestMergeCLI_DryRunLeavesFilesAlone(t *testing.T) {
	localPath, otherPath, _, otherOnly := mergeCLIPair(t)

	beforeLocal, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeOther, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := runCLI(t, localPath, "merge", otherPath, "--dry-run")
	if err != nil {
		t.Fatalf("merge --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, otherOnly) {
		t.Errorf("dry run should still report what it would do:\n%s", out)
	}

	afterLocal, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	afterOther, err := os.ReadFile(otherPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeLocal) != string(afterLocal) {
		t.Error("--dry-run wrote to the local database file")
	}
	if string(beforeOther) != string(afterOther) {
		t.Error("--dry-run wrote to the other database file")
	}
}

func TestMergeCLI_FormatJSON(t *testing.T) {
	localPath, otherPath, _, otherOnly := mergeCLIPair(t)

	out, _, err := runCLI(t, localPath, "merge", otherPath, "--format", "json", "--dry-run")
	if err != nil {
		t.Fatalf("merge --format=json: %v\n%s", err, out)
	}
	var report job.MergeReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if !report.DryRun || !report.Changed {
		t.Errorf("report = %+v", report)
	}
	if len(report.OnlyInOther) != 1 || report.OnlyInOther[0].ShortID != otherOnly {
		t.Errorf("OnlyInOther = %+v", report.OnlyInOther)
	}
}

func TestMergeCLI_RejectsBadFormat(t *testing.T) {
	localPath, otherPath, _, _ := mergeCLIPair(t)
	_, _, err := runCLI(t, localPath, "merge", otherPath, "--format", "yaml")
	if err == nil || !strings.Contains(err.Error(), "--format") {
		t.Fatalf("expected a --format error, got %v", err)
	}
}

func TestMergeCLI_MissingFile(t *testing.T) {
	localPath, _, _, _ := mergeCLIPair(t)
	_, _, err := runCLI(t, localPath, "merge", filepath.Join(t.TempDir(), "nope.jobs.db"))
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// The verb records no events, so it needs no identity.
func TestMergeCLI_NeedsNoIdentity(t *testing.T) {
	localPath, otherPath, _, _ := mergeCLIPair(t)
	out, _, err := runCLI(t, localPath, "merge", otherPath, "--dry-run")
	if err != nil {
		t.Fatalf("merge without --as: %v\n%s", err, out)
	}
	if strings.Contains(out, "identity required") {
		t.Errorf("merge should not require --as:\n%s", out)
	}
}
