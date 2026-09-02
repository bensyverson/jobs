package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// W7XoA / 4GMzO — `job focus` CLI: one focused root per tree kind. Bare
// `focus` prints a Task: line and an Issues: line (`(none)` where unset);
// `focus <root>` sets the focus for that root's kind; `--release` clears both
// and `--release --issues` clears only the issue focus.

// focusLine returns the `focus` output line for one kind's label, so tests
// read the line rather than the padding that aligns the two labels.
func focusLine(t *testing.T, out, label string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, label+":") {
			return line
		}
	}
	t.Fatalf("no %s: line in focus output:\n%s", label, out)
	return ""
}

// mustRunCLI runs a command that is expected to succeed.
func mustRunCLI(t *testing.T, dbFile string, args ...string) string {
	t.Helper()
	out, errOut, err := runCLI(t, dbFile, args...)
	if err != nil {
		t.Fatalf("job %s: %v\n%s%s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

// 1h3 — bare focus prints both kinds, with (none) where unset.
func TestFocus_PrintsATaskLineAndAnIssuesLine(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "The active tree")
	leaf1 := job.MustAdd(t, db, root, "Leaf 1")
	job.MustAdd(t, db, root, "Leaf 2")
	bugs := job.MustAdd(t, db, "", "Bugs")
	job.MustAdd(t, db, bugs, "A bug")
	db.Close()
	mustRunCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue")

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if !strings.Contains(out, "Task:") || !strings.Contains(out, "Issues:") {
		t.Fatalf("focus must print a Task: line and an Issues: line:\n%s", out)
	}
	if strings.Count(out, "(none)") != 2 {
		t.Errorf("both lines must read (none) when nothing is focused:\n%s", out)
	}

	mustRunCLI(t, dbFile, "--as", "alice", "claim", leaf1, "1h")

	out = mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if !strings.Contains(out, root) || !strings.Contains(out, "The active tree") {
		t.Errorf("the Task: line must name the focused root and title:\n%s", out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("a focused line must carry an availability summary:\n%s", out)
	}
	if !strings.Contains(focusLine(t, out, "Issues"), "(none)") {
		t.Errorf("claiming in a task tree must leave the issue focus unset:\n%s", out)
	}
}

// 9Mh — the two kinds are independent slots, set by claiming in either tree.
func TestFocus_ClaimingInAnIssueTreeSetsOnlyTheIssueLine(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Plan")
	taskLeaf := job.MustAdd(t, db, root, "Plan leaf")
	bugs := job.MustAdd(t, db, "", "Bugs")
	bugLeaf := job.MustAdd(t, db, bugs, "A bug")
	db.Close()
	mustRunCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue")

	mustRunCLI(t, dbFile, "--as", "alice", "claim", taskLeaf, "1h")
	mustRunCLI(t, dbFile, "--as", "alice", "claim", bugLeaf, "1h")

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if !strings.Contains(out, "Task:") || !strings.Contains(out, root) {
		t.Errorf("the task focus must survive a claim in the issue tree:\n%s", out)
	}
	if !strings.Contains(out, "Issues:") || !strings.Contains(out, bugs) {
		t.Errorf("claiming in the issue tree must set the issue focus:\n%s", out)
	}
}

// `job focus <root>` is the setter, and the root's kind decides the slot.
func TestFocus_SetterUsesTheRootsKind(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	bugs := job.MustAdd(t, db, "", "Bugs")
	bugLeaf := job.MustAdd(t, db, bugs, "A bug")
	other := job.MustAdd(t, db, "", "Other bugs")
	job.MustAdd(t, db, other, "Another bug")
	db.Close()
	mustRunCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue")
	mustRunCLI(t, dbFile, "--as", "alice", "kind", other, "issue")

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus", bugs)
	if !strings.Contains(out, bugs) || !strings.Contains(out, "issue-tree") {
		t.Errorf("setting an issue root must confirm the kind it focused:\n%s", out)
	}

	out = mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if !strings.Contains(focusLine(t, out, "Task"), "(none)") {
		t.Errorf("focus on an issue root must leave the task focus unset:\n%s", out)
	}
	if !strings.Contains(focusLine(t, out, "Issues"), bugs) {
		t.Errorf("focus on an issue root must land on the Issues: line:\n%s", out)
	}

	// COY — and `next --issues` follows it instead of going forest-wide.
	out = mustRunCLI(t, dbFile, "--as", "alice", "next", "--issues")
	if !strings.Contains(out, bugLeaf) {
		t.Errorf("next --issues must stay inside the focused issue root:\n%s", out)
	}
}

// Setting a focus is a write, so it needs an identity.
func TestFocus_SetterRequiresAnIdentity(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Plan")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "focus", root); err == nil {
		t.Fatal("focus <root> without --as: want an identity error")
	}
}

// Focus is per-actor: another identity sees neither line set.
func TestFocus_PerActor(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	leaf := job.MustAdd(t, db, root, "Leaf")
	db.Close()

	mustRunCLI(t, dbFile, "--as", "alice", "claim", leaf, "1h")

	out := mustRunCLI(t, dbFile, "--as", "bob", "focus")
	if strings.Count(out, "(none)") != 2 {
		t.Errorf("bob must see no focus of either kind:\n%s", out)
	}
}

// ZKx — --release --issues clears only the issue focus.
func TestFocus_ReleaseIssuesLeavesTheTaskFocus(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Plan")
	taskLeaf := job.MustAdd(t, db, root, "Plan leaf")
	job.MustAdd(t, db, root, "Plan leaf 2")
	bugs := job.MustAdd(t, db, "", "Bugs")
	bugLeaf := job.MustAdd(t, db, bugs, "A bug")
	db.Close()
	mustRunCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue")

	mustRunCLI(t, dbFile, "--as", "alice", "claim", taskLeaf, "1h")
	mustRunCLI(t, dbFile, "--as", "alice", "claim", bugLeaf, "1h")

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus", "--release", "--issues")
	if !strings.Contains(out, bugs) {
		t.Errorf("the release confirmation must name the released root:\n%s", out)
	}
	if strings.Contains(out, root) {
		t.Errorf("--release --issues must not touch the task focus:\n%s", out)
	}

	out = mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if !strings.Contains(focusLine(t, out, "Issues"), "(none)") {
		t.Errorf("the issue focus must be gone:\n%s", out)
	}
	if !strings.Contains(out, root) {
		t.Errorf("the task focus must remain:\n%s", out)
	}
}

// Bare --release clears both kinds, and records no event: focus is local.
func TestFocus_ReleaseClearsBothKinds(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Plan")
	taskLeaf := job.MustAdd(t, db, root, "Plan leaf")
	job.MustAdd(t, db, root, "Plan leaf 2")
	bugs := job.MustAdd(t, db, "", "Bugs")
	bugLeaf := job.MustAdd(t, db, bugs, "A bug")
	db.Close()
	mustRunCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue")

	mustRunCLI(t, dbFile, "--as", "alice", "claim", taskLeaf, "1h")
	mustRunCLI(t, dbFile, "--as", "alice", "claim", bugLeaf, "1h")

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus", "--release")
	if !strings.Contains(out, root) || !strings.Contains(out, bugs) {
		t.Errorf("--release must name both released roots:\n%s", out)
	}

	out = mustRunCLI(t, dbFile, "--as", "alice", "focus")
	if strings.Count(out, "(none)") != 2 {
		t.Errorf("both focuses must be gone after --release:\n%s", out)
	}

	db = openTestDB(t, dbFile)
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type IN ('focus_set','focus_released') AND actor = 'alice'",
	).Scan(&n); err != nil {
		t.Fatalf("count focus events: %v", err)
	}
	if n != 0 {
		t.Errorf("focus events: got %d, want 0 (focus lives in local.json)", n)
	}
}

// Releasing with nothing set is a friendly no-op.
func TestFocus_ReleaseWithoutFocus_NoOp(t *testing.T) {
	dbFile := setupCLI(t)

	out := mustRunCLI(t, dbFile, "--as", "alice", "focus", "--release")
	if !strings.Contains(out, "No focus set") {
		t.Errorf("a no-op release should say there was nothing to release:\n%s", out)
	}
}

// --issues is a modifier for --release; alone it is a usage error rather
// than a silently ignored flag.
func TestFocus_IssuesWithoutRelease_IsAUsageError(t *testing.T) {
	dbFile := setupCLI(t)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "focus", "--issues"); err == nil {
		t.Fatal("focus --issues without --release: want a usage error")
	}
}
