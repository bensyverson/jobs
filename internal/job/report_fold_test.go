package job

import (
	"encoding/json"
	"testing"
)

func foldEvent(t *testing.T, ts int64, typ EventType, task string, payload any) reportEvent {
	t.Helper()
	var data []byte
	if payload != nil {
		var err error
		if data, err = json.Marshal(payload); err != nil {
			t.Fatal(err)
		}
	}
	return reportEvent{ts: ts, actor: TestActor, typ: typ, task: task, data: data}
}

// An adopted store carries its state in a snapshot, after history-only lines
// the fold replays as best it can. The snapshot overwrites what it carries:
// tasks it adds arrive with their recorded creation time, and statuses and
// blocks become what it says. A task it omits that the store still holds
// keeps its place and its history — see applySnapshot for why.
func TestReportFold_SnapshotOverwrites(t *testing.T) {
	existing := map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true}
	fold := newReportFold(existing)
	events := []reportEvent{
		foldEvent(t, 1000, EventCreated, "A", CreatedPayload{Title: "A"}),
		foldEvent(t, 2000, EventCreated, "B", CreatedPayload{Title: "B", ParentID: "A"}),
		foldEvent(t, 3000, EventCreated, "C", CreatedPayload{Title: "C", ParentID: "A"}),
		foldEvent(t, 9000, EventSnapshot, "", SnapshotPayload{
			Tasks: []SnapshotTask{
				{ShortID: "A", Status: "available", Kind: "task", CreatedAt: 1},
				{ShortID: "B", ParentID: "A", Status: "done", Kind: "task", CreatedAt: 2},
				{ShortID: "D", ParentID: "A", Status: "claimed", Kind: "task", CreatedAt: 5},
				{ShortID: "E", ParentID: "A", Status: "available", Kind: "task", CreatedAt: 6},
				{ShortID: "Z", ParentID: "A", Status: "available", Kind: "task", CreatedAt: 7},
			},
			Blocks: []SnapshotBlock{{BlockerID: "D", BlockedID: "E"}, {BlockerID: "B", BlockedID: "D"}},
		}),
	}
	for _, e := range events {
		if err := fold.apply(e); err != nil {
			t.Fatalf("apply %s: %v", e.typ, err)
		}
	}
	got := fold.sample(forestUniverse())
	// B done; C open, kept though omitted; D open (its blocker B is done); E
	// open and blocked by D. Z is not in the store (purged since).
	want := Sample{Scope: 4, Done: 1, Open: 3, Blocked: 1}
	if got != want {
		t.Errorf("sample = %+v, want %+v", got, want)
	}
	if c := fold.tasks["C"]; c == nil || c.created != 3000 {
		t.Errorf("C = %+v, want kept with its created time", c)
	}
	if d := fold.tasks["D"]; d == nil || d.created != 5000 {
		t.Errorf("D = %+v, want created at the snapshot's recorded 5s", d)
	}
}

// Events on a task the store no longer holds are ignored, so a purged task
// never counts and never makes its parent a parent.
func TestReportFold_IgnoresTasksNotInTheStore(t *testing.T) {
	fold := newReportFold(map[string]bool{"A": true})
	for _, e := range []reportEvent{
		foldEvent(t, 1000, EventCreated, "A", CreatedPayload{Title: "A"}),
		foldEvent(t, 2000, EventCreated, "P", CreatedPayload{Title: "purged", ParentID: "A"}),
	} {
		if err := fold.apply(e); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := fold.sample(forestUniverse()), (Sample{Scope: 1, Open: 1}); got != want {
		t.Errorf("sample = %+v, want %+v — A is a leaf and a plan", got, want)
	}
}
