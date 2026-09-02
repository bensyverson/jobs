package job

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

// Retrying a refused adoption.
//
// The diff file is the memory of the refusal: while it exists the next open
// says so in one line and leaves the cache alone, and deleting it is how a
// person asks for another attempt. Nothing about a retry lives in local.json,
// where a hand edit once corrupted the file (Hirewell, 2026-09-02).

// refusedAdoption builds a legacy cache whose first open refuses, because the
// snapshot seam drops a task, and returns the path and the notices printed.
func refusedAdoption(t *testing.T) (path string, notices string) {
	t.Helper()
	newMergeClock(t)
	dir := t.TempDir()
	path = legacyCache(t, dir, func(db *sql.DB) {
		a := MustAdd(t, db, "", "Alpha root")
		MustAdd(t, db, a, "Beta child")
	})

	orig := adoptMutateSnapshot
	adoptMutateSnapshot = func(p *SnapshotPayload) {
		if len(p.Tasks) > 0 {
			p.Tasks = p.Tasks[:len(p.Tasks)-1]
		}
	}
	t.Cleanup(func() { adoptMutateSnapshot = orig })

	buf := captureNotices(t)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("a refused adoption is not an error on open: %v", err)
	}
	db.Close()
	adoptMutateSnapshot = orig
	return path, buf.String()
}

func TestAdopt_ARefusalNamesTheDiffAndHowToRetry(t *testing.T) {
	path, notices := refusedAdoption(t)

	diff := path + adoptDiffSuffix
	if _, err := os.Stat(diff); err != nil {
		t.Fatalf("the refusal should leave its diff at %s: %v", diff, err)
	}
	if !strings.Contains(notices, diff) || !strings.Contains(strings.ToLower(notices), "delete") {
		t.Errorf("the refusal should name the diff file and say deleting it retries:\n%s", notices)
	}
}

// While the diff file stands, the next open does not rebuild and compare the
// whole database again — it says where the diff is and moves on.
func TestAdopt_ARefusalIsRememberedByTheDiffFile(t *testing.T) {
	path, _ := refusedAdoption(t)

	buf := captureNotices(t)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	if _, err := os.Stat(path + adoptBackupSuffix); !os.IsNotExist(err) {
		t.Error("the second open retried adoption while the diff file still stood")
	}
	if n := countLegacyRows(t, mustOpenPristine(t, path)); n == 0 {
		t.Error("the cache should still be the legacy one")
	}
	if !strings.Contains(buf.String(), path+adoptDiffSuffix) || !strings.Contains(strings.ToLower(buf.String()), "delete") {
		t.Errorf("the second open should say where the diff is and how to retry:\n%s", buf.String())
	}
}

// Deleting the diff file is the retry. With the cause gone, the next open
// adopts the cache.
func TestAdopt_DeletingTheDiffRetries(t *testing.T) {
	path, _ := refusedAdoption(t)
	quietNotices(t)

	if err := os.Remove(path + adoptDiffSuffix); err != nil {
		t.Fatal(err)
	}
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := os.Stat(path + adoptBackupSuffix); err != nil {
		t.Errorf("the retry should have adopted the cache and kept the backup: %v", err)
	}
	if n := countLegacyRows(t, db); n != 0 {
		t.Errorf("the retry left %d legacy rows", n)
	}
	if _, err := LoadLocalState(path); err != nil {
		t.Errorf("local state should still parse after a refusal and a retry: %v", err)
	}
}

// mustOpenPristine reads a cache without adopting it.
func mustOpenPristine(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := OpenDBForRecovery(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
