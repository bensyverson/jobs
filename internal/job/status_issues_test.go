package job

import (
	"bytes"
	"strings"
	"testing"
)

// `status`'s per-root rollup lists task roots only (decision 4,
// project/2026-08-28-issues-ux.md): work under issue-tree roots is folded
// into one summary line instead of one row per issue root. These tests
// cover BuildIssuesStatus, the computation behind that line.

func TestBuildIssuesStatus_NoIssueRoots_ReturnsNil(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "TaskRoot")

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil with no issue roots, got %+v", got)
	}
}

func TestBuildIssuesStatus_CountsOpenAcrossIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leaf1 := MustAdd(t, db, bugs, "Leaf 1")
	MustAdd(t, db, bugs, "Leaf 2")
	MustDone(t, db, leaf1)

	other := MustAdd(t, db, "", "Other issues")
	mustSetKind(t, db, other, KindIssue)
	MustAdd(t, db, other, "Leaf 3")

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil status with issue roots present")
	}
	// Leaf 1 is done; Leaf 2 and Leaf 3 are open. Total open = 2.
	if got.Open != 2 {
		t.Errorf("Open = %d, want 2", got.Open)
	}
}

func TestBuildIssuesStatus_ExcludesTaskTreeDescendants(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	MustAdd(t, db, plan, "Task leaf")

	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	MustAdd(t, db, bugs, "Issue leaf")

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got.Open != 1 {
		t.Errorf("Open = %d, want 1 (task-tree leaf must not count)", got.Open)
	}
}

func TestBuildIssuesStatus_ClaimedCountsLiveClaims(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leaf := MustAdd(t, db, bugs, "Leaf")
	if err := RunClaim(db, leaf, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got.Claimed != 1 {
		t.Errorf("Claimed = %d, want 1", got.Claimed)
	}
	// The claimed task is still open work.
	if got.Open != 1 {
		t.Errorf("Open = %d, want 1 (claimed task still counts as open)", got.Open)
	}
}

// claimedActor scopes Claimed the same way the status preamble's tally
// scopes its own claimed count: empty counts every live claim, a specific
// actor narrows to that actor's own claims only.
func TestBuildIssuesStatus_ClaimedScopedToActor(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leafA := MustAdd(t, db, bugs, "Leaf A")
	leafB := MustAdd(t, db, bugs, "Leaf B")
	if err := RunClaim(db, leafA, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim leafA: %v", err)
	}
	if err := RunClaim(db, leafB, "1h", "", "bob", false); err != nil {
		t.Fatalf("claim leafB: %v", err)
	}

	got, err := BuildIssuesStatus(db, "alice", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got.Claimed != 1 {
		t.Errorf("Claimed scoped to alice = %d, want 1", got.Claimed)
	}
	if got.Open != 2 {
		t.Errorf("Open = %d, want 2 (unaffected by actor scoping)", got.Open)
	}

	all, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if all.Claimed != 2 {
		t.Errorf("unscoped Claimed = %d, want 2", all.Claimed)
	}
}

func TestBuildIssuesStatus_NextNamesClaimableIssueLeaf(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leaf := MustAdd(t, db, bugs, "Leaf")

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got.Next == nil || got.Next.ShortID != leaf {
		t.Errorf("Next = %+v, want leaf %s", got.Next, leaf)
	}
}

// lXi9K — an issue-tree root never auto-closes (it is open-ended by
// design), so closing its last child leaves the root itself open with no
// open children. That makes the root the frontier: `job next --issues`
// surfaces it, matching the docs' "the next open issue" — there is nothing
// left to point at except the container itself. This test previously
// asserted Next == nil under the old (buggy) cascade, where closing the
// leaf also closed the root out from under it.
func TestBuildIssuesStatus_NextNilWhenIssueRootExhausted(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)
	leaf := MustAdd(t, db, bugs, "Leaf")
	MustDone(t, db, leaf)

	got, err := BuildIssuesStatus(db, "", "")
	if err != nil {
		t.Fatalf("BuildIssuesStatus: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil status: the issue root still exists")
	}
	// The root stays open (it never auto-closes) but it is not a unit of
	// work, so an exhausted issue tree has nothing next.
	if got.Next != nil {
		t.Errorf("Next = %+v, want nil: an issue root is never the next issue", got.Next)
	}
}

// RenderSummary prints the Issues: line after the per-root rollup and
// before Focus:/Next:, omitting the "· next <id>" tail when there is
// nothing claimable.
func TestRenderSummary_AppendsIssuesLine(t *testing.T) {
	s := &Summary{
		DirectChildren: []*SubtreeRollup{{ShortID: "abc12", Title: "TaskRoot"}},
		Issues:         &IssuesStatus{Open: 3, Claimed: 1, Next: &Task{ShortID: "xyz99", Title: "Bug"}},
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()
	want := "Issues: 3 open (1 claimed) · next xyz99"
	if !strings.Contains(got, want) {
		t.Errorf("expected %q in:\n%s", want, got)
	}
}

func TestRenderSummary_IssuesLineOmitsNextWhenNoneClaimable(t *testing.T) {
	s := &Summary{
		DirectChildren: []*SubtreeRollup{{ShortID: "abc12", Title: "TaskRoot"}},
		Issues:         &IssuesStatus{Open: 0, Claimed: 0, Next: nil},
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()
	if !strings.Contains(got, "Issues: 0 open (0 claimed)\n") {
		t.Errorf("expected Issues line with no next tail in:\n%s", got)
	}
	if strings.Contains(got, "· next") {
		t.Errorf("should not print a next tail when nothing is claimable:\n%s", got)
	}
}

func TestRenderSummary_NoIssuesLineWhenNilIssues(t *testing.T) {
	s := &Summary{
		DirectChildren: []*SubtreeRollup{{ShortID: "abc12", Title: "TaskRoot"}},
	}
	var buf bytes.Buffer
	RenderSummary(&buf, s)
	got := buf.String()
	if strings.Contains(got, "Issues:") {
		t.Errorf("should not print an Issues: line when Issues is nil:\n%s", got)
	}
}
