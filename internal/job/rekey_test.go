package job

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Two replicas minting the same short id while apart is rare and unfixable
// after the fact, so it is detected rather than merged: the rebuild fails
// naming both sides, and `job rekey` is the way out
// (project/2026-09-01-git-native-event-log.md, "Short ids under independent
// minting").

// forgeReplicaFile writes one created event for shortID into a second
// replica's log file inside dir's store, standing in for another machine that
// minted the same id while apart.
func forgeReplicaFile(t *testing.T, dir, shortID, title string) string {
	t.Helper()
	cache := filepath.Join(dir, ".jobs.db")
	rep, err := eventlog.NewReplicaID()
	if err != nil {
		t.Fatalf("mint replica: %v", err)
	}
	ap, err := eventlog.OpenAppender(eventlog.StoreDir(cache), cache, rep)
	if err != nil {
		t.Fatalf("open appender: %v", err)
	}
	defer ap.Close()
	payload, err := json.Marshal(CreatedPayload{ShortID: shortID, Title: title, SortKey: "zzzzzz"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	e := eventlog.Envelope{
		TS:    CurrentNowFunc().UnixMilli() + 60_000,
		Actor: "sam",
		Type:  eventlog.Type(EventCreated),
		Task:  shortID,
		Data:  payload,
	}
	if err := ap.Append([]*eventlog.Envelope{&e}); err != nil {
		t.Fatalf("append: %v", err)
	}
	return rep
}

func TestTwoCreatedEventsForOneShortIDFailTheRebuild(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	task, err := RunAdd(db, "", "the original", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	mine := repOf(t, dir)
	db.Close()

	theirs := forgeReplicaFile(t, dir, task.ShortID, "the impostor")

	_, err = OpenDB(filepath.Join(dir, ".jobs.db"))
	if err == nil {
		t.Fatalf("the rebuild accepted two created events for %s", task.ShortID)
	}
	msg := err.Error()
	for _, want := range []string{mine, theirs, "the original", "the impostor", task.ShortID, "job rekey"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the collision error does not name %q:\n%s", want, msg)
		}
	}
}

func TestRekeyThenRebuildDumpsIdenticallyOnBothReplicas(t *testing.T) {
	dir := t.TempDir()
	db := storeAt(t, dir)
	task, err := RunAdd(db, "", "the original", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	db.Close()

	theirs := forgeReplicaFile(t, dir, task.ShortID, "the impostor")

	// The cache refused to build, so rekey opens it without a sync and works
	// from the raw log.
	recovery, err := OpenDBForRecovery(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("open for recovery: %v", err)
	}
	res, err := RunRekey(recovery, theirs+":"+task.ShortID, "ben")
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if res.NewID == task.ShortID || len(res.NewID) != shortIDLen {
		t.Fatalf("rekey minted %q", res.NewID)
	}
	recovery.Close()

	db, err = OpenDB(filepath.Join(dir, ".jobs.db"))
	if err != nil {
		t.Fatalf("open after rekey: %v", err)
	}
	defer db.Close()

	var original, moved string
	if err := db.QueryRow("SELECT title FROM tasks WHERE short_id = ?", task.ShortID).Scan(&original); err != nil {
		t.Fatalf("original task missing: %v", err)
	}
	if original != "the original" {
		t.Fatalf("the earlier replica did not keep the id: %q", original)
	}
	if err := db.QueryRow("SELECT title FROM tasks WHERE short_id = ?", res.NewID).Scan(&moved); err != nil {
		t.Fatalf("rekeyed task missing: %v", err)
	}
	if moved != "the impostor" {
		t.Fatalf("rekeyed title = %q", moved)
	}

	// A second machine that pulls the same three files converges without a
	// second decision.
	other := t.TempDir()
	carryLog(t, dir, other)
	otherDB, err := OpenDB(filepath.Join(other, ".jobs.db"))
	if err != nil {
		t.Fatalf("open the other replica: %v", err)
	}
	defer otherDB.Close()
	if a, b := applyDump(t, db), applyDump(t, otherDB); a != b {
		t.Fatalf("the two replicas' caches differ after rekey")
	}
}
