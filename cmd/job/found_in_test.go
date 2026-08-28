package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFoundInCmd_SetsAndShowsBothEnds(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "Wire the router")
	bug := addTask(t, dbFile, "", "Router drops trailing slash")

	out, _, err := runCLI(t, dbFile, "found-in", bug, "in", leaf, "--as", "tester")
	if err != nil {
		t.Fatalf("found-in: %v", err)
	}
	if !strings.Contains(out, leaf) || !strings.Contains(out, bug) {
		t.Fatalf("found-in ack = %q, want both ids", out)
	}

	// The issue end names its source, with the source's status.
	showBug, _, err := runCLI(t, dbFile, "show", bug)
	if err != nil {
		t.Fatalf("show issue: %v", err)
	}
	if !strings.Contains(showBug, "Found in:") {
		t.Fatalf("show(issue) = %q, want a Found in: line", showBug)
	}
	if !strings.Contains(showBug, leaf) || !strings.Contains(showBug, "Wire the router") {
		t.Fatalf("show(issue) = %q, want the source id and title", showBug)
	}
	if !strings.Contains(showBug, "(available)") {
		t.Fatalf("show(issue) = %q, want the source status in parentheses", showBug)
	}

	// The source end names what it surfaced.
	showLeaf, _, err := runCLI(t, dbFile, "show", leaf)
	if err != nil {
		t.Fatalf("show source: %v", err)
	}
	if !strings.Contains(showLeaf, "Surfaced:") {
		t.Fatalf("show(source) = %q, want a Surfaced: line", showLeaf)
	}
	if !strings.Contains(showLeaf, bug) || !strings.Contains(showLeaf, "Router drops trailing slash") {
		t.Fatalf("show(source) = %q, want the issue id and title", showLeaf)
	}
	// And no blocking language leaked in either direction.
	if strings.Contains(showLeaf, "Blocks:") || strings.Contains(showBug, "Blocked by:") {
		t.Fatalf("found-in rendered as a block: issue=%q source=%q", showBug, showLeaf)
	}
}

func TestFoundInCmd_ShowOmitsTheLinesWhenUnset(t *testing.T) {
	dbFile := setupCLI(t)
	plain := addTask(t, dbFile, "", "Just a task")
	out, _, err := runCLI(t, dbFile, "show", plain)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(out, "Found in:") || strings.Contains(out, "Surfaced:") {
		t.Fatalf("show = %q, want neither line on a task with no found-in edges", out)
	}
}

func TestFoundInCmd_Clear(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "A leaf")
	bug := addTask(t, dbFile, "", "A defect")
	if _, _, err := runCLI(t, dbFile, "found-in", bug, "in", leaf, "--as", "tester"); err != nil {
		t.Fatalf("found-in: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "found-in", bug, "--clear", "--as", "tester")
	if err != nil {
		t.Fatalf("found-in --clear: %v", err)
	}
	if !strings.Contains(out, bug) {
		t.Fatalf("clear ack = %q, want the task id", out)
	}

	show, _, err := runCLI(t, dbFile, "show", bug)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(show, "Found in:") {
		t.Fatalf("show = %q, want no Found in: line after --clear", show)
	}
}

func TestFoundInCmd_SelfReferenceRejected(t *testing.T) {
	dbFile := setupCLI(t)
	bug := addTask(t, dbFile, "", "A defect")
	if _, _, err := runCLI(t, dbFile, "found-in", bug, "in", bug, "--as", "tester"); err == nil {
		t.Fatal("expected an error setting a task's found-in source to itself")
	}
}

func TestFoundInCmd_UsageErrors(t *testing.T) {
	dbFile := setupCLI(t)
	a := addTask(t, dbFile, "", "A")
	b := addTask(t, dbFile, "", "B")

	if _, _, err := runCLI(t, dbFile, "found-in", a, "at", b, "--as", "tester"); err == nil {
		t.Fatal("expected a usage error for the wrong preposition")
	}
	if _, _, err := runCLI(t, dbFile, "found-in", a, "--as", "tester"); err == nil {
		t.Fatal("expected a usage error for a bare task with no source and no --clear")
	}
	if _, _, err := runCLI(t, dbFile, "found-in", a, "in", b, "--clear", "--as", "tester"); err == nil {
		t.Fatal("expected an error combining a source with --clear")
	}
}

func TestAddCmd_FoundInFlag(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "Wire the router")
	issues := addTask(t, dbFile, "", "Issues")

	out, _, err := runCLI(t, dbFile, "add", issues, "Router drops trailing slash",
		"--found-in", leaf, "--as", "tester")
	if err != nil {
		t.Fatalf("add --found-in: %v", err)
	}
	bug := strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])

	show, _, err := runCLI(t, dbFile, "show", bug)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(show, "Found in:") || !strings.Contains(show, leaf) {
		t.Fatalf("show = %q, want a Found in: line naming %s", show, leaf)
	}
	// Parented by the issues root, not by the source.
	if !strings.Contains(show, "Parent:       "+issues) {
		t.Fatalf("show = %q, want the parent to be %s", show, issues)
	}
}

func TestAddCmd_FoundInUnknownSourceFailsBeforeCreating(t *testing.T) {
	dbFile := setupCLI(t)
	before := countTaskRows(t, dbFile)
	if _, _, err := runCLI(t, dbFile, "add", "A defect", "--parent", "",
		"--found-in", "zzzzz", "--as", "tester"); err == nil {
		t.Fatal("expected an error for an unknown --found-in source")
	}
	if after := countTaskRows(t, dbFile); after != before {
		t.Fatalf("task count went %d → %d; a rejected --found-in must not create a task", before, after)
	}
}

func TestFoundInCmd_ShowJSONCarriesBothEnds(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "A leaf")
	bug := addTask(t, dbFile, "", "A defect")
	if _, _, err := runCLI(t, dbFile, "found-in", bug, "in", leaf, "--as", "tester"); err != nil {
		t.Fatalf("found-in: %v", err)
	}

	out, _, err := runCLI(t, dbFile, "show", bug, "--format", "json")
	if err != nil {
		t.Fatalf("show --format json: %v", err)
	}
	var got []struct {
		FoundIn  string   `json:"found_in"`
		Surfaced []string `json:"surfaced"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(got) != 1 || got[0].FoundIn != leaf {
		t.Fatalf("json found_in = %+v, want %s", got, leaf)
	}

	srcOut, _, err := runCLI(t, dbFile, "show", leaf, "--format", "json")
	if err != nil {
		t.Fatalf("show source --format json: %v", err)
	}
	var srcGot []struct {
		FoundIn  string   `json:"found_in"`
		Surfaced []string `json:"surfaced"`
	}
	if err := json.Unmarshal([]byte(srcOut), &srcGot); err != nil {
		t.Fatalf("unmarshal %q: %v", srcOut, err)
	}
	if len(srcGot) != 1 || len(srcGot[0].Surfaced) != 1 || srcGot[0].Surfaced[0] != bug {
		t.Fatalf("json surfaced = %+v, want [%s]", srcGot, bug)
	}
}

func TestFoundInCmd_SourceStaysClaimableAndClosable(t *testing.T) {
	dbFile := setupCLI(t)
	leaf := addTask(t, dbFile, "", "A leaf")
	bug := addTask(t, dbFile, "", "A defect")
	if _, _, err := runCLI(t, dbFile, "found-in", bug, "in", leaf, "--as", "tester"); err != nil {
		t.Fatalf("found-in: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "claim", leaf, "--as", "tester"); err != nil {
		t.Fatalf("claim the source while the issue is open: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "done", leaf, "--as", "tester"); err != nil {
		t.Fatalf("close the source while the issue is open: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "claim", bug, "--as", "tester"); err != nil {
		t.Fatalf("claim the issue after its source closed: %v", err)
	}

	// The reference is still readable with the source closed.
	show, _, err := runCLI(t, dbFile, "show", bug)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(show, "Found in:") || !strings.Contains(show, "(done)") {
		t.Fatalf("show = %q, want the closed source still named with its status", show)
	}
}

// addTask creates a task through the CLI and returns its short id.
func addTask(t *testing.T, dbFile, parent, title string) string {
	t.Helper()
	args := []string{"add"}
	if parent != "" {
		args = append(args, parent)
	} else {
		args = append(args, "--parent", "")
	}
	args = append(args, title, "--id-only", "--as", "tester")
	out, _, err := runCLI(t, dbFile, args...)
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return strings.TrimSpace(out)
}

func countTaskRows(t *testing.T, dbFile string) int {
	t.Helper()
	db := openTestDB(t, dbFile)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}
