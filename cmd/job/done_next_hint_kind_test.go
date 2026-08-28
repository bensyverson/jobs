package main

import (
	"strings"
	"testing"
)

// End-to-end guard for the done ack's trailing "Next:" hint: it must not
// cross into an issue-tree, the same default `job next` and `job status` use.

func TestDone_NextHint_DoesNotNameIssueTreeLeaf(t *testing.T) {
	dbFile := setupCLI(t)
	mustID := func(args ...string) string {
		t.Helper()
		out, _, err := runCLI(t, dbFile, append([]string{"--as", "alice"}, args...)...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return strings.TrimSpace(out)
	}
	plan := mustID("add", "Plan", "--id-only")
	leaf := mustID("add", plan, "Only task leaf", "--id-only")
	bugs := mustID("add", "Bugs", "--kind", "issue", "--id-only")
	issueLeaf := mustID("add", bugs, "Issue leaf", "--id-only")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if strings.Contains(stdout, "Next:") {
		t.Errorf("done ack should have no Next: hint when only issue-tree work remains:\n%s", stdout)
	}
	if strings.Contains(stdout, issueLeaf) {
		t.Errorf("done ack named the issue-tree leaf %s:\n%s", issueLeaf, stdout)
	}
}
