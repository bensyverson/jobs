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

// SeedBlockEdge writes a blocks row directly, the same INSERT applyBlocked
// performs when replaying a `blocked` event — skipping RunBlockMany's cycle
// check entirely. block add and import both refuse an edge that would close
// a loop in the wait graph, so a healthy store can never reach one through
// the CLI; this is how a test puts one in a store anyway, standing in for a
// database written before that check existed.
func SeedBlockEdge(t *testing.T, db *sql.DB, blockedShortID, blockerShortID string) {
	t.Helper()
	blocked := MustGet(t, db, blockedShortID)
	blocker := MustGet(t, db, blockerShortID)
	if _, err := db.Exec(
		"INSERT OR IGNORE INTO blocks (blocker_id, blocked_id, created_at) VALUES (?, ?, ?)",
		blocker.ID, blocked.ID, CurrentNowFunc().Unix(),
	); err != nil {
		t.Fatalf("seed block edge %s by %s: %v", blockedShortID, blockerShortID, err)
	}
}
