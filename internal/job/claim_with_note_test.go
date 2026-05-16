package job

import (
	"testing"
)

// `claim -m` parity with `release -m` and `done -m`: when the caller passes
// a non-empty note, a `noted` event is recorded in the same transaction as
// the `claimed` event and lands first in the timeline so the agent's
// starting context anchors the work rather than trailing it.

func TestRunClaim_WithNote_RecordsBothEventsAtomically(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "", "starting context", TestActor, false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	notedDetail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if notedDetail == nil {
		t.Fatal("expected noted event alongside claimed event")
	}
	if notedDetail["text"] != "starting context" {
		t.Errorf("noted text: got %v, want %q", notedDetail["text"], "starting context")
	}
	claimedDetail, err := GetLatestEventDetail(db, task.ID, "claimed")
	if err != nil {
		t.Fatalf("GetLatestEventDetail claimed: %v", err)
	}
	if claimedDetail == nil {
		t.Fatal("expected claimed event alongside noted event")
	}
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want %q", task.Status, "claimed")
	}
}

// Timeline ordering: noted must precede claimed so the agent's starting
// context anchors the lifecycle rather than trails it. This is the load-
// bearing distinction from `release -m`, where the note also lands inside
// the same tx but the verb itself naturally ends a window of work.
func TestRunClaim_WithNote_NotedPrecedesClaimedInTimeline(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "", "what I'm about to try", TestActor, false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	events, err := GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var notedIdx, claimedIdx int = -1, -1
	for i, e := range events {
		switch e.EventType {
		case "noted":
			notedIdx = i
		case "claimed":
			claimedIdx = i
		}
	}
	if notedIdx == -1 {
		t.Fatal("expected a noted event")
	}
	if claimedIdx == -1 {
		t.Fatal("expected a claimed event")
	}
	if notedIdx > claimedIdx {
		t.Errorf("noted should precede claimed in timeline; got notedIdx=%d, claimedIdx=%d", notedIdx, claimedIdx)
	}
}

// Empty note is the dominant call shape — must not emit a `noted` event,
// otherwise every plain `claim` would clutter the timeline with an empty
// note row.
func TestRunClaim_EmptyNote_NoNotedEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	notedDetail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if notedDetail != nil {
		t.Errorf("empty note should not emit a noted event; got %v", notedDetail)
	}
}

// When the claim itself fails (e.g. task already claimed by another actor
// without --force), the noted event must not land either — atomicity is
// the contract, same as release.
func TestRunClaim_WithNote_FailedClaimRollsBackNote(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if err := RunClaim(db, id, "", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}

	// bob tries to claim without --force; should fail with no noted event
	// landing on the task.
	err := RunClaim(db, id, "", "bob's plan", "bob", false)
	if err == nil {
		t.Fatal("expected claim to be rejected (already claimed by alice)")
	}

	task := MustGet(t, db, id)
	events, err := GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	for _, e := range events {
		if e.EventType == "noted" {
			t.Errorf("noted event should have rolled back with failed claim; got %+v", e)
		}
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != "alice" {
		t.Errorf("alice's claim should still hold; ClaimedBy=%v", task.ClaimedBy)
	}
}
