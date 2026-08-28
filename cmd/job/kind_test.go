package main

import (
	"strings"
	"testing"
)

// seedKindCLI builds one task tree and one issue tree in a fresh CLI database
// and returns (dbFile, taskLeaf, issueRoot, issueLeaf).
func seedKindCLI(t *testing.T) (string, string, string, string) {
	t.Helper()
	dbFile := setupCLI(t)
	plan := mustAddCLI(t, dbFile, "", "Plan")
	taskLeaf := mustAddCLI(t, dbFile, plan, "Task leaf")
	bugs := mustAddCLI(t, dbFile, "", "Bugs")
	issueLeaf := mustAddCLI(t, dbFile, bugs, "Issue leaf")
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "kind", bugs, "issue"); err != nil {
		t.Fatalf("job kind %s issue: %v", bugs, err)
	}
	return dbFile, taskLeaf, bugs, issueLeaf
}

func mustAddCLI(t *testing.T, dbFile, parent, title string) string {
	t.Helper()
	args := []string{"--as", "tester", "add"}
	if parent != "" {
		args = append(args, parent)
	}
	args = append(args, title, "--id-only")
	out, _, err := runCLI(t, dbFile, args...)
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return strings.TrimSpace(out)
}

func TestKindCmdSetsAndReportsIssueRoot(t *testing.T) {
	dbFile := setupCLI(t)
	root := mustAddCLI(t, dbFile, "", "Bugs")

	out, _, err := runCLI(t, dbFile, "--as", "tester", "kind", root, "issue")
	if err != nil {
		t.Fatalf("job kind: %v", err)
	}
	if !strings.Contains(out, root) || !strings.Contains(out, "issue") {
		t.Errorf("ack %q should name the root and the new kind", out)
	}

	show, _, err := runCLI(t, dbFile, "show", root)
	if err != nil {
		t.Fatalf("job show: %v", err)
	}
	if !strings.Contains(show, "Kind:") || !strings.Contains(show, "issue") {
		t.Errorf("job show on an issue root should print the kind:\n%s", show)
	}
}

func TestKindCmdReadsBackCurrentKind(t *testing.T) {
	dbFile, _, issueRoot, _ := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "kind", issueRoot)
	if err != nil {
		t.Fatalf("job kind <id>: %v", err)
	}
	if !strings.Contains(out, "issue") {
		t.Errorf("`job kind <id>` should report the current kind, got %q", out)
	}
}

func TestKindCmdConvertsBackToTask(t *testing.T) {
	dbFile, _, issueRoot, _ := seedKindCLI(t)

	if _, _, err := runCLI(t, dbFile, "--as", "tester", "kind", issueRoot, "task"); err != nil {
		t.Fatalf("job kind %s task: %v", issueRoot, err)
	}
	show, _, err := runCLI(t, dbFile, "show", issueRoot)
	if err != nil {
		t.Fatalf("job show: %v", err)
	}
	if strings.Contains(show, "Kind:") {
		t.Errorf("a converted-back root should not print a kind line:\n%s", show)
	}
}

func TestKindCmdRejectsNonRoot(t *testing.T) {
	dbFile, _, _, issueLeaf := seedKindCLI(t)

	_, _, err := runCLI(t, dbFile, "--as", "tester", "kind", issueLeaf, "issue")
	if err == nil {
		t.Fatal("want error setting kind on a non-root")
	}
}

func TestKindCmdRejectsUnknownKind(t *testing.T) {
	dbFile := setupCLI(t)
	root := mustAddCLI(t, dbFile, "", "Bugs")

	_, _, err := runCLI(t, dbFile, "--as", "tester", "kind", root, "bug")
	if err == nil {
		t.Fatal("want error for an unknown kind")
	}
	if !strings.Contains(err.Error(), "task") || !strings.Contains(err.Error(), "issue") {
		t.Errorf("error %q should name the two valid kinds", err)
	}
}

func TestKindCmdRequiresIdentityToWrite(t *testing.T) {
	dbFile := setupCLI(t)
	root := mustAddCLI(t, dbFile, "", "Bugs")

	_, _, err := runCLI(t, dbFile, "kind", root, "issue")
	if err == nil || !strings.Contains(err.Error(), "identity required") {
		t.Fatalf("want identity-required error, got %v", err)
	}
}

func TestAddKindIssueCreatesIssueRoot(t *testing.T) {
	dbFile := setupCLI(t)

	out, _, err := runCLI(t, dbFile, "--as", "tester", "add", "Bugs", "--kind", "issue", "--id-only")
	if err != nil {
		t.Fatalf("job add --kind issue: %v", err)
	}
	root := strings.TrimSpace(out)
	show, _, err := runCLI(t, dbFile, "show", root)
	if err != nil {
		t.Fatalf("job show: %v", err)
	}
	if !strings.Contains(show, "Kind:") {
		t.Errorf("add --kind issue did not create an issue root:\n%s", show)
	}
}

func TestAddKindIssueUnderParentIsError(t *testing.T) {
	dbFile := setupCLI(t)
	root := mustAddCLI(t, dbFile, "", "Plan")

	_, _, err := runCLI(t, dbFile, "--as", "tester", "add", root, "Bug", "--kind", "issue")
	if err == nil {
		t.Fatal("want error: --kind issue is only valid when creating a root")
	}
}

// TestListMarksIssueRootsCLI used to assert that default `job ls` tagged an
// issue root inline as `(issue-tree)`. That tag is superseded by the
// task/issue split added in the ls-issues leaf: default `ls` now omits
// issue roots entirely (pointing at `job ls --issues` via a trailer
// instead), and `ls --issues` itself drops the tag as redundant — every row
// it renders is already an issue root. See cmd/job/list_issues_test.go for
// the new coverage.
func TestListOmitsIssueRootsByDefaultCLI(t *testing.T) {
	dbFile, _, issueRoot, _ := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "ls")
	if err != nil {
		t.Fatalf("job ls: %v", err)
	}
	if strings.Contains(out, issueRoot) {
		t.Errorf("default `job ls` should omit the issue root %s, got:\n%s", issueRoot, out)
	}
	if !strings.Contains(out, "Issues: 2 open · job ls --issues") {
		t.Errorf("default `job ls` should end with the Issues trailer, got:\n%s", out)
	}
}

func TestNextCLIExcludesIssueTrees(t *testing.T) {
	dbFile, taskLeaf, _, issueLeaf := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "next")
	if err != nil {
		t.Fatalf("job next: %v", err)
	}
	if !strings.Contains(out, taskLeaf) {
		t.Errorf("job next = %q, want the task leaf %s", out, taskLeaf)
	}
	if strings.Contains(out, issueLeaf) {
		t.Errorf("job next surfaced an issue-tree leaf: %q", out)
	}
}

func TestNextCLIIssuesFlag(t *testing.T) {
	dbFile, taskLeaf, _, issueLeaf := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "next", "--issues")
	if err != nil {
		t.Fatalf("job next --issues: %v", err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("job next --issues = %q, want the issue leaf %s", out, issueLeaf)
	}
	if strings.Contains(out, taskLeaf) {
		t.Errorf("job next --issues surfaced a task-tree leaf: %q", out)
	}
}

func TestOrientCLIIssuesFlag(t *testing.T) {
	dbFile, taskLeaf, _, issueLeaf := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "orient")
	if err != nil {
		t.Fatalf("job orient: %v", err)
	}
	if !strings.Contains(out, taskLeaf) {
		t.Errorf("job orient = %q, want the task leaf %s", out, taskLeaf)
	}

	out, _, err = runCLI(t, dbFile, "orient", "--issues")
	if err != nil {
		t.Fatalf("job orient --issues: %v", err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("job orient --issues = %q, want the issue leaf %s", out, issueLeaf)
	}
}

func TestClaimNextCLIIssuesFlag(t *testing.T) {
	dbFile, taskLeaf, _, issueLeaf := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "--as", "tester", "claim", "--next", "-q")
	if err != nil {
		t.Fatalf("job claim --next: %v", err)
	}
	if !strings.Contains(out, taskLeaf) {
		t.Errorf("job claim --next = %q, want %s", out, taskLeaf)
	}

	out, _, err = runCLI(t, dbFile, "--as", "tester2", "claim", "--next", "--issues", "-q")
	if err != nil {
		t.Fatalf("job claim --next --issues: %v", err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("job claim --next --issues = %q, want %s", out, issueLeaf)
	}
}

func TestKindChangeAppearsInLog(t *testing.T) {
	dbFile, _, issueRoot, _ := seedKindCLI(t)

	out, _, err := runCLI(t, dbFile, "log", issueRoot)
	if err != nil {
		t.Fatalf("job log: %v", err)
	}
	if !strings.Contains(out, "issue") {
		t.Errorf("kind change is not visible in `job log`:\n%s", out)
	}
}

func TestMoveIssueRootUnderParentIsError(t *testing.T) {
	dbFile, taskLeaf, issueRoot, _ := seedKindCLI(t)

	_, _, err := runCLI(t, dbFile, "--as", "tester", "move", issueRoot, "under", taskLeaf)
	if err == nil {
		t.Fatal("want error moving an issue root under a parent")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error %q should point at `job kind`", err)
	}
}
