package job

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reconcile: the invariants a single replica keeps, restored after two
// replicas' logs are merged.
//
// Apply never derives — a cascade close is an explicit event, not something
// the applier works out — so a trigger split across two machines leaves the
// invariant broken until reconcile notices and appends the repairing events
// (project/2026-09-01-git-native-event-log.md, "Rebuild, and when it runs").

// openAt opens (not creates) the cache in dir, registering a close.
func openAt(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := OpenDB(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// atSecond runs fn with the package clock pinned to a wall time, so two
// replicas' events order the way the test means them to.
func atSecond(t *testing.T, unix int64, fn func()) {
	t.Helper()
	prev := CurrentNowFunc
	CurrentNowFunc = func() time.Time { return time.Unix(unix, 0) }
	defer func() { CurrentNowFunc = prev }()
	fn()
}

func TestReconcileClosesAParentWhoseLastChildClosedElsewhere(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	dbA := storeAt(t, dirA)

	parent, err := RunAdd(dbA, "", "the parent", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add parent: %v", err)
	}
	one, err := RunAdd(dbA, parent.ShortID, "child one", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add child: %v", err)
	}
	two, err := RunAdd(dbA, parent.ShortID, "child two", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add child: %v", err)
	}
	dbA.Close()

	carryLog(t, dirA, dirB)
	dbB := openAt(t, dirB)
	atSecond(t, time.Now().Unix()+10, func() {
		if _, _, err := RunDone(dbB, []string{one.ShortID}, false, "", nil, "sam", false, ""); err != nil {
			t.Fatalf("close on B: %v", err)
		}
	})
	dbB.Close()

	dbA = openAt(t, dirA)
	atSecond(t, time.Now().Unix()+20, func() {
		if _, _, err := RunDone(dbA, []string{two.ShortID}, false, "", nil, "ben", false, ""); err != nil {
			t.Fatalf("close on A: %v", err)
		}
	})
	dbA.Close()

	// Neither machine saw the other's close, so the parent is still open.
	carryLog(t, dirB, dirA)
	notices := captureNotices(t)
	dbA = openAt(t, dirA)

	var status string
	if err := dbA.QueryRow("SELECT status FROM tasks WHERE short_id = ?", parent.ShortID).Scan(&status); err != nil {
		t.Fatalf("read parent: %v", err)
	}
	if status != "done" {
		t.Fatalf("parent status = %q, want done — reconcile did not close it", status)
	}
	var events int
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'done' AND e.actor = ?`, parent.ShortID, reconcileActor).Scan(&events); err != nil {
		t.Fatalf("count repair events: %v", err)
	}
	if events != 1 {
		t.Fatalf("reconcile logged %d done events for the parent, want 1", events)
	}
	if !strings.Contains(notices.String(), parent.ShortID) {
		t.Fatalf("the repair was not printed: %q", notices.String())
	}

	// Idempotent: a second open repairs nothing more.
	dbA.Close()
	dbA = openAt(t, dirA)
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'done' AND e.actor = ?`, parent.ShortID, reconcileActor).Scan(&events); err != nil {
		t.Fatalf("count repair events: %v", err)
	}
	if events != 1 {
		t.Fatalf("a second open repaired again: %d done events", events)
	}
}

func TestReconcilePurgesAChildOfAPurgedTask(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	dbA := storeAt(t, dirA)
	parent, err := RunAdd(dbA, "", "doomed", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	dbA.Close()

	carryLog(t, dirA, dirB)

	base := time.Now().Unix()
	dbA = openAt(t, dirA)
	atSecond(t, base+10, func() {
		if _, _, _, err := RunCancel(dbA, []string{parent.ShortID}, "wrong tree", false, true, true, "ben"); err != nil {
			t.Fatalf("purge on A: %v", err)
		}
	})
	dbA.Close()

	dbB := openAt(t, dirB)
	var child *AddResult
	atSecond(t, base+20, func() {
		child, err = RunAdd(dbB, parent.ShortID, "added elsewhere", "", "", nil, "sam")
		if err != nil {
			t.Fatalf("add child on B: %v", err)
		}
	})
	dbB.Close()

	carryLog(t, dirB, dirA)
	notices := captureNotices(t)
	dbA = openAt(t, dirA)

	var n int
	if err := dbA.QueryRow("SELECT COUNT(*) FROM tasks WHERE short_id = ?", child.ShortID).Scan(&n); err != nil {
		t.Fatalf("count child: %v", err)
	}
	if n != 0 {
		t.Fatalf("the child of a purged task survived reconcile")
	}
	if !strings.Contains(notices.String(), child.ShortID) {
		t.Fatalf("the repair was not printed: %q", notices.String())
	}
}

func TestReconcileReleasesTheLaterOfTwoLiveClaims(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	dbA := storeAt(t, dirA)
	task, err := RunAdd(dbA, "", "contended", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	dbA.Close()
	carryLog(t, dirA, dirB)

	base := time.Now().Unix()
	dbA = openAt(t, dirA)
	atSecond(t, base+10, func() {
		if err := RunClaim(dbA, task.ShortID, "4h", "", "ben", false); err != nil {
			t.Fatalf("claim on A: %v", err)
		}
	})
	dbA.Close()

	dbB := openAt(t, dirB)
	atSecond(t, base+20, func() {
		if err := RunClaim(dbB, task.ShortID, "4h", "", "sam", false); err != nil {
			t.Fatalf("claim on B: %v", err)
		}
	})
	dbB.Close()

	carryLog(t, dirB, dirA)
	notices := captureNotices(t)
	dbA = openAt(t, dirA)

	var holder sql.NullString
	if err := dbA.QueryRow("SELECT claimed_by FROM tasks WHERE short_id = ?", task.ShortID).Scan(&holder); err != nil {
		t.Fatalf("read holder: %v", err)
	}
	if holder.String != "ben" {
		t.Fatalf("holder = %q, want ben (the earlier claim wins)", holder.String)
	}
	var releases int
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'released' AND e.detail LIKE '%lost-merge%'`,
		task.ShortID).Scan(&releases); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releases != 1 {
		t.Fatalf("lost-merge releases = %d, want 1", releases)
	}
	if !strings.Contains(notices.String(), "sam") {
		t.Fatalf("the losing claimant was not named: %q", notices.String())
	}

	// Idempotent across a second merge-and-open.
	dbA.Close()
	dbA = openAt(t, dirA)
	if err := dbA.QueryRow(`SELECT COUNT(*) FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'released' AND e.detail LIKE '%lost-merge%'`,
		task.ShortID).Scan(&releases); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if releases != 1 {
		t.Fatalf("a second open repaired the same claim again: %d releases", releases)
	}
}
