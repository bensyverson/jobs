package job

import "testing"

// `claim --next -m "…"` must record the note on whichever leaf it lands on,
// exactly as `claim <id> -m` does. The note is resolved before the pick, so
// the domain entry point has to carry it through to RunClaim rather than
// dropping it on the floor.

func TestRunClaimNextFiltered_WithNote_RecordsNotedEvent(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	leaf := MustAdd(t, db, plan, "Leaf")

	task, err := RunClaimNextFiltered(db, "", "", "starting context", TestActor, false, false, false)
	if err != nil {
		t.Fatalf("RunClaimNextFiltered: %v", err)
	}
	if task.ShortID != leaf {
		t.Fatalf("claimed %s, want %s", task.ShortID, leaf)
	}

	detail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if detail == nil {
		t.Fatal("expected a noted event on the leaf claimed by --next")
	}
	if detail["text"] != "starting context" {
		t.Errorf("noted text: got %v, want %q", detail["text"], "starting context")
	}
}

// Empty note stays clutter-free, matching plain `claim`.
func TestRunClaimNextFiltered_EmptyNote_NoNotedEvent(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	MustAdd(t, db, plan, "Leaf")

	task, err := RunClaimNextFiltered(db, "", "", "", TestActor, false, false, false)
	if err != nil {
		t.Fatalf("RunClaimNextFiltered: %v", err)
	}
	detail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if detail != nil {
		t.Errorf("plain claim --next should not emit a noted event; got %v", detail)
	}
}

// The note lands on the leaf the frontier picked, not on the scope argument.
func TestRunClaimNextFiltered_WithNote_ScopedToSubtree(t *testing.T) {
	db := SetupTestDB(t)
	planA := MustAdd(t, db, "", "Plan A")
	MustAdd(t, db, planA, "Leaf A")
	planB := MustAdd(t, db, "", "Plan B")
	leafB := MustAdd(t, db, planB, "Leaf B")

	task, err := RunClaimNextFiltered(db, planB, "", "scoped note", TestActor, false, false, false)
	if err != nil {
		t.Fatalf("RunClaimNextFiltered: %v", err)
	}
	if task.ShortID != leafB {
		t.Fatalf("claimed %s, want %s", task.ShortID, leafB)
	}
	detail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if detail == nil || detail["text"] != "scoped note" {
		t.Errorf("noted detail on %s: got %v, want %q", task.ShortID, detail, "scoped note")
	}
	scope := MustGet(t, db, planB)
	scopeDetail, err := GetLatestEventDetail(db, scope.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail on scope: %v", err)
	}
	if scopeDetail != nil {
		t.Errorf("note should not land on the scope argument; got %v", scopeDetail)
	}
}
