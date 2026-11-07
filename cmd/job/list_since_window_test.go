package main

import (
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// --since <duration> means "closed within": it applies to closed rows the
// tree renders inline under an open parent, not only to the footer.
func TestListSinceDuration_DropsInlineClosedRowsOutsideWindow(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	now := time.Now().Unix()
	prev := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = prev }()
	at := func(ago int64) { job.CurrentNowFunc = func() time.Time { return time.Unix(now-ago, 0) } }

	at(40 * 86400)
	parent := job.MustAdd(t, db, "", "Parent")
	job.MustAdd(t, db, parent, "open sibling")
	old := job.MustAdd(t, db, parent, "old child")
	job.MustDone(t, db, old)
	at(3600)
	fresh := job.MustAdd(t, db, parent, "fresh child")
	job.MustDone(t, db, fresh)
	at(0)
	db.Close()

	for _, args := range [][]string{
		{"ls", "--all", "--since", "7d"},
		{"ls", "--status", "done", "--since", "7d"},
	} {
		stdout, _, err := runCLI(t, dbFile, append([]string{"--as", "alice"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(stdout, "`"+fresh+"` fresh child") {
			t.Errorf("%v: expected the child closed 1h ago, got:\n%s", args, stdout)
		}
		if strings.Contains(stdout, "old child") {
			t.Errorf("%v: expected the child closed 40d ago dropped, got:\n%s", args, stdout)
		}
		if !strings.Contains(stdout, "Parent") {
			t.Errorf("%v: expected the open parent kept, got:\n%s", args, stdout)
		}
	}
}

// A duration bounds the list itself, so it lifts the default footer cap.
func TestListSinceDuration_LiftsDefaultCap(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	now := time.Now().Unix()
	prev := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = prev }()
	n := job.DefaultClosedTailCap + 5
	for i := range n {
		ts := now - int64(n-i)
		job.CurrentNowFunc = func() time.Time { return time.Unix(ts, 0) }
		job.MustDone(t, db, job.MustAdd(t, db, "", "closed task"))
	}
	job.CurrentNowFunc = func() time.Time { return time.Unix(now, 0) }
	job.MustAdd(t, db, "", "still open")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--all", "--since", "1h")
	if err != nil {
		t.Fatalf("ls --all --since 1h: %v", err)
	}
	if got := strings.Count(stdout, "closed task"); got != n {
		t.Errorf("expected all %d closures in the window, got %d:\n%s", n, got, stdout)
	}
	if strings.Contains(stdout, "recent closures shown") {
		t.Errorf("expected no truncation hint, got:\n%s", stdout)
	}
}

func TestListSinceDuration_AppliesToJSON(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	now := time.Now().Unix()
	prev := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = prev }()
	job.CurrentNowFunc = func() time.Time { return time.Unix(now-40*86400, 0) }
	old := job.MustAdd(t, db, "", "old")
	job.MustDone(t, db, old)
	job.CurrentNowFunc = func() time.Time { return time.Unix(now, 0) }
	fresh := job.MustAdd(t, db, "", "fresh")
	job.MustDone(t, db, fresh)
	job.MustAdd(t, db, "", "still open")
	db.Close()

	ids := closedTailIDs(t, dbFile, "ls", "--all", "--format", "json", "--since", "7d")
	if ids[old] || !ids[fresh] {
		t.Errorf("expected only %s in the 7d window, got %v", fresh, ids)
	}
}

// An RFC3339 timestamp is a fixed window start, as on `job log --since`.
func TestListSinceTimestamp_FiltersTreeFooterAndJSON(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	now := time.Now().Unix()
	prev := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = prev }()
	at := func(ago int64) { job.CurrentNowFunc = func() time.Time { return time.Unix(now-ago, 0) } }

	at(40 * 86400)
	parent := job.MustAdd(t, db, "", "Parent")
	job.MustAdd(t, db, parent, "open sibling")
	oldInline := job.MustAdd(t, db, parent, "old inline")
	job.MustDone(t, db, oldInline)
	oldRoot := job.MustAdd(t, db, "", "old root")
	job.MustDone(t, db, oldRoot)
	at(3600)
	freshInline := job.MustAdd(t, db, parent, "fresh inline")
	job.MustDone(t, db, freshInline)
	freshRoot := job.MustAdd(t, db, "", "fresh root")
	job.MustDone(t, db, freshRoot)
	at(0)
	db.Close()

	cutoff := time.Unix(now-7*86400, 0).UTC().Format(time.RFC3339)
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--status", "done", "--since", cutoff)
	if err != nil {
		t.Fatalf("ls --since %s: %v", cutoff, err)
	}
	for _, want := range []string{"fresh inline", "fresh root"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("expected %q, got:\n%s", want, stdout)
		}
	}
	for _, gone := range []string{"old inline", "old root"} {
		if strings.Contains(stdout, gone) {
			t.Errorf("expected %q outside the window, got:\n%s", gone, stdout)
		}
	}

	ids := closedTailIDs(t, dbFile, "ls", "--all", "--format", "json", "--since", cutoff)
	if ids[oldRoot] || !ids[freshRoot] {
		t.Errorf("JSON: expected only %s in the window, got %v", freshRoot, ids)
	}
}

func TestListSince_BadValueNamesEveryForm(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--all", "--since", "yesterday")
	if err == nil {
		t.Fatal("expected an error for --since yesterday")
	}
	for _, form := range []string{"duration", "RFC3339", "count"} {
		if !strings.Contains(err.Error(), form) {
			t.Errorf("expected the error to name %q, got: %v", form, err)
		}
	}
}
