package job

import (
	"encoding/json"
	"testing"
)

func TestFoundIn_SetRecordsTheSource(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Ship v1")
	leaf := MustAdd(t, db, plan, "Wire the router")
	bug := MustAdd(t, db, "", "Router drops trailing slash")

	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("RunSetFoundIn: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil {
		t.Fatal("expected a found-in source, got none")
	}
	if src.ShortID != leaf {
		t.Fatalf("found-in source = %s, want %s", src.ShortID, leaf)
	}
}

func TestFoundIn_SetIsSingleValuedAndReplaces(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "First leaf")
	second := MustAdd(t, db, "", "Second leaf")
	bug := MustAdd(t, db, "", "A defect")

	if err := RunSetFoundIn(db, bug, first, TestActor); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := RunSetFoundIn(db, bug, second, TestActor); err != nil {
		t.Fatalf("replacing set: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil || src.ShortID != second {
		t.Fatalf("found-in source = %v, want %s", src, second)
	}

	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM found_in").Scan(&n); err != nil {
		t.Fatalf("count found_in: %v", err)
	}
	if n != 1 {
		t.Fatalf("found_in row count = %d, want 1 (one source per task)", n)
	}
}

func TestFoundIn_ClearRemovesTheReference(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if err := RunClearFoundIn(db, bug, TestActor); err != nil {
		t.Fatalf("RunClearFoundIn: %v", err)
	}
	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src != nil {
		t.Fatalf("found-in source = %s, want none after clear", src.ShortID)
	}
}

func TestFoundIn_ClearWithoutAReferenceIsAnError(t *testing.T) {
	db := SetupTestDB(t)
	bug := MustAdd(t, db, "", "A defect")
	if err := RunClearFoundIn(db, bug, TestActor); err == nil {
		t.Fatal("expected an error clearing a task with no found-in reference")
	}
}

func TestFoundIn_SelfReferenceRejected(t *testing.T) {
	db := SetupTestDB(t)
	bug := MustAdd(t, db, "", "A defect")
	err := RunSetFoundIn(db, bug, bug, TestActor)
	if err == nil {
		t.Fatal("expected an error setting a task's found-in source to itself")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM found_in").Scan(&n); err != nil {
		t.Fatalf("count found_in: %v", err)
	}
	if n != 0 {
		t.Fatalf("found_in row count = %d, want 0", n)
	}
}

func TestFoundIn_ChainsAreAllowed(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	c := MustAdd(t, db, "", "C")

	if err := RunSetFoundIn(db, a, b, TestActor); err != nil {
		t.Fatalf("a found in b: %v", err)
	}
	if err := RunSetFoundIn(db, b, c, TestActor); err != nil {
		t.Fatalf("b found in c: %v", err)
	}
	// A two-node loop is not a cycle worth rejecting: found-in never gates
	// anything, so there is no traversal to protect.
	if err := RunSetFoundIn(db, c, a, TestActor); err != nil {
		t.Fatalf("c found in a: %v", err)
	}
}

func TestFoundIn_UnknownTaskOrSourceIsAnError(t *testing.T) {
	db := SetupTestDB(t)
	real := MustAdd(t, db, "", "Real")
	if err := RunSetFoundIn(db, "zzzzz", real, TestActor); err == nil {
		t.Fatal("expected an error for an unknown task")
	}
	if err := RunSetFoundIn(db, real, "zzzzz", TestActor); err == nil {
		t.Fatal("expected an error for an unknown source")
	}
}

func TestFoundIn_SurvivesSourceDone(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	MustDone(t, db, leaf)

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil {
		t.Fatal("found-in reference vanished when the source was marked done")
	}
	if src.Status != "done" {
		t.Fatalf("source status = %s, want done", src.Status)
	}
}

func TestFoundIn_SurvivesSourceCanceled(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, _, err := RunCancel(db, []string{leaf}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil {
		t.Fatal("found-in reference vanished when the source was canceled")
	}
	if src.Status != "canceled" {
		t.Fatalf("source status = %s, want canceled", src.Status)
	}
}

func TestFoundIn_SurvivesSourceCanceledByCascade(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Ship v1")
	leaf := MustAdd(t, db, plan, "Wire the router")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, _, err := RunCancel(db, []string{plan}, "descoped", true, false, false, TestActor); err != nil {
		t.Fatalf("cascade cancel: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil {
		t.Fatal("found-in reference vanished when the source was canceled by cascade")
	}
	if src.Status != "canceled" {
		t.Fatalf("source status = %s, want canceled", src.Status)
	}
	// The issue itself is untouched by the cascade — it is not in the tree.
	if MustGet(t, db, bug).Status != "available" {
		t.Fatalf("the issue was closed by the source's cascade; found-in must not imply hierarchy")
	}
}

func TestFoundIn_SurvivesSourceSoftDelete(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, err := db.Exec("UPDATE tasks SET deleted_at = ? WHERE short_id = ?", 1, leaf); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil {
		t.Fatal("found-in reference vanished when the source was soft-deleted")
	}
	if src.ShortID != leaf {
		t.Fatalf("source = %s, want %s", src.ShortID, leaf)
	}
}

func TestFoundIn_PurgingTheSourceDropsTheEdge(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, _, err := RunCancel(db, []string{leaf}, "never happened", false, true, true, TestActor); err != nil {
		t.Fatalf("purge: %v", err)
	}

	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src != nil {
		t.Fatalf("found-in source = %s, want none after the source was purged", src.ShortID)
	}
	// The issue survives its purged source.
	if MustGet(t, db, bug).Status != "available" {
		t.Fatal("the issue was erased along with its found-in source")
	}
}

func TestFoundIn_PurgingTheTaskDropsTheEdge(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	if _, _, _, err := RunCancel(db, []string{bug}, "not a bug", false, true, true, TestActor); err != nil {
		t.Fatalf("purge: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM found_in").Scan(&n); err != nil {
		t.Fatalf("count found_in: %v", err)
	}
	if n != 0 {
		t.Fatalf("found_in row count = %d, want 0 after the referring task was purged", n)
	}
}

func TestFoundIn_CreatesNoBlockingRelationship(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	// The issue is claimable and closable while its source is wide open.
	if err := RunClaim(db, bug, "30m", "", TestActor, false); err != nil {
		t.Fatalf("claim the issue while the source is open: %v", err)
	}
	if _, _, err := RunDone(db, []string{bug}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("close the issue while the source is open: %v", err)
	}

	// And the reverse: reopen the issue, then close the source.
	if _, err := RunReopen(db, bug, false, TestActor); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := RunClaim(db, leaf, "30m", "", TestActor, false); err != nil {
		t.Fatalf("claim the source while the issue is open: %v", err)
	}
	if _, _, err := RunDone(db, []string{leaf}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("close the source while the issue is open: %v", err)
	}

	// Neither end appears in the blockers graph.
	blocked, err := getBlockedTaskIDs(db)
	if err != nil {
		t.Fatalf("getBlockedTaskIDs: %v", err)
	}
	if len(blocked) != 0 {
		t.Fatalf("found-in produced %d blocked task(s), want 0", len(blocked))
	}
}

func TestFoundIn_SurfacedListsTheOtherEnd(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bugA := MustAdd(t, db, "", "Defect A")
	bugB := MustAdd(t, db, "", "Defect B")
	other := MustAdd(t, db, "", "Unrelated")
	if err := RunSetFoundIn(db, bugA, leaf, TestActor); err != nil {
		t.Fatalf("set A: %v", err)
	}
	if err := RunSetFoundIn(db, bugB, leaf, TestActor); err != nil {
		t.Fatalf("set B: %v", err)
	}

	surfaced, err := GetSurfaced(db, leaf)
	if err != nil {
		t.Fatalf("GetSurfaced: %v", err)
	}
	if len(surfaced) != 2 {
		t.Fatalf("surfaced count = %d, want 2", len(surfaced))
	}
	got := map[string]bool{surfaced[0].ShortID: true, surfaced[1].ShortID: true}
	if !got[bugA] || !got[bugB] {
		t.Fatalf("surfaced = %v, want %s and %s", got, bugA, bugB)
	}

	empty, err := GetSurfaced(db, other)
	if err != nil {
		t.Fatalf("GetSurfaced on an unreferenced task: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("surfaced count = %d, want 0", len(empty))
	}
}

func TestFoundIn_SurfacedKeepsClosedIssues(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}
	MustDone(t, db, bug)

	surfaced, err := GetSurfaced(db, leaf)
	if err != nil {
		t.Fatalf("GetSurfaced: %v", err)
	}
	if len(surfaced) != 1 || surfaced[0].ShortID != bug {
		t.Fatalf("surfaced = %v, want the closed issue %s", surfaced, bug)
	}
}

func TestFoundIn_RecordsEvents(t *testing.T) {
	db := SetupTestDB(t)
	first := MustAdd(t, db, "", "First leaf")
	second := MustAdd(t, db, "", "Second leaf")
	bug := MustAdd(t, db, "", "A defect")

	if err := RunSetFoundIn(db, bug, first, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := RunSetFoundIn(db, bug, second, TestActor); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := RunClearFoundIn(db, bug, TestActor); err != nil {
		t.Fatalf("clear: %v", err)
	}

	events, err := GetEventsForTaskTree(db, bug)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	var kinds []string
	var details []map[string]any
	for _, e := range events {
		if e.EventType != "found_in_set" && e.EventType != "found_in_cleared" {
			continue
		}
		kinds = append(kinds, e.EventType)
		var d map[string]any
		if err := json.Unmarshal([]byte(e.Detail), &d); err != nil {
			t.Fatalf("unmarshal detail %q: %v", e.Detail, err)
		}
		details = append(details, d)
	}
	want := []string{"found_in_set", "found_in_set", "found_in_cleared"}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", kinds, want)
		}
	}
	if details[0]["source_id"] != first {
		t.Fatalf("first set source_id = %v, want %s", details[0]["source_id"], first)
	}
	if details[1]["source_id"] != second {
		t.Fatalf("second set source_id = %v, want %s", details[1]["source_id"], second)
	}
	if details[1]["previous_source_id"] != first {
		t.Fatalf("replacing set previous_source_id = %v, want %s", details[1]["previous_source_id"], first)
	}
	if details[2]["source_id"] != second {
		t.Fatalf("cleared source_id = %v, want %s", details[2]["source_id"], second)
	}
	for i, d := range details {
		if d["task_id"] != bug {
			t.Fatalf("event %d task_id = %v, want %s", i, d["task_id"], bug)
		}
	}
}

func TestRunInfo_CarriesBothEndsOfFoundIn(t *testing.T) {
	db := SetupTestDB(t)
	leaf := MustAdd(t, db, "", "A leaf")
	bug := MustAdd(t, db, "", "A defect")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	bugInfo, err := RunInfo(db, bug)
	if err != nil {
		t.Fatalf("RunInfo(bug): %v", err)
	}
	if bugInfo.FoundIn == nil || bugInfo.FoundIn.ShortID != leaf {
		t.Fatalf("bug FoundIn = %v, want %s", bugInfo.FoundIn, leaf)
	}
	if len(bugInfo.Surfaced) != 0 {
		t.Fatalf("bug Surfaced = %v, want empty", bugInfo.Surfaced)
	}

	leafInfo, err := RunInfo(db, leaf)
	if err != nil {
		t.Fatalf("RunInfo(leaf): %v", err)
	}
	if leafInfo.FoundIn != nil {
		t.Fatalf("leaf FoundIn = %v, want nil", leafInfo.FoundIn)
	}
	if len(leafInfo.Surfaced) != 1 || leafInfo.Surfaced[0].ShortID != bug {
		t.Fatalf("leaf Surfaced = %v, want [%s]", leafInfo.Surfaced, bug)
	}
}

func TestFormatEventDescription_FoundIn(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		detail    string
		want      string
	}{
		{"set", "found_in_set", `{"task_id":"aaaaa","source_id":"bbbbb"}`, "found in bbbbb"},
		{"replaced", "found_in_set", `{"task_id":"aaaaa","source_id":"bbbbb","previous_source_id":"ccccc"}`, "found in bbbbb (was ccccc)"},
		{"cleared", "found_in_cleared", `{"task_id":"aaaaa","source_id":"bbbbb"}`, "found-in cleared (was bbbbb)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatEventDescription(tc.eventType, tc.detail); got != tc.want {
				t.Fatalf("FormatEventDescription(%s) = %q, want %q", tc.eventType, got, tc.want)
			}
		})
	}
}

// The case the feature exists for: the plan finishes while the bug does not.
func TestFoundIn_SourceTreeAutoClosingLeavesTheIssueOpen(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Ship v1")
	leaf := MustAdd(t, db, plan, "Wire the router")
	issues := MustAdd(t, db, "", "Issues")
	bug := MustAdd(t, db, issues, "Router drops trailing slash")
	if err := RunSetFoundIn(db, bug, leaf, TestActor); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Closing the only leaf auto-closes the plan root.
	MustDone(t, db, leaf)
	if got := MustGet(t, db, plan).Status; got != "done" {
		t.Fatalf("plan status = %s, want done", got)
	}

	// The issue and its own root are untouched, and the reference stands.
	if got := MustGet(t, db, bug).Status; got != "available" {
		t.Fatalf("issue status = %s, want available", got)
	}
	if got := MustGet(t, db, issues).Status; got != "available" {
		t.Fatalf("issues root status = %s, want available", got)
	}
	src, err := GetFoundInSource(db, bug)
	if err != nil {
		t.Fatalf("GetFoundInSource: %v", err)
	}
	if src == nil || src.ShortID != leaf {
		t.Fatalf("found-in source = %v, want %s", src, leaf)
	}
	surfaced, err := GetSurfaced(db, leaf)
	if err != nil {
		t.Fatalf("GetSurfaced: %v", err)
	}
	if len(surfaced) != 1 || surfaced[0].ShortID != bug {
		t.Fatalf("surfaced = %v, want [%s]", surfaced, bug)
	}
}
