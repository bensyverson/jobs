package job

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// 4GMzO — Focus is one root per *kind* per actor. A focus_set event records
// the root's kind at the time it was set (roots convert, so the event stores
// what was true), and resolution asks per kind: the latest focus_set of that
// kind not followed by a focus_released for that kind. Claiming inside an
// issue tree moves only the issue focus; closing an issue root releases only
// the issue focus.

func mustSetFocus(t *testing.T, db *sql.DB, shortID, actor string) {
	t.Helper()
	if _, err := SetFocus(db, shortID, actor); err != nil {
		t.Fatalf("SetFocus(%s, %s): %v", shortID, actor, err)
	}
}

// seedTwoKinds returns a task root with a leaf and an issue root with a leaf.
func seedTwoKinds(t *testing.T, db *sql.DB) (taskRoot, taskLeaf, issueRoot, issueLeaf string) {
	t.Helper()
	taskRoot = MustAdd(t, db, "", "Plan")
	taskLeaf = MustAdd(t, db, taskRoot, "Plan leaf")
	issueRoot = MustAdd(t, db, "", "Bugs")
	issueLeaf = MustAdd(t, db, issueRoot, "Bug leaf")
	mustSetKind(t, db, issueRoot, KindIssue)
	return
}

// Setting a focus on each kind leaves both live and independently readable.
func TestSetFocus_KeepsOneSlotPerKind(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, _, issueRoot, _ := seedTwoKinds(t, db)

	mustSetFocus(t, db, taskRoot, TestActor)
	mustSetFocus(t, db, issueRoot, TestActor)

	got, err := GetFocusKind(db, TestActor, KindTask)
	if err != nil {
		t.Fatalf("GetFocusKind(task): %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("task focus: got %v, want %s", got, taskRoot)
	}

	got, err = GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind(issue): %v", err)
	}
	if got == nil || got.ShortID != issueRoot {
		t.Errorf("issue focus: got %v, want %s", got, issueRoot)
	}

	// GetFocus is the task-kind accessor its callers already assume.
	got, err = GetFocus(db, TestActor)
	if err != nil {
		t.Fatalf("GetFocus: %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("GetFocus: got %v, want the task focus %s", got, taskRoot)
	}
}

// The focus_set event carries the root's kind, so a later conversion cannot
// retroactively move a focus between slots.
func TestSetFocus_RecordsTheRootKindOnTheEvent(t *testing.T) {
	db := SetupTestDB(t)
	_, _, issueRoot, _ := seedTwoKinds(t, db)

	mustSetFocus(t, db, issueRoot, TestActor)

	var detail string
	if err := db.QueryRow(
		"SELECT detail FROM events WHERE event_type = 'focus_set' AND actor = ? ORDER BY id DESC LIMIT 1",
		TestActor,
	).Scan(&detail); err != nil {
		t.Fatalf("read focus_set detail: %v", err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatalf("unmarshal detail %q: %v", detail, err)
	}
	if d["kind"] != string(KindIssue) {
		t.Errorf("focus_set detail kind: got %v, want %q (detail: %s)", d["kind"], KindIssue, detail)
	}
}

// `job focus <id>` is allowed to name any task; focus is always its root.
func TestSetFocus_ResolvesAChildToItsRoot(t *testing.T) {
	db := SetupTestDB(t)
	_, _, issueRoot, issueLeaf := seedTwoKinds(t, db)

	mustSetFocus(t, db, issueLeaf, TestActor)

	got, err := GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind(issue): %v", err)
	}
	if got == nil || got.ShortID != issueRoot {
		t.Errorf("issue focus after focusing a leaf: got %v, want its root %s", got, issueRoot)
	}
}

// ZKx — releasing one kind leaves the other standing.
func TestReleaseFocusKind_ReleasesOnlyThatKind(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, _, issueRoot, _ := seedTwoKinds(t, db)

	mustSetFocus(t, db, taskRoot, TestActor)
	mustSetFocus(t, db, issueRoot, TestActor)

	released, err := ReleaseFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("ReleaseFocusKind(issue): %v", err)
	}
	if released == nil || released.ShortID != issueRoot {
		t.Errorf("ReleaseFocusKind returned %v, want the released root %s", released, issueRoot)
	}

	if got, err := GetFocusKind(db, TestActor, KindIssue); err != nil || got != nil {
		t.Errorf("issue focus after release: got %v (err %v), want nil", got, err)
	}
	got, err := GetFocusKind(db, TestActor, KindTask)
	if err != nil {
		t.Fatalf("GetFocusKind(task): %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("task focus after releasing the issue focus: got %v, want %s", got, taskRoot)
	}
}

// Releasing a kind that has no live focus is a quiet no-op.
func TestReleaseFocusKind_NoFocus_NoOp(t *testing.T) {
	db := SetupTestDB(t)
	released, err := ReleaseFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("ReleaseFocusKind with no focus: %v", err)
	}
	if released != nil {
		t.Errorf("ReleaseFocusKind with no focus returned %v, want nil", released)
	}
}

// Bare ReleaseFocus clears every kind.
func TestReleaseFocus_ReleasesEveryKind(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, _, issueRoot, _ := seedTwoKinds(t, db)

	mustSetFocus(t, db, taskRoot, TestActor)
	mustSetFocus(t, db, issueRoot, TestActor)

	if err := ReleaseFocus(db, TestActor); err != nil {
		t.Fatalf("ReleaseFocus: %v", err)
	}
	for _, kind := range []TreeKind{KindTask, KindIssue} {
		if got, err := GetFocusKind(db, TestActor, kind); err != nil || got != nil {
			t.Errorf("%s focus after ReleaseFocus: got %v (err %v), want nil", kind, got, err)
		}
	}
}

// 9Mh — claiming inside an issue tree moves the issue focus only.
func TestClaimInIssueTree_MovesTheIssueFocusOnly(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, taskLeaf, issueRoot, issueLeaf := seedTwoKinds(t, db)

	MustClaim(t, db, taskLeaf, "1h")
	MustClaim(t, db, issueLeaf, "1h")

	got, err := GetFocusKind(db, TestActor, KindTask)
	if err != nil {
		t.Fatalf("GetFocusKind(task): %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("task focus after claiming in the issue tree: got %v, want %s (unchanged)", got, taskRoot)
	}
	got, err = GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind(issue): %v", err)
	}
	if got == nil || got.ShortID != issueRoot {
		t.Errorf("issue focus after claiming in the issue tree: got %v, want %s", got, issueRoot)
	}
}

// 9Mh (vice versa) — claiming inside a task tree leaves the issue focus alone.
func TestClaimInTaskTree_LeavesTheIssueFocusAlone(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, taskLeaf, issueRoot, issueLeaf := seedTwoKinds(t, db)

	MustClaim(t, db, issueLeaf, "1h")
	MustClaim(t, db, taskLeaf, "1h")

	got, err := GetFocusKind(db, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("GetFocusKind(issue): %v", err)
	}
	if got == nil || got.ShortID != issueRoot {
		t.Errorf("issue focus after claiming in the task tree: got %v, want %s (unchanged)", got, issueRoot)
	}
	got, err = GetFocusKind(db, TestActor, KindTask)
	if err != nil {
		t.Fatalf("GetFocusKind(task): %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("task focus after claiming in the task tree: got %v, want %s", got, taskRoot)
	}
}

// n7J — closing an issue root releases every actor's issue focus on it and
// leaves task focuses untouched.
// lXi9K — an issue-tree root never auto-closes (open-ended by design), so
// the leaf-frontier cascade must never reach releaseFocusOnRootClose for it:
// closing the last open leaf under an issue root leaves the root, and every
// actor's issue focus on it, exactly as they were. This test previously
// named itself for the opposite (buggy) behavior, where the cascade closed
// the issue root and released everyone's issue focus.
func TestCloseIssueLeaf_LeavesIssueFocusInPlace(t *testing.T) {
	db := SetupTestDB(t)
	taskRoot, taskLeaf, issueRoot, issueLeaf := seedTwoKinds(t, db)

	MustClaim(t, db, taskLeaf, "1h")
	MustClaim(t, db, issueLeaf, "1h")
	mustSetFocus(t, db, issueRoot, "bystander")

	// Closing the only open leaf no longer cascade-closes the issue root.
	if _, _, err := RunDone(db, []string{issueLeaf}, false, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done issue leaf: %v", err)
	}

	for _, actor := range []string{TestActor, "bystander"} {
		got, err := GetFocusKind(db, actor, KindIssue)
		if err != nil {
			t.Fatalf("GetFocusKind(issue) for %s: %v", actor, err)
		}
		if got == nil || got.ShortID != issueRoot {
			t.Errorf("issue focus for %s after the leaf closed: got %v, want %s (root stays open)", actor, got, issueRoot)
		}
	}
	got, err := GetFocusKind(db, TestActor, KindTask)
	if err != nil {
		t.Fatalf("GetFocusKind(task): %v", err)
	}
	if got == nil || got.ShortID != taskRoot {
		t.Errorf("task focus after an issue leaf closed: got %v, want %s (untouched)", got, taskRoot)
	}
	if n := focusReleasedCount(t, db, "bystander"); n != 0 {
		t.Errorf("focus_released events for the bystander: got %d, want 0 (root never closes)", n)
	}
}

// COY — `next --issues` walks the focused issue root when one is set.
func TestNextIssues_ScopesToTheIssueFocus(t *testing.T) {
	db := SetupTestDB(t)
	_, _, _, _ = seedTwoKinds(t, db)
	otherIssueRoot := MustAdd(t, db, "", "Other bugs")
	otherIssueLeaf := MustAdd(t, db, otherIssueRoot, "Other bug leaf")
	mustSetKind(t, db, otherIssueRoot, KindIssue)

	mustSetFocus(t, db, otherIssueRoot, TestActor)

	got, err := RunNextFiltered(db, "", TestActor, "", false, true)
	if err != nil {
		t.Fatalf("RunNextFiltered(--issues): %v", err)
	}
	if got.ShortID != otherIssueLeaf {
		t.Errorf("next --issues with an issue focus: got %s, want %s", got.ShortID, otherIssueLeaf)
	}
}

// An exhausted focused issue root fails loudly, mirroring the task side,
// rather than silently crossing into another issue tree.
func TestNextIssues_ExhaustedIssueFocus_FailsLoudly(t *testing.T) {
	db := SetupTestDB(t)
	_, _, issueRoot, issueLeaf := seedTwoKinds(t, db)
	otherIssueRoot := MustAdd(t, db, "", "Other bugs")
	MustAdd(t, db, otherIssueRoot, "Other bug leaf")
	mustSetKind(t, db, otherIssueRoot, KindIssue)

	MustClaim(t, db, issueLeaf, "1h") // focuses the issue root and empties it

	_, err := RunNextFiltered(db, "", TestActor, "", false, true)
	if err == nil {
		t.Fatalf("next --issues with an exhausted issue focus: want an error")
	}
	if !strings.Contains(err.Error(), issueRoot) || !strings.Contains(err.Error(), "focus --release") {
		t.Errorf("error must name the focused root and the escape: %q", err.Error())
	}
}

// Bare `next` reads the task focus only: an issue focus never scopes the plan
// frontier, and never turns it into an issue walk.
func TestNext_IgnoresAnIssueFocus(t *testing.T) {
	db := SetupTestDB(t)
	_, taskLeaf, issueRoot, _ := seedTwoKinds(t, db)

	mustSetFocus(t, db, issueRoot, TestActor)

	got, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext with an issue focus: %v", err)
	}
	if got.ShortID != taskLeaf {
		t.Errorf("bare next with an issue focus: got %s, want the task-tree leaf %s", got.ShortID, taskLeaf)
	}
}
