package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// seedIssueRootsCLI builds one task tree ("Plan" -> "Task leaf") and one
// issue tree ("Bugs" -> "Issue leaf") in a fresh CLI database, returning
// (dbFile, taskRoot, taskLeaf, issueRoot, issueLeaf).
func seedIssueRootsCLI(t *testing.T) (dbFile, taskRoot, taskLeaf, issueRoot, issueLeaf string) {
	t.Helper()
	dbFile = setupCLI(t)
	taskRoot = mustAddCLI(t, dbFile, "", "Plan")
	taskLeaf = mustAddCLI(t, dbFile, taskRoot, "Task leaf")
	issueRoot = mustAddCLI(t, dbFile, "", "Bugs")
	issueLeaf = mustAddCLI(t, dbFile, issueRoot, "Issue leaf")
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "kind", issueRoot, "issue"); err != nil {
		t.Fatalf("job kind %s issue: %v", issueRoot, err)
	}
	return dbFile, taskRoot, taskLeaf, issueRoot, issueLeaf
}

func TestLsDefaultOmitsIssueRootsAndAppendsTrailer(t *testing.T) {
	dbFile, taskRoot, taskLeaf, issueRoot, issueLeaf := seedIssueRootsCLI(t)

	out, _, err := runCLI(t, dbFile, "ls")
	if err != nil {
		t.Fatalf("job ls: %v", err)
	}
	if strings.Contains(out, issueRoot) {
		t.Errorf("default `ls` should omit the issue root, got:\n%s", out)
	}
	if strings.Contains(out, issueLeaf) {
		t.Errorf("default `ls` should omit issue-tree children too, got:\n%s", out)
	}
	if !strings.Contains(out, taskRoot) || !strings.Contains(out, taskLeaf) {
		t.Errorf("default `ls` should still show the task tree, got:\n%s", out)
	}
	wantTrailer := "Issues: 2 open · job ls --issues"
	if !strings.Contains(out, wantTrailer) {
		t.Errorf("expected trailer %q, got:\n%s", wantTrailer, out)
	}
	trimmed := strings.TrimRight(out, "\n")
	if !strings.HasSuffix(trimmed, wantTrailer) {
		t.Errorf("trailer should be the last line, got:\n%s", out)
	}
}

func TestLsNoTrailerWithoutIssueRoots(t *testing.T) {
	dbFile := setupCLI(t)
	mustAddCLI(t, dbFile, "", "Plan")

	out, _, err := runCLI(t, dbFile, "ls")
	if err != nil {
		t.Fatalf("job ls: %v", err)
	}
	if strings.Contains(out, "Issues:") {
		t.Errorf("no issue roots should mean no trailer, got:\n%s", out)
	}
}

func TestLsIssuesShowsOnlyIssueRootsNoTrailer(t *testing.T) {
	dbFile, taskRoot, taskLeaf, issueRoot, issueLeaf := seedIssueRootsCLI(t)

	out, _, err := runCLI(t, dbFile, "ls", "--issues")
	if err != nil {
		t.Fatalf("job ls --issues: %v", err)
	}
	if strings.Contains(out, taskRoot) || strings.Contains(out, taskLeaf) {
		t.Errorf("`ls --issues` should not show task-tree rows, got:\n%s", out)
	}
	if !strings.Contains(out, issueRoot) || !strings.Contains(out, issueLeaf) {
		t.Errorf("`ls --issues` should show the issue tree, got:\n%s", out)
	}
	if strings.Contains(out, "Issues:") {
		t.Errorf("`ls --issues` should not print the trailer, got:\n%s", out)
	}
	if strings.Contains(out, "issue-tree") {
		t.Errorf("`ls --issues` should drop the now-redundant issue-tree tag, got:\n%s", out)
	}
}

func TestLsIssuesWithNoIssueRootsIsFriendlyNotError(t *testing.T) {
	dbFile := setupCLI(t)
	mustAddCLI(t, dbFile, "", "Plan")

	out, _, err := runCLI(t, dbFile, "ls", "--issues")
	if err != nil {
		t.Fatalf("job ls --issues with no issue roots should not error: %v", err)
	}
	if out == "" {
		t.Error("expected some explanatory output, got empty string")
	}
}

func TestLsExplicitIssueRootIDUnchanged(t *testing.T) {
	dbFile, _, _, issueRoot, issueLeaf := seedIssueRootsCLI(t)

	out, _, err := runCLI(t, dbFile, "ls", issueRoot)
	if err != nil {
		t.Fatalf("job ls %s: %v", issueRoot, err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("`ls <issue-root>` should show its children, got:\n%s", out)
	}
	if strings.Contains(out, "Issues:") {
		t.Errorf("a scoped `ls <id>` should not print the forest-wide trailer, got:\n%s", out)
	}
}

func TestLsFormatJSONKeepsEveryRootWithKind(t *testing.T) {
	dbFile, taskRoot, _, issueRoot, _ := seedIssueRootsCLI(t)

	out, _, err := runCLI(t, dbFile, "ls", "--format=json")
	if err != nil {
		t.Fatalf("job ls --format=json: %v", err)
	}
	var nodes []map[string]any
	if err := json.Unmarshal([]byte(out), &nodes); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	var sawTask, sawIssue bool
	for _, n := range nodes {
		switch n["id"] {
		case taskRoot:
			sawTask = true
			if k, ok := n["kind"]; ok && k != "" {
				t.Errorf("task root should not carry a non-empty kind, got %v", k)
			}
		case issueRoot:
			sawIssue = true
			if n["kind"] != "issue" {
				t.Errorf("issue root kind = %v, want \"issue\"", n["kind"])
			}
		}
	}
	if !sawTask {
		t.Errorf("JSON output missing task root %s:\n%s", taskRoot, out)
	}
	if !sawIssue {
		t.Errorf("JSON output missing issue root %s:\n%s", issueRoot, out)
	}
	if strings.Contains(out, "Issues:") {
		t.Errorf("JSON output should never carry the text trailer, got:\n%s", out)
	}
}

func TestLsIssuesComposesWithMine(t *testing.T) {
	dbFile, _, _, issueRoot, issueLeaf := seedIssueRootsCLI(t)
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", issueLeaf); err != nil {
		t.Fatalf("claim: %v", err)
	}
	otherLeaf := mustAddCLI(t, dbFile, issueRoot, "unclaimed issue leaf")

	out, _, err := runCLI(t, dbFile, "ls", "--issues", "--mine", "--as", "alice")
	if err != nil {
		t.Fatalf("job ls --issues --mine: %v", err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("expected alice's claimed issue leaf, got:\n%s", out)
	}
	if strings.Contains(out, otherLeaf) {
		t.Errorf("--mine should exclude the unclaimed leaf, got:\n%s", out)
	}
}

func TestLsIssuesComposesWithLabel(t *testing.T) {
	dbFile, _, _, _, issueLeaf := seedIssueRootsCLI(t)
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "label", "add", issueLeaf, "p0"); err != nil {
		t.Fatalf("label: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "ls", "--issues", "-l", "p0")
	if err != nil {
		t.Fatalf("job ls --issues -l p0: %v", err)
	}
	if !strings.Contains(out, issueLeaf) {
		t.Errorf("expected the labeled issue leaf, got:\n%s", out)
	}
}

func TestLsAllClosedFooterScopedByKindInBothModes(t *testing.T) {
	dbFile := setupCLI(t)
	taskRoot := mustAddCLI(t, dbFile, "", "closed task root")
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "done", taskRoot); err != nil {
		t.Fatalf("done: %v", err)
	}
	issueRoot := mustAddCLI(t, dbFile, "", "closed issue root")
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "kind", issueRoot, "issue"); err != nil {
		t.Fatalf("kind: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "tester", "done", issueRoot); err != nil {
		t.Fatalf("done: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "ls", "--all")
	if err != nil {
		t.Fatalf("job ls --all: %v", err)
	}
	if !strings.Contains(out, "Recently closed (1 of 1)") {
		t.Errorf("default `ls --all` footer should scope to task-tree closures only, got:\n%s", out)
	}
	if !strings.Contains(out, taskRoot) {
		t.Errorf("expected closed task root in the footer, got:\n%s", out)
	}
	if strings.Contains(out, issueRoot) {
		t.Errorf("default footer should not include the closed issue root, got:\n%s", out)
	}

	out, _, err = runCLI(t, dbFile, "ls", "--issues", "--all")
	if err != nil {
		t.Fatalf("job ls --issues --all: %v", err)
	}
	if !strings.Contains(out, "Recently closed (1 of 1)") {
		t.Errorf("`ls --issues --all` footer should scope to issue-tree closures only, got:\n%s", out)
	}
	if !strings.Contains(out, issueRoot) {
		t.Errorf("expected closed issue root in the footer, got:\n%s", out)
	}
	if strings.Contains(out, taskRoot) {
		t.Errorf("issues footer should not include the closed task root, got:\n%s", out)
	}
}
