package main

import (
	"encoding/json"
	job "github.com/bensyverson/jobs/internal/job"
	"strings"
	"testing"
)

func TestNextAll_ReturnsAllAvailable(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	c := job.MustAdd(t, db, "", "C")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	for _, id := range []string{a, b, c} {
		if !strings.Contains(stdout, "- "+id+" ") {
			t.Errorf("missing %s:\n%s", id, stdout)
		}
	}
}

func TestNextAll_EmptyIsNotError(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all on empty db should not error: %v", err)
	}
	if !strings.Contains(stdout, "No available tasks.") {
		t.Errorf("expected friendly empty message:\n%s", stdout)
	}
}

func TestNextAll_EmptyJSON(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "next", "all", "--format=json")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	var got []any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(got) != 0 {
		t.Errorf("expected empty array, got %v", got)
	}
}

func TestNextAll_ScopesToParent_Either_Order(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	other := job.MustAdd(t, db, "", "Other")
	db.Close()

	for _, args := range [][]string{
		{"next", parent, "all"},
		{"next", "all", parent},
	} {
		stdout, _, err := runCLI(t, dbFile, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, id := range []string{c1, c2} {
			if !strings.Contains(stdout, "- "+id+" ") {
				t.Errorf("%v: missing %s:\n%s", args, id, stdout)
			}
		}
		if strings.Contains(stdout, "- "+other+" ") {
			t.Errorf("%v: should not include sibling-of-parent %s:\n%s", args, other, stdout)
		}
	}
}

func TestNextAll_ExcludesBlocked(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	if err := job.RunBlock(db, b, a, job.TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	if strings.Contains(stdout, b) {
		t.Errorf("blocked task %s should not appear:\n%s", b, stdout)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("blocker %s should appear:\n%s", a, stdout)
	}
}

func TestNextAll_ExcludesClaimed(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	if strings.Contains(stdout, "- "+a+" ") {
		t.Errorf("claimed task should not appear:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- "+b+" ") {
		t.Errorf("available task should appear:\n%s", stdout)
	}
}

func TestNextAll_Md_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	want := "- " + a + " \"Alpha\"\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestNextAll_Json_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alpha")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all", "--format=json")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 task, got %d", len(got))
	}
	if got[0]["id"] != a {
		t.Errorf("id: got %v, want %s", got[0]["id"], a)
	}
	if got[0]["title"] != "Alpha" {
		t.Errorf("title: got %v", got[0]["title"])
	}
}

// --- P1a: leaf-frontier semantics ---------------------------------------

// A task is "claimable" iff it has no open children. `next all` without a
// parent should surface the leaf frontier of the entire tree, descending
// through parents rather than surfacing them.
func TestNextAll_ExcludesParentsWithOpenChildren(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "Child 1")
	c2 := job.MustAdd(t, db, parent, "Child 2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	if strings.Contains(stdout, "- "+parent+" ") {
		t.Errorf("parent with open children should not appear in leaf frontier:\n%s", stdout)
	}
	for _, id := range []string{c1, c2} {
		if !strings.Contains(stdout, "- "+id+" ") {
			t.Errorf("leaf %s should appear:\n%s", id, stdout)
		}
	}
}

// `next` (singular) also obeys the leaf-frontier rule.
func TestNext_ExcludesParentsWithOpenChildren(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "Child 1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if strings.Contains(stdout, parent) {
		t.Errorf("parent with open children should not be next:\n%s", stdout)
	}
	if !strings.Contains(stdout, c1) {
		t.Errorf("leaf %s should be next:\n%s", c1, stdout)
	}
}

// `claim --next` should claim a leaf, never a parent with open children.
func TestClaimNext_PrefersLeafOverParent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "Child 1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next")
	if err != nil {
		t.Fatalf("claim --next: %v", err)
	}
	// The first line is the scriptable ack ("Claimed: <id> ...") and
	// names which task got claimed. Full stdout also contains the
	// briefing, which legitimately names the parent on a `Parent:` line
	// — so substring-matching the whole buffer for the parent ID is the
	// wrong check after briefing was added.
	first := firstLine(stdout)
	if strings.Contains(first, parent) {
		t.Errorf("claim --next should not claim parent (first line names the claim target):\n%s", first)
	}
	if !strings.Contains(first, c1) {
		t.Errorf("claim --next should claim leaf %s:\n%s", c1, first)
	}
}

// `next all` descends through nested parents to return deep leaves.
func TestNextAll_DescendsIntoNestedLeaves(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	grand := job.MustAdd(t, db, "", "Grandparent")
	parent := job.MustAdd(t, db, grand, "Parent")
	leaf := job.MustAdd(t, db, parent, "Leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all")
	if err != nil {
		t.Fatalf("next all: %v", err)
	}
	if !strings.Contains(stdout, "- "+leaf+" ") {
		t.Errorf("deep leaf %s should appear:\n%s", leaf, stdout)
	}
	for _, id := range []string{grand, parent} {
		if strings.Contains(stdout, "- "+id+" ") {
			t.Errorf("non-leaf %s should not appear:\n%s", id, stdout)
		}
	}
}

// Obsolete under P4 (cancel cascade). Previously tested that a parent
// with only canceled children was still reachable as a frontier leaf.
// Under the symmetric cancel cascade, canceling the last open child of
// a parent auto-cancels the parent too — so the state this test set up
// (canceled child, open parent) is no longer possible. P4's new
// TestCancel_Cascade_MultiLevel exercises the replacement behaviour.

// `--include-parents` restores the pre-leaf-frontier behavior: tasks with
// open children are surfaced as they were before.
func TestNextAll_IncludeParentsFlagSurfacesParents(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, parent, "Child 1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all", "--include-parents")
	if err != nil {
		t.Fatalf("next all --include-parents: %v", err)
	}
	if !strings.Contains(stdout, "- "+parent+" ") {
		t.Errorf("--include-parents should surface parent:\n%s", stdout)
	}
}

// When a parent arg is provided, `next all <parent>` returns leaf
// descendants of that parent, not just direct children.
func TestNextAll_ParentScoped_ReturnsLeafDescendants(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	mid := job.MustAdd(t, db, root, "Mid")
	leaf := job.MustAdd(t, db, mid, "Leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "next", "all", root)
	if err != nil {
		t.Fatalf("next all %s: %v", root, err)
	}
	if !strings.Contains(stdout, "- "+leaf+" ") {
		t.Errorf("deep leaf %s should appear under scope %s:\n%s", leaf, root, stdout)
	}
	if strings.Contains(stdout, "- "+mid+" ") {
		t.Errorf("non-leaf %s should not appear:\n%s", mid, stdout)
	}
}

// --- P1b: claim refuses parents with open children ----------------------

// Claiming a parent with open children should be refused with an error
// message that names the alternative (claim a leaf instead). The lock
// semantics of a claim have no coherent meaning on a task whose actual
// work is in its descendants.
func TestClaim_RefusesParentWithOpenChildren(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, parent, "Child 1")
	_ = job.MustAdd(t, db, parent, "Child 2")

	err := job.RunClaim(db, parent, "1h", "", "alice", false)
	if err == nil {
		t.Fatal("expected claim of parent-with-open-children to error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "open children") {
		t.Errorf("error should mention open children: %v", err)
	}
	if !strings.Contains(msg, "leaf") {
		t.Errorf("error should name the alternative (leaf): %v", err)
	}
}

// The refusal includes a count so the user understands scope.
func TestClaim_RefusesParentWithOpenChildren_IncludesCount(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	for range 3 {
		_ = job.MustAdd(t, db, parent, "Child")
	}

	err := job.RunClaim(db, parent, "1h", "", "alice", false)
	if err == nil {
		t.Fatal("expected claim to error")
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error should name count of open children (3): %v", err)
	}
}

// Claiming a leaf (no children) still works.
func TestClaim_LeafStillWorks(t *testing.T) {
	db := job.SetupTestDB(t)
	leaf := job.MustAdd(t, db, "", "Leaf")
	if err := job.RunClaim(db, leaf, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim leaf: %v", err)
	}
}

// Obsolete under P4 (cancel cascade). Previously tested that a parent
// with only closed children (done or canceled) was still claimable.
// Under the symmetric cancel cascade, closing the last open child of a
// parent auto-closes the parent as well — so a parent whose last child
// just closed is never in the "still open" state this test relied on.

// --force on a non-claim conflict (i.e., parent-with-open-children) should
// NOT override the structural refusal — --force is for stealing another
// agent's claim, not for bypassing leaf-frontier semantics. A parent with
// open children has no executable work, so forcing has no referent.
func TestClaim_RefusesParentEvenWithForce(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, parent, "Child")

	err := job.RunClaim(db, parent, "1h", "", "alice", true)
	if err == nil {
		t.Fatal("expected --force to not bypass parent-with-open-children refusal")
	}
	if !strings.Contains(err.Error(), "open children") {
		t.Errorf("error should mention open children: %v", err)
	}
}

// --- P1c: parent auto-close on last-child-done cascade ------------------

// Closing the last open child of a parent auto-closes the parent.
func TestDone_LastOpenChildAutoClosesParent(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Only Child")

	if _, _, err := job.RunDone(db, []string{child}, false, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done child: %v", err)
	}

	p := job.MustGet(t, db, parent)
	if p.Status != "done" {
		t.Errorf("parent should auto-close; got status %q", p.Status)
	}
}

// Auto-close cascades upward through the entire ancestor chain.
func TestDone_AutoCloseCascadesUpTree(t *testing.T) {
	db := job.SetupTestDB(t)
	grand := job.MustAdd(t, db, "", "Grand")
	parent := job.MustAdd(t, db, grand, "Parent")
	leaf := job.MustAdd(t, db, parent, "Leaf")

	if _, _, err := job.RunDone(db, []string{leaf}, false, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done leaf: %v", err)
	}

	for _, id := range []string{parent, grand} {
		task := job.MustGet(t, db, id)
		if task.Status != "done" {
			t.Errorf("ancestor %s should auto-close; got status %q", id, task.Status)
		}
	}
}

// Canceled siblings do not count as open, so they don't block cascade.
func TestDone_CanceledSiblingsDoNotBlockAutoClose(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	canceled := job.MustAdd(t, db, parent, "Canceled")
	open := job.MustAdd(t, db, parent, "Open")
	if _, _, _, err := job.RunCancel(db, []string{canceled}, "obsolete", false, false, false, job.TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, _, err := job.RunDone(db, []string{open}, false, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done open: %v", err)
	}

	p := job.MustGet(t, db, parent)
	if p.Status != "done" {
		t.Errorf("parent should auto-close despite canceled sibling; got status %q", p.Status)
	}
}

// A parent with siblings still open must NOT auto-close.
func TestDone_DoesNotAutoCloseWhenSiblingOpen(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	_ = job.MustAdd(t, db, parent, "C2") // still open

	if _, _, err := job.RunDone(db, []string{c1}, false, "", nil, job.TestActor, false, ""); err != nil {
		t.Fatalf("done c1: %v", err)
	}

	p := job.MustGet(t, db, parent)
	if p.Status == "done" {
		t.Errorf("parent should not auto-close while C2 is still open")
	}
}

// The CLI done ack surfaces the cascade so the user sees what happened.
func TestDone_CLIOutput_ShowsAutoCloseCascade(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	grand := job.MustAdd(t, db, "", "Grand")
	parent := job.MustAdd(t, db, grand, "Parent")
	leaf := job.MustAdd(t, db, parent, "Leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", leaf)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "Auto-closed") {
		t.Errorf("ack should mention auto-close:\n%s", stdout)
	}
	for _, id := range []string{parent, grand} {
		if !strings.Contains(stdout, id) {
			t.Errorf("ack should name auto-closed ancestor %s:\n%s", id, stdout)
		}
	}
}

// --- P1d: parent claim auto-release on first open child ----------------

// Adding an open child to a claimed parent releases the claim. The parent
// now has no executable work of its own — its work is in the new child
// (plus whatever the agent decomposes next), so the lock has no referent.
func TestAdd_ChildToClaimedParent_AutoReleasesClaim(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	if err := job.RunClaim(db, parent, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := job.RunAdd(db, parent, "Child", "", "", nil, "alice"); err != nil {
		t.Fatalf("add child: %v", err)
	}

	p := job.MustGet(t, db, parent)
	if p.Status != "available" {
		t.Errorf("parent should be released to available; got status %q", p.Status)
	}
	if p.ClaimedBy != nil {
		t.Errorf("parent claimed_by should be nil; got %v", *p.ClaimedBy)
	}
	if p.ClaimExpiresAt != nil {
		t.Errorf("parent claim_expires_at should be nil; got %v", *p.ClaimExpiresAt)
	}
}

// The add ack mentions the release so the agent sees it happened.
func TestAdd_CLIOutput_ShowsAutoRelease(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	if err := job.RunClaim(db, parent, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", parent, "Child")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(stdout, "Released") {
		t.Errorf("add ack should mention release of parent claim:\n%s", stdout)
	}
	if !strings.Contains(stdout, parent) {
		t.Errorf("add ack should name released parent %s:\n%s", parent, stdout)
	}
}

// Adding a child to an unclaimed parent is a no-op on the claim field.
func TestAdd_ChildToUnclaimedParent_NoRelease(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")

	if _, err := job.RunAdd(db, parent, "Child", "", "", nil, "alice"); err != nil {
		t.Fatalf("add child: %v", err)
	}

	p := job.MustGet(t, db, parent)
	if p.Status != "available" {
		t.Errorf("unclaimed parent status should remain 'available'; got %q", p.Status)
	}
	if p.ClaimedBy != nil {
		t.Errorf("parent should stay unclaimed")
	}
}

// Adding a SECOND child to a parent that was auto-released on the first
// child (and never re-claimed) is idempotent — no events spammed, status
// stays available.
func TestAdd_SecondChild_IsIdempotent(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	if err := job.RunClaim(db, parent, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := job.RunAdd(db, parent, "First", "", "", nil, "alice"); err != nil {
		t.Fatalf("add first: %v", err)
	}
	// Parent now auto-released. Adding a second child should not error or
	// try to re-release.
	if _, err := job.RunAdd(db, parent, "Second", "", "", nil, "alice"); err != nil {
		t.Fatalf("add second: %v", err)
	}

	// Count 'released' events on the parent — should be exactly one.
	p := job.MustGet(t, db, parent)
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'released'`,
		p.ID,
	).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 released event on parent, got %d", n)
	}
}

// The event log records an auto-release with detail marking the trigger.
func TestAdd_EventLog_RecordsAutoReleaseDetail(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	if err := job.RunClaim(db, parent, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if _, err := job.RunAdd(db, parent, "Child", "", "", nil, "alice"); err != nil {
		t.Fatalf("add: %v", err)
	}

	p := job.MustGet(t, db, parent)
	var detail string
	if err := db.QueryRow(
		`SELECT detail FROM events WHERE task_id = ? AND event_type = 'released' ORDER BY id DESC LIMIT 1`,
		p.ID,
	).Scan(&detail); err != nil {
		t.Fatalf("query event: %v", err)
	}
	if !strings.Contains(detail, "auto_released") {
		t.Errorf("released event detail should mark auto_released=true: %s", detail)
	}
}

// The event log records the auto-close with attribution to the closer
// of the last child, plus a detail flag marking it as automatic.
func TestDone_EventLog_RecordsAutoCloseAttribution(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child")

	if _, _, err := job.RunDone(db, []string{child}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done child: %v", err)
	}

	// Look up the parent's ID so we can query events directly.
	p := job.MustGet(t, db, parent)
	rows, err := db.Query(
		`SELECT actor, detail FROM events WHERE task_id = ? AND event_type = 'done'`,
		p.ID,
	)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var actor, detail string
		if err := rows.Scan(&actor, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if actor != "alice" {
			t.Errorf("event actor: got %q, want %q", actor, "alice")
		}
		if !strings.Contains(detail, "auto_closed") {
			t.Errorf("event detail should mark auto_closed=true: %s", detail)
		}
		found = true
	}
	if !found {
		t.Errorf("expected a 'done' event on auto-closed parent %s", parent)
	}
}
