package job

import (
	"encoding/json"
	"strings"
	"testing"
)

// eventPayloadSamples pairs every EventType constant with one populated
// value of its payload struct. TestEventPayloadsMarshalWithShortIDs walks
// this table so a new event type with no sample here, or a payload field
// with no matching EventType constant, fails loudly.
var eventPayloadSamples = []struct {
	Type    EventType
	Payload any
}{
	{EventCreated, CreatedPayload{
		ShortID: "Qr7Tm9", ParentID: "AbC12", Title: "Root task", Description: "desc",
		SortKey: "V00001", Kind: "issue",
	}},
	{EventLabeled, LabeledPayload{Names: []string{"web", "bug"}, Existing: []string{}}},
	{EventUnlabeled, UnlabeledPayload{Names: []string{"web"}, Absent: []string{"bug"}}},
	{EventReleased, ReleasedPayload{
		AutoReleased: true, TriggeredByChild: "AbC12", WasClaimedBy: "alice", WasExpiresAt: 1234,
	}},
	{EventCanceled, CanceledPayload{
		Reason: "obsolete", Cascade: new(true), CascadeClosed: []string{"AbC12"},
		CascadeClosedByParent: "XyZ99", WasStatus: "claimed", WasClaimedBy: "alice",
		WasExpiresAt: 1234, AutoClosed: true, TriggerKind: "cancel", TriggeredBy: "AbC12",
		CascadeStatus: "canceled",
	}},
	{EventBlocked, BlockedPayload{BlockedID: "AbC12", BlockerID: "XyZ99"}},
	{EventUnblocked, UnblockedPayload{BlockedID: "AbC12", BlockerID: "XyZ99", Reason: "blocker_done"}},
	{EventPurged, PurgedPayload{
		Reason: "cleanup", PurgedID: "AbC12", PurgedTitle: "Old task", Cascade: true,
		CascadePurged: []string{"XyZ99"},
	}},
	{EventClaimExpired, ClaimExpiredPayload{WasClaimedBy: "alice", WasExpiresAt: 1234}},
	{EventNoted, NotedPayload{Text: "a note", Result: map[string]any{"ok": true}}},
	{EventClaimed, ClaimedPayload{
		Duration: "30m", ExpiresAt: 1234, WasClaimedBy: "bob", WasExpiresAt: 5678,
	}},
	{EventCriteriaAdded, CriteriaAddedPayload{Criteria: []CriterionEntry{
		{Label: "tests pass", State: "pending", ShortID: "aB3", SortKey: "V0001"},
	}}},
	{EventCriterionState, CriterionStatePayload{
		Label: "tests pass", State: "passed", Prior: "pending", ShortID: "aB3",
	}},
	{EventHeartbeat, HeartbeatPayload{NewExpiresAt: 1234}},
	{EventFoundInSet, FoundInSetPayload{
		TaskID: "AbC12", SourceID: "XyZ99", PreviousSourceID: "zzTop",
	}},
	{EventFoundInCleared, FoundInClearedPayload{TaskID: "AbC12", SourceID: "XyZ99"}},
	{EventKindChanged, KindChangedPayload{From: "task", To: "issue"}},
	{EventDone, DonePayload{
		Note: "shipped", Result: map[string]any{"n": 1}, Cascade: new(false),
		CascadeClosed: []string{"AbC12"}, CascadeClosedByParent: "XyZ99",
		CriteriaWaived: []string{"aB3"}, CriteriaBulkState: "passed", WasStatus: "claimed",
		WasClaimedBy: "alice", WasExpiresAt: 1234, AutoClosed: true, TriggerKind: "done",
		TriggeredBy: "AbC12", CascadeStatus: "done",
	}},
	{EventReopened, ReopenedPayload{
		Cascade: true, ReopenedChildren: []string{"AbC12"}, FromStatus: "done",
	}},
	{EventEdited, EditedPayload{
		OldTitle: new("old"), NewTitle: new("new"), OldDesc: new("old desc"), NewDesc: new("new desc"),
	}},
	{EventMoved, MovedPayload{
		Direction: "before", RelativeTo: "AbC12", OldSortKey: "V00001", SortKey: "V00002",
	}},
	{EventReparented, ReparentedPayload{
		PriorParentID: "AbC12", NewParentID: "XyZ99", OldSortKey: "V00001", SortKey: "V00002",
		Direction: "after", RelativeTo: "QnB2g",
	}},
}

// TestEventPayloadsMarshalWithShortIDs marshals one populated value of
// every payload type and asserts that no JSON field whose name ends in
// "_id" or equals "id" carries a number — every task or criterion
// reference inside an event payload must be a short id (a string), never
// a row id, because row ids are minted by the local cache and differ per
// machine.
func TestEventPayloadsMarshalWithShortIDs(t *testing.T) {
	seen := map[EventType]bool{}
	for _, sample := range eventPayloadSamples {
		t.Run(string(sample.Type), func(t *testing.T) {
			if seen[sample.Type] {
				t.Fatalf("duplicate sample for event type %q", sample.Type)
			}
			seen[sample.Type] = true

			b, err := json.Marshal(sample.Payload)
			if err != nil {
				t.Fatalf("marshal %T: %v", sample.Payload, err)
			}
			var parsed any
			if err := json.Unmarshal(b, &parsed); err != nil {
				t.Fatalf("unmarshal %T: %v", sample.Payload, err)
			}
			assertNoNumericIDFields(t, "", parsed)
		})
	}
}

// assertNoNumericIDFields walks a decoded JSON value looking for any object
// field named "id" or ending in "_id" whose value is a JSON number (row
// ids are integers; short ids are always strings).
func assertNoNumericIDFields(t *testing.T, path string, v any) {
	t.Helper()
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			childPath := path + "." + k
			if k == "id" || strings.HasSuffix(k, "_id") {
				if _, isNumber := child.(float64); isNumber {
					t.Errorf("field %q at %s carries a number (%v); task/criterion references must be short ids",
						k, childPath, child)
				}
			}
			assertNoNumericIDFields(t, childPath, child)
		}
	case []any:
		for i, child := range val {
			assertNoNumericIDFields(t, path+"["+string(rune('0'+i))+"]", child)
		}
	}
}

// TestEveryEventTypeHasAPayloadSample cross-checks eventPayloadSamples
// against a hand-maintained list of every EventType constant, so adding a
// constant without a marshalling sample fails loudly (and vice versa).
func TestEveryEventTypeHasAPayloadSample(t *testing.T) {
	allTypes := []EventType{
		EventCreated, EventLabeled, EventUnlabeled, EventReleased, EventCanceled,
		EventBlocked, EventUnblocked, EventPurged, EventClaimExpired, EventNoted,
		EventClaimed, EventCriteriaAdded, EventCriterionState,
		EventHeartbeat, EventFoundInSet, EventFoundInCleared,
		EventKindChanged, EventDone, EventReopened, EventEdited, EventMoved,
		EventReparented,
	}
	have := map[EventType]bool{}
	for _, sample := range eventPayloadSamples {
		have[sample.Type] = true
	}
	for _, et := range allTypes {
		if !have[et] {
			t.Errorf("EventType %q has no sample in eventPayloadSamples", et)
		}
	}
	if len(eventPayloadSamples) != len(allTypes) {
		t.Errorf("eventPayloadSamples has %d entries, allTypes has %d; keep them in lockstep",
			len(eventPayloadSamples), len(allTypes))
	}
}

// An edit that clears the description is still an edit: the payload must
// carry new_desc as "" rather than omit it, or the log cannot replay it.
func TestEditedPayloadKeepsAnEmptyNewValue(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Title")
	desc := "was here"
	if err := RunEdit(db, id, nil, &desc, TestActor); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := RunEdit(db, id, nil, &empty, TestActor); err != nil {
		t.Fatal(err)
	}
	var detail string
	if err := db.QueryRow(
		`SELECT detail FROM events WHERE event_type = 'edited' ORDER BY id DESC LIMIT 1`,
	).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, `"new_desc":""`) {
		t.Fatalf("edited payload %s lost the empty new_desc", detail)
	}
}
