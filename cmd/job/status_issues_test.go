package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// `job status` demotes issue-tree roots out of the per-root rollup into a
// single Issues: line (project/2026-08-28-issues-ux.md, decision 4). These
// tests drive the CLI end to end with seedKindCLI's mixed task/issue forest.

func TestStatus_IssueRootOmittedFromRollup_PrintsIssuesLine(t *testing.T) {
	dbFile, _, issueRoot, issueLeaf := seedKindCLI(t)

	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(stdout, issueRoot) {
		t.Errorf("issue root %q must not appear in the per-root rollup:\n%s", issueRoot, stdout)
	}
	want := "Issues: 1 open (0 claimed) · next " + issueLeaf
	if !strings.Contains(stdout, want) {
		t.Errorf("expected %q in:\n%s", want, stdout)
	}
}

func TestStatus_NoIssueRoots_NoIssuesLine(t *testing.T) {
	dbFile := setupCLI(t)
	mustAddCLI(t, dbFile, "", "Plan")

	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(stdout, "Issues:") {
		t.Errorf("should not print an Issues: line with no issue roots:\n%s", stdout)
	}
}

func TestStatus_IssuesLine_ClaimedScopedToAs(t *testing.T) {
	dbFile, _, _, issueLeaf := seedKindCLI(t)
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", issueLeaf); err != nil {
		t.Fatalf("claim: %v", err)
	}

	// Unscoped: counts the live claim.
	stdout, _, err := runCLI(t, dbFile, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(stdout, "Issues: 1 open (1 claimed)") {
		t.Errorf("expected the claim to count without --as:\n%s", stdout)
	}

	// Scoped to a different actor: the claim is not theirs.
	stdout, _, err = runCLI(t, dbFile, "--as", "bob", "status")
	if err != nil {
		t.Fatalf("status --as bob: %v", err)
	}
	if !strings.Contains(stdout, "Issues: 1 open (0 claimed)") {
		t.Errorf("expected bob's scoped Issues line to show 0 claimed:\n%s", stdout)
	}
}

func TestStatus_JSON_IssuesObject(t *testing.T) {
	dbFile, _, _, issueLeaf := seedKindCLI(t)

	stdout, _, err := runCLI(t, dbFile, "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON:\n%s\nerr: %v", stdout, err)
	}
	issues, ok := got["issues"].(map[string]any)
	if !ok {
		t.Fatalf("issues should be an object; got %T %v", got["issues"], got["issues"])
	}
	if issues["open"].(float64) != 1 {
		t.Errorf("issues.open: got %v, want 1", issues["open"])
	}
	if issues["claimed"].(float64) != 0 {
		t.Errorf("issues.claimed: got %v, want 0", issues["claimed"])
	}
	next, ok := issues["next"].(map[string]any)
	if !ok {
		t.Fatalf("issues.next should be an object; got %T %v", issues["next"], issues["next"])
	}
	if next["short_id"] != issueLeaf {
		t.Errorf("issues.next.short_id: got %v, want %q", next["short_id"], issueLeaf)
	}
}

func TestStatus_JSON_IssuesNullWhenNoIssueRoots(t *testing.T) {
	dbFile := setupCLI(t)
	mustAddCLI(t, dbFile, "", "Plan")

	stdout, _, err := runCLI(t, dbFile, "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	key, present := got["issues"]
	if !present {
		t.Fatalf("issues key should be present (as null) when there are no issue roots")
	}
	if key != nil {
		t.Errorf("issues should be null when there are no issue roots; got %v", key)
	}
}
