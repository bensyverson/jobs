package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 88QEE — `job issue <title>` is `add` with the parent resolved and the
// provenance defaulted: it targets the caller's focused issue root, else the
// sole issue root, and records a found-in edge to the caller's single live
// claim.

// addIssueRoot creates an issue-tree root through the CLI and returns its id.
func addIssueRoot(t *testing.T, dbFile, title string) string {
	t.Helper()
	out, _, err := runCLI(t, dbFile, "add", "--parent", "", title,
		"--kind", "issue", "--id-only", "--as", "tester")
	if err != nil {
		t.Fatalf("add --kind issue %q: %v", title, err)
	}
	return strings.TrimSpace(out)
}

// newIssueID reads the new task's id off the first line: `issue` leads with
// it, exactly as `add` does.
func newIssueID(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

func mustShow(t *testing.T, dbFile, shortID string) string {
	t.Helper()
	out, _, err := runCLI(t, dbFile, "show", shortID)
	if err != nil {
		t.Fatalf("show %s: %v", shortID, err)
	}
	return out
}

func TestIssueCmd_CreatesUnderTheSoleIssueRoot(t *testing.T) {
	dbFile := setupCLI(t)
	addTask(t, dbFile, "", "A plan")
	bugs := addIssueRoot(t, dbFile, "Bugs")

	out, _, err := runCLI(t, dbFile, "issue", "Router drops trailing slash", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	id := newIssueID(out)
	if id == "" {
		t.Fatalf("issue printed %q, want the new short id on the first line", out)
	}
	show := mustShow(t, dbFile, id)
	if !strings.Contains(show, "Parent:") || !strings.Contains(show, bugs) {
		t.Fatalf("show = %q, want a Parent: line naming the issue root %s", show, bugs)
	}
	if !strings.Contains(show, "Router drops trailing slash") {
		t.Fatalf("show = %q, want the title", show)
	}
}

func TestIssueCmd_PrefersTheFocusedIssueRoot(t *testing.T) {
	dbFile := setupCLI(t)
	addIssueRoot(t, dbFile, "Bugs")
	inbox := addIssueRoot(t, dbFile, "Inbox")
	if _, _, err := runCLI(t, dbFile, "focus", inbox, "--as", "tester"); err != nil {
		t.Fatalf("focus: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "issue", "A defect", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	show := mustShow(t, dbFile, newIssueID(out))
	if !strings.Contains(show, inbox) {
		t.Fatalf("show = %q, want the focused issue root %s as parent", show, inbox)
	}
}

func TestIssueCmd_AmbiguousIssueRootsFailAndNameBoth(t *testing.T) {
	dbFile := setupCLI(t)
	bugs := addIssueRoot(t, dbFile, "Bugs")
	inbox := addIssueRoot(t, dbFile, "Inbox")
	before := countTaskRows(t, dbFile)

	_, _, err := runCLI(t, dbFile, "issue", "A defect", "--as", "tester")
	if err == nil {
		t.Fatal("issue: want a non-nil error with two issue roots")
	}
	msg := err.Error()
	for _, want := range []string{bugs, "Bugs", inbox, "Inbox", "job focus <id>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}
	if got := countTaskRows(t, dbFile); got != before {
		t.Fatalf("task rows = %d, want %d — a failed resolve must create nothing", got, before)
	}
}

func TestIssueCmd_NoIssueRootFailsAndNamesAddKindIssue(t *testing.T) {
	dbFile := setupCLI(t)
	addTask(t, dbFile, "", "A plan")

	_, _, err := runCLI(t, dbFile, "issue", "A defect", "--as", "tester")
	if err == nil {
		t.Fatal("issue: want a non-nil error with no issue root")
	}
	if !strings.Contains(err.Error(), "job add <title> --kind issue") {
		t.Fatalf("error = %q, want it to name `job add <title> --kind issue`", err)
	}
}

func TestIssueCmd_DefaultsFoundInToTheSoleLiveClaim(t *testing.T) {
	dbFile := setupCLI(t)
	plan := addTask(t, dbFile, "", "A plan")
	leaf := addTask(t, dbFile, plan, "Wire the router")
	addIssueRoot(t, dbFile, "Bugs")
	if _, _, err := runCLI(t, dbFile, "claim", leaf, "--as", "tester"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "issue", "Router drops trailing slash", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.Contains(out, "Found in:") || !strings.Contains(out, leaf) {
		t.Fatalf("issue output = %q, want a Found in: acknowledgement naming %s", out, leaf)
	}
	show := mustShow(t, dbFile, newIssueID(out))
	if !strings.Contains(show, "Found in:") || !strings.Contains(show, leaf) {
		t.Fatalf("show = %q, want a Found in: line naming %s", show, leaf)
	}
}

func TestIssueCmd_NoClaimRecordsNoEdge(t *testing.T) {
	dbFile := setupCLI(t)
	addIssueRoot(t, dbFile, "Bugs")

	out, _, err := runCLI(t, dbFile, "issue", "A defect", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if strings.Contains(out, "Found in:") {
		t.Fatalf("issue output = %q, want no Found in: line with no live claim", out)
	}
	if show := mustShow(t, dbFile, newIssueID(out)); strings.Contains(show, "Found in:") {
		t.Fatalf("show = %q, want no Found in: line", show)
	}
}

func TestIssueCmd_SeveralClaimsRecordNoEdgeAndHint(t *testing.T) {
	dbFile := setupCLI(t)
	first := addTask(t, dbFile, "", "First leaf")
	second := addTask(t, dbFile, "", "Second leaf")
	addIssueRoot(t, dbFile, "Bugs")
	for _, id := range []string{first, second} {
		if _, _, err := runCLI(t, dbFile, "claim", id, "--as", "tester"); err != nil {
			t.Fatalf("claim %s: %v", id, err)
		}
	}

	out, _, err := runCLI(t, dbFile, "issue", "A defect", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.Contains(out, "--found-in") {
		t.Fatalf("issue output = %q, want a hint naming --found-in with several live claims", out)
	}
	if show := mustShow(t, dbFile, newIssueID(out)); strings.Contains(show, "Found in:") {
		t.Fatalf("show = %q, want no Found in: line with several live claims", show)
	}
}

func TestIssueCmd_FoundInNoneSuppressesTheDefault(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "A leaf")
	addIssueRoot(t, dbFile, "Bugs")
	if _, _, err := runCLI(t, dbFile, "claim", leaf, "--as", "tester"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "issue", "A defect", "--found-in", "none", "--as", "tester")
	if err != nil {
		t.Fatalf("issue --found-in none: %v", err)
	}
	if strings.Contains(out, "Found in:") {
		t.Fatalf("issue output = %q, want no Found in: line under --found-in none", out)
	}
	if show := mustShow(t, dbFile, newIssueID(out)); strings.Contains(show, "Found in:") {
		t.Fatalf("show = %q, want no Found in: line under --found-in none", show)
	}
}

func TestIssueCmd_FoundInOverridesTheDefault(t *testing.T) {
	dbFile := setupCLI(t)
	claimed := addTask(t, dbFile, "", "The claimed leaf")
	other := addTask(t, dbFile, "", "Some other leaf")
	addIssueRoot(t, dbFile, "Bugs")
	if _, _, err := runCLI(t, dbFile, "claim", claimed, "--as", "tester"); err != nil {
		t.Fatalf("claim: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "issue", "A defect", "--found-in", other, "--as", "tester")
	if err != nil {
		t.Fatalf("issue --found-in: %v", err)
	}
	show := mustShow(t, dbFile, newIssueID(out))
	if !strings.Contains(show, other) {
		t.Fatalf("show = %q, want the overriding source %s", show, other)
	}
	if strings.Contains(show, claimed) {
		t.Fatalf("show = %q, want the claim %s not to win over --found-in", show, claimed)
	}
}

func TestIssueCmd_UnknownFoundInSourceCreatesNothing(t *testing.T) {
	dbFile := setupCLI(t)
	addIssueRoot(t, dbFile, "Bugs")
	before := countTaskRows(t, dbFile)

	if _, _, err := runCLI(t, dbFile, "issue", "A defect", "--found-in", "ZZZZZ", "--as", "tester"); err == nil {
		t.Fatal("issue --found-in ZZZZZ: want an error")
	}
	if got := countTaskRows(t, dbFile); got != before {
		t.Fatalf("task rows = %d, want %d — a mistyped source must leave no task behind", got, before)
	}
}

func TestIssueCmd_BodyFlagsAndLabelsMatchAdd(t *testing.T) {
	dbFile := setupCLI(t)
	addIssueRoot(t, dbFile, "Bugs")

	out, _, err := runCLI(t, dbFile, "issue", "Inline body", "--desc", "Panics on an empty tree",
		"--label", "cli", "--as", "tester")
	if err != nil {
		t.Fatalf("issue --desc: %v", err)
	}
	show := mustShow(t, dbFile, newIssueID(out))
	if !strings.Contains(show, "Panics on an empty tree") {
		t.Fatalf("show = %q, want the --desc body", show)
	}
	if !strings.Contains(show, "cli") {
		t.Fatalf("show = %q, want the label", show)
	}

	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte("Body read from a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runCLI(t, dbFile, "issue", "File body", "-F", path, "--as", "tester")
	if err != nil {
		t.Fatalf("issue -F: %v", err)
	}
	if show := mustShow(t, dbFile, newIssueID(out)); !strings.Contains(show, "Body read from a file") {
		t.Fatalf("show = %q, want the -F body", show)
	}

	out, _, err = runCLIWithStdin(t, dbFile, "Body read from stdin",
		"issue", "Stdin body", "-F", "-", "--as", "tester")
	if err != nil {
		t.Fatalf("issue -F -: %v", err)
	}
	if show := mustShow(t, dbFile, newIssueID(out)); !strings.Contains(show, "Body read from stdin") {
		t.Fatalf("show = %q, want the stdin body", show)
	}
}

// An issue root never auto-closes, so the child-count advisory that ends
// "complete them all to auto-close the parent" would be false under one.
func TestIssueCmd_NoAutoCloseAdvisoryUnderIssueRoot(t *testing.T) {
	dbFile := setupCLI(t)
	bugs := addIssueRoot(t, dbFile, "Bugs")
	if _, _, err := runCLI(t, dbFile, "issue", "first", "--as", "tester"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	out, _, err := runCLI(t, dbFile, "issue", "second", "--as", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if strings.Contains(out, "auto-close") {
		t.Fatalf("issue printed the auto-close advisory under issue root %s:\n%s", bugs, out)
	}
	out, _, err = runCLI(t, dbFile, "add", bugs, "third", "--as", "tester")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.Contains(out, "auto-close") {
		t.Fatalf("add printed the auto-close advisory under issue root %s:\n%s", bugs, out)
	}
}
