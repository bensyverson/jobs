package job

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Test helpers exported so both this package's own tests and higher-level
// CLI tests (in cmd/job) can share them. Non-test filename so Go exports
// them across package boundaries.

const TestActor = "TestActor"

func SetupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func MustAdd(t *testing.T, db *sql.DB, parentShortID, title string) string {
	t.Helper()
	res, err := RunAdd(db, parentShortID, title, "", "", nil, TestActor)
	if err != nil {
		t.Fatalf("add task %q: %v", title, err)
	}
	return res.ShortID
}

func MustAddDesc(t *testing.T, db *sql.DB, parentShortID, title, desc string) string {
	t.Helper()
	res, err := RunAdd(db, parentShortID, title, desc, "", nil, TestActor)
	if err != nil {
		t.Fatalf("add task %q: %v", title, err)
	}
	return res.ShortID
}

func MustDone(t *testing.T, db *sql.DB, shortID string) {
	t.Helper()
	if _, _, err := RunDone(db, []string{shortID}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done task %s: %v", shortID, err)
	}
}

func MustGet(t *testing.T, db *sql.DB, shortID string) *Task {
	t.Helper()
	task, err := GetTaskByShortID(db, shortID)
	if err != nil {
		t.Fatalf("get task %s: %v", shortID, err)
	}
	if task == nil {
		t.Fatalf("task %s not found", shortID)
	}
	return task
}

func MustClaim(t *testing.T, db *sql.DB, shortID, duration string) {
	t.Helper()
	if err := RunClaim(db, shortID, duration, "", TestActor, false); err != nil {
		t.Fatalf("claim task %s: %v", shortID, err)
	}
}
