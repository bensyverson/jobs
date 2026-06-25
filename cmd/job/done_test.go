package main

import (
	"encoding/json"
	job "github.com/bensyverson/jobs/internal/job"
	"os"
	"strings"
	"testing"
)

func TestDone_Variadic_Happy(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	c3 := job.MustAdd(t, db, parent, "C3")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1, c2, c3)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.HasPrefix(stdout, "Closed 3 tasks:\n") {
		t.Errorf("headline:\n%s", stdout)
	}
	for _, id := range []string{c1, c2, c3} {
		if !strings.Contains(stdout, "- Done: "+id) {
			t.Errorf("missing per-id line for %s:\n%s", id, stdout)
		}
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{c1, c2, c3} {
		task := job.MustGet(t, db, id)
		if task.Status != "done" {
			t.Errorf("%s: status=%q, want done", id, task.Status)
		}
	}
}

func TestDone_Variadic_AllOrNothing(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "noExs", b)
	if err == nil {
		t.Fatal("expected error with invalid id")
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{a, b} {
		task := job.MustGet(t, db, id)
		if task.Status == "done" {
			t.Errorf("%s was closed despite failure", id)
		}
	}
}

func TestDone_Cascade_ClosesDescendants(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	c1 := job.MustAdd(t, db, parent, "C1")
	gc := job.MustAdd(t, db, c1, "GC")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", parent, "--cascade")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "and 2 subtasks") {
		t.Errorf("missing cascade count:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{parent, c1, gc} {
		task := job.MustGet(t, db, id)
		if task.Status != "done" {
			t.Errorf("%s: status=%q, want done", id, task.Status)
		}
	}
}

func TestDone_Cascade_Variadic_Composes(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p1 := job.MustAdd(t, db, "", "P1")
	c1 := job.MustAdd(t, db, p1, "C1")
	p2 := job.MustAdd(t, db, "", "P2")
	c2 := job.MustAdd(t, db, p2, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", "--cascade", p1, p2)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.HasPrefix(stdout, "Closed 2 tasks:\n") {
		t.Errorf("headline:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	for _, id := range []string{p1, c1, p2, c2} {
		task := job.MustGet(t, db, id)
		if task.Status != "done" {
			t.Errorf("%s: status=%q, want done", id, task.Status)
		}
	}
}

func TestDone_WithoutCascade_OpenChildrenErrors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p := job.MustAdd(t, db, "", "Parent")
	_ = job.MustAdd(t, db, p, "Child")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", p)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--cascade "+p) {
		t.Errorf("missing --cascade hint: %v", err)
	}
}

func TestDone_ForceFlag_Gone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--force")
	if err == nil {
		t.Fatal("expected --force to be unknown")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("expected unknown flag error: %v", err)
	}
}

func TestDone_Note_Inline(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "shipped"); err != nil {
		t.Fatalf("done: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.CompletionNote == nil || *task.CompletionNote != "shipped" {
		t.Errorf("completion_note: got %v, want %q", task.CompletionNote, "shipped")
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "done")
	if detail["note"] != "shipped" {
		t.Errorf("event note: got %v", detail["note"])
	}
}

func TestDone_Result_Json(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--result", `{"k":1}`); err != nil {
		t.Fatalf("done: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	detail, _ := job.GetLatestEventDetail(db, task.ID, "done")
	result, ok := detail["result"].(map[string]any)
	if !ok {
		t.Fatalf("result: got %T, want map", detail["result"])
	}
	if result["k"] != float64(1) {
		t.Errorf("result[k]: got %v", result["k"])
	}
}

func TestDone_Result_InvalidJson(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--result", "not-json")
	if err == nil {
		t.Fatal("expected invalid JSON error")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("err: %v", err)
	}
}

func TestDone_NoteAndResult_Both(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "n", "--result", `{"x":2}`); err != nil {
		t.Fatalf("done: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.CompletionNote == nil || *task.CompletionNote != "n" {
		t.Errorf("note: %v", task.CompletionNote)
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "done")
	if detail["note"] != "n" {
		t.Errorf("event note: %v", detail["note"])
	}
	result, _ := detail["result"].(map[string]any)
	if result["x"] != float64(2) {
		t.Errorf("result[x]: %v", result)
	}
}

func TestDone_Note_Positional_Gone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	// `done <id> "some note"` is now treated as two ids; the second ("some note") is not a task.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "some note")
	if err == nil {
		t.Fatal("expected error: second positional arg is not a valid id")
	}
}

func TestDone_Single_Uses_Phase3_Render(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "P")
	c1 := job.MustAdd(t, db, parent, "C1")
	_ = job.MustAdd(t, db, parent, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.HasPrefix(stdout, "Done: "+c1+" \"C1\"") {
		t.Errorf("want Phase 3 single headline:\n%s", stdout)
	}
	if strings.Contains(stdout, "Closed ") {
		t.Errorf("single-ID call should not use multi-headline:\n%s", stdout)
	}
}

func TestDone_Multi_Md_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "P")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1, c2)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "Closed 2 tasks:") {
		t.Errorf("missing multi headline:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Done: "+c1) {
		t.Errorf("missing c1 line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Done: "+c2) {
		t.Errorf("missing c2 line:\n%s", stdout)
	}
}

func TestDone_Multi_Json_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "P")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1, c2, "--format=json")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	closed, ok := got["closed"].([]any)
	if !ok || len(closed) != 2 {
		t.Fatalf("closed: %v", got["closed"])
	}
	first := closed[0].(map[string]any)
	if first["id"] != c1 || first["title"] != "C1" {
		t.Errorf("closed[0]: %v", first)
	}
	if _, ok := got["already_done"]; !ok {
		t.Errorf("missing already_done key")
	}
}

func TestDone_AlreadyDone_InVariadic(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "P")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	job.MustDone(t, db, c1)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1, c2)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "already done: "+c1) {
		t.Errorf("missing already-done line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "- Done: "+c2) {
		t.Errorf("missing c2 close line:\n%s", stdout)
	}
}

func TestDone_EnrichedAck_MultiID_FinalContext(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "P")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1, c2)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	wantParent := "  Auto-closed: " + parent + " \"P\""
	if !strings.Contains(stdout, wantParent) {
		t.Errorf("missing auto-closed line:\n%s", stdout)
	}
}

func TestFormatEvent_LegacyForce_Renders(t *testing.T) {
	detailJSON := `{"force":true,"note":"","force_closed_children":["abc12"]}`
	out := job.FormatEventDescription("done", detailJSON)
	if !strings.Contains(out, "done --cascade") {
		t.Errorf("legacy force should render as done --cascade: %q", out)
	}
	if !strings.Contains(out, "and 1 subtasks") {
		t.Errorf("legacy force should surface cascade count: %q", out)
	}
}

// --- P2: done --claim-next ---------------------------------------------

// The flag closes the target AND claims the next available leaf in one
// call, collapsing the most common close-then-advance flow.
func TestDone_ClaimNext_ClosesAndClaims(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	b := job.MustAdd(t, db, root, "B")
	db.Close()

	// alice claims a, then done --claim-next: should close a, then claim b.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if !strings.Contains(stdout, "Done: "+a) {
		t.Errorf("ack should include Done line for closed task:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Claimed: "+b) {
		t.Errorf("ack should include Claimed line for next leaf:\n%s", stdout)
	}

	// Verify DB state: b is claimed by alice.
	db2 := openTestDB(t, dbFile)
	bt := job.MustGet(t, db2, b)
	if bt.Status != "claimed" || bt.ClaimedBy == nil || *bt.ClaimedBy != "alice" {
		t.Errorf("b should be claimed by alice; status=%q, claimed_by=%v", bt.Status, bt.ClaimedBy)
	}
}

// Output shape matches bare claim's ack: each line is greppable and the
// Claimed line starts with the literal "Claimed:" so tailing agents can
// use `^Claimed:` regardless of how the claim was acquired.
func TestDone_ClaimNext_OutputShapeMatchesClaim(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	_ = job.MustAdd(t, db, root, "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	lines := strings.Split(stdout, "\n")
	foundClaimed := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Claimed: ") {
			foundClaimed = true
			if !strings.Contains(line, "expires in") {
				t.Errorf("Claimed line should include expiry, got: %q", line)
			}
		}
	}
	if !foundClaimed {
		t.Errorf("expected a line starting with 'Claimed:' for greppability:\n%s", stdout)
	}
}

// When there is no next leaf (the closed task was the last work), the
// done succeeds and the ack simply omits the Claimed line (the existing
// "All tasks in X complete" ack still fires).
func TestDone_ClaimNext_NoNextLeaf_Succeeds(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next should not error when no next leaf: %v", err)
	}
	if !strings.Contains(stdout, "Done: "+a) {
		t.Errorf("ack should include Done line:\n%s", stdout)
	}
	if strings.Contains(stdout, "Claimed:") {
		t.Errorf("ack should NOT include Claimed line when no next leaf:\n%s", stdout)
	}
}

// If the next leaf got claimed by someone else between the done and the
// auto-claim, done still succeeds; a status line names the taken leaf
// instead of erroring (done is irreversible, claim is opportunistic).
func TestDone_ClaimNext_RaceLostReportsStatus(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	b := job.MustAdd(t, db, root, "B")
	db.Close()

	// alice claims a; bob claims b before alice finishes.
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "bob", "claim", b); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	// alice: done a --claim-next. b is gone; no other leaf.
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next should not error on race: %v", err)
	}
	if !strings.Contains(stdout, "Done: "+a) {
		t.Errorf("ack should include Done line:\n%s", stdout)
	}
	if strings.Contains(stdout, "Claimed: "+b) {
		t.Errorf("should not claim b, which bob already has:\n%s", stdout)
	}
}

// --- done --claim-next scoping -----------------------------------------

// The follow-on claim is scoped to the just-closed task's root subtree, so
// closing a leaf in root A never hands you an unrelated leaf in root B.
func TestDone_ClaimNext_ScopedToClosedTaskRoot(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootB := job.MustAdd(t, db, "", "Root B") // created first, sorts before A
	b1 := job.MustAdd(t, db, rootB, "B1")
	rootA := job.MustAdd(t, db, "", "Root A")
	a1 := job.MustAdd(t, db, rootA, "A1")
	a2 := job.MustAdd(t, db, rootA, "A2")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a1); err != nil {
		t.Fatalf("claim a1: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a1, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+a2) {
		t.Errorf("should claim A's next leaf %s:\n%s", a2, stdout)
	}
	if strings.Contains(stdout, "Claimed: "+b1) {
		t.Errorf("must not claim %s in unrelated root B:\n%s", b1, stdout)
	}
}

// --under <id> overrides the default scope to an arbitrary subtree.
func TestDone_ClaimNext_Under_OverridesScope(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootA := job.MustAdd(t, db, "", "Root A")
	a1 := job.MustAdd(t, db, rootA, "A1")
	_ = job.MustAdd(t, db, rootA, "A2")
	rootB := job.MustAdd(t, db, "", "Root B")
	b1 := job.MustAdd(t, db, rootB, "B1")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a1); err != nil {
		t.Fatalf("claim a1: %v", err)
	}
	// Close a1 but direct the next claim into root B explicitly.
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a1, "--claim-next", "--under", rootB)
	if err != nil {
		t.Fatalf("done --claim-next --under: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+b1) {
		t.Errorf("--under %s should claim %s:\n%s", rootB, b1, stdout)
	}
}

// --any restores the old repo-global next-leaf behavior.
func TestDone_ClaimNext_Any_RestoresGlobal(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootB := job.MustAdd(t, db, "", "Root B") // sorts before A globally
	b1 := job.MustAdd(t, db, rootB, "B1")
	rootA := job.MustAdd(t, db, "", "Root A")
	a1 := job.MustAdd(t, db, rootA, "A1") // A's only leaf
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a1); err != nil {
		t.Fatalf("claim a1: %v", err)
	}
	// A is fully done after this close; --any should reach into B globally.
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a1, "--claim-next", "--any")
	if err != nil {
		t.Fatalf("done --claim-next --any: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+b1) {
		t.Errorf("--any should claim globally-next leaf %s:\n%s", b1, stdout)
	}
}

// Default scope (no --any) must NOT fall through to another root when the
// closed task's own root is exhausted.
func TestDone_ClaimNext_DefaultScope_NoFallthrough(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootB := job.MustAdd(t, db, "", "Root B")
	b1 := job.MustAdd(t, db, rootB, "B1")
	rootA := job.MustAdd(t, db, "", "Root A")
	a1 := job.MustAdd(t, db, rootA, "A1") // A's only leaf
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a1); err != nil {
		t.Fatalf("claim a1: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a1, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if strings.Contains(stdout, "Claimed: "+b1) {
		t.Errorf("default scope must not fall through to root B leaf %s:\n%s", b1, stdout)
	}
}

// --- P4: positional-prose detection -------------------------------------

// A multi-word second positional to `done` is prose, not an ID. Suggest -m.
func TestDone_PositionalProse_MultiWord_SuggestsDashM(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "wrote the red tests")
	if err == nil {
		t.Fatal("expected error for prose second positional")
	}
	if !strings.Contains(err.Error(), "-m") {
		t.Errorf("error should suggest -m: %v", err)
	}
}

// A single-word non-ID second positional is ambiguous. Heuristic: err on
// the side of suggesting -m (typoed IDs are rarer than forgotten -m).
func TestDone_PositionalProse_SingleWordNonID_SuggestsDashM(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	// "done" is 4 chars, not a valid 5-char short-ID shape.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "done")
	if err == nil {
		t.Fatal("expected error for non-ID-shaped second positional")
	}
	if !strings.Contains(err.Error(), "-m") {
		t.Errorf("error should suggest -m: %v", err)
	}
}

// A valid 5-char short-ID-shaped second positional is treated as a task
// ID (multi-done), not as prose.
func TestDone_TwoValidIDs_StillWorks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, b); err != nil {
		t.Fatalf("done of two valid IDs should work: %v", err)
	}
}

// claim with a prose second positional: suggest -m.
func TestClaim_PositionalProse_SuggestsDashM(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "heading into this now")
	if err == nil {
		t.Fatal("expected error for prose second positional on claim")
	}
	// claim's second arg is a duration, not a note. Suggest -m isn't the
	// right pointer here — the right pointer is "that's not a duration".
	// But for the feedback author's muscle memory, suggest a helpful hint.
	if !strings.Contains(err.Error(), "duration") && !strings.Contains(err.Error(), "-m") {
		t.Errorf("error should explain the second arg shape: %v", err)
	}
}

// --- P3: -m @file / -m - stdin support ---------------------------------

// -m @path reads the note body from a file.
func TestDone_DashM_AtFile_ReadsFromFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	notePath := t.TempDir() + "/note.txt"
	contents := "multi-line\nevidence payload\nwith backticks ```and stuff```"
	if err := os.WriteFile(notePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "@"+notePath); err != nil {
		t.Fatalf("done -m @file: %v", err)
	}

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil || *task.CompletionNote != contents {
		t.Errorf("note: got %v, want %q", task.CompletionNote, contents)
	}
}

// -m - reads the note body from stdin.
func TestDone_DashM_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	contents := "stdin piped note\nwith newlines"
	if _, _, err := runCLIWithStdin(t, dbFile, contents, "--as", "alice", "done", id, "-m", "-"); err != nil {
		t.Fatalf("done -m -: %v", err)
	}

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil || *task.CompletionNote != contents {
		t.Errorf("note: got %v, want %q", task.CompletionNote, contents)
	}
}

// Literal strings starting with anything other than @ or - are unchanged.
func TestDone_DashM_LiteralStringStillWorks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "plain literal"); err != nil {
		t.Fatalf("done -m: %v", err)
	}

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil || *task.CompletionNote != "plain literal" {
		t.Errorf("note: got %v, want 'plain literal'", task.CompletionNote)
	}
}

// @nonexistent errors with a clear message — don't silently treat as
// literal; the user meant a file.
func TestDone_DashM_AtNonexistentFile_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "@/does/not/exist.txt")
	if err == nil {
		t.Fatal("expected error for @nonexistent file")
	}
	if !strings.Contains(err.Error(), "-m @") && !strings.Contains(err.Error(), "read note file") {
		t.Errorf("error should explain @-file failure: %v", err)
	}
}

// File contents preserve internal newlines verbatim.
func TestDone_DashM_AtFile_PreservesNewlines(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	notePath := t.TempDir() + "/note.txt"
	contents := "line 1\nline 2\nline 3"
	if err := os.WriteFile(notePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "@"+notePath); err != nil {
		t.Fatalf("done: %v", err)
	}

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil {
		t.Fatal("no completion note")
	}
	if !strings.Contains(*task.CompletionNote, "\n") {
		t.Errorf("newlines not preserved: %q", *task.CompletionNote)
	}
	if *task.CompletionNote != contents {
		t.Errorf("note mismatch:\n got: %q\nwant: %q", *task.CompletionNote, contents)
	}
}

// The same resolution applies to `note -m`.
func TestNote_DashM_AtFile_ReadsFromFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	notePath := t.TempDir() + "/note.txt"
	contents := "note body from file"
	if err := os.WriteFile(notePath, []byte(contents), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "note", id, "-m", "@"+notePath); err != nil {
		t.Fatalf("note -m @file: %v", err)
	}

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.Description != "" {
		t.Errorf("description should remain empty, got %q", task.Description)
	}
	detail, err := job.GetLatestEventDetail(db2, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil || detail["text"] != contents {
		t.Errorf("noted event text: got %v, want %q", detail, contents)
	}
}

// -m "<note>" composes with --claim-next: the note attaches to the
// closed task and the claim still fires.
func TestDone_ClaimNext_CombinesWithNote(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	b := job.MustAdd(t, db, root, "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "-m", "wrapped up the red tests", "--claim-next"); err != nil {
		t.Fatalf("done -m --claim-next: %v", err)
	}

	// Verify note recorded on a, and b claimed by alice.
	db2 := openTestDB(t, dbFile)
	at := job.MustGet(t, db2, a)
	if at.CompletionNote == nil || *at.CompletionNote != "wrapped up the red tests" {
		t.Errorf("completion note not recorded; got %v", at.CompletionNote)
	}
	bt := job.MustGet(t, db2, b)
	if bt.Status != "claimed" || bt.ClaimedBy == nil || *bt.ClaimedBy != "alice" {
		t.Errorf("b should be claimed; got status=%q, claimed_by=%v", bt.Status, bt.ClaimedBy)
	}
}

// --- P7: output tightening ---------------------------------------------

// Improvement 1 (simple case): closing the only child of a root auto-closes
// the root. The `Auto-closed: <root>` line says everything that needs to be
// said; the trailing `All tasks in <root> complete.` is a redundant
// duplicate and should be suppressed.
func TestDone_AutoCloseIsWholeTreeRoot_SuppressesWholeTreeLine(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	c1 := job.MustAdd(t, db, root, "C1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	wantAuto := "  Auto-closed: " + root + " \"Root\""
	if !strings.Contains(stdout, wantAuto) {
		t.Errorf("auto-closed line should still fire:\n%s", stdout)
	}
	if strings.Contains(stdout, "All tasks in "+root+" complete") {
		t.Errorf("whole-tree line duplicates the auto-closed line and should be suppressed:\n%s", stdout)
	}
}

// Improvement 1 (nested): closing the only leaf in a root->p1->c1 chain
// auto-closes p1 AND root. The highest auto-closed ancestor equals the
// whole-tree root, so `All tasks in root complete` must still be
// suppressed — the two Auto-closed lines already convey the cascade.
func TestDone_NestedAutoCloseAllTheWayUp_SuppressesWholeTreeLine(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	p1 := job.MustAdd(t, db, root, "P1")
	c1 := job.MustAdd(t, db, p1, "C1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "  Auto-closed: "+p1) {
		t.Errorf("expected auto-closed line for p1:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  Auto-closed: "+root) {
		t.Errorf("expected auto-closed line for root:\n%s", stdout)
	}
	if strings.Contains(stdout, "All tasks in "+root+" complete") {
		t.Errorf("whole-tree line should be suppressed when root is the highest auto-closed ancestor:\n%s", stdout)
	}
}

// Improvement 2: Next: should resolve to a claimable leaf, not a parent
// that still has open children. Here done c1 auto-closes p1; the next
// top-level sibling is p2, but p2 is a non-leaf — the actionable next
// task is c2 (p2's leaf).
func TestDone_NextResolvesToLeafUnderSiblingParent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p1 := job.MustAdd(t, db, "", "P1")
	p2 := job.MustAdd(t, db, "", "P2")
	c1 := job.MustAdd(t, db, p1, "C1")
	c2 := job.MustAdd(t, db, p2, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	// Must not point at the non-leaf parent.
	if strings.Contains(stdout, "Next: "+p2+" \"P2\"") {
		t.Errorf("Next should not point at non-leaf parent p2:\n%s", stdout)
	}
	wantLeaf := "Next: " + c2 + " \"C2\""
	if !strings.Contains(stdout, wantLeaf) {
		t.Errorf("Next should resolve to leaf c2:\n%s", stdout)
	}
}

// Improvement 3 (success): when --claim-next successfully claims the next
// leaf, the ack must not also emit a Next: line pointing at the same
// work. The Claimed: line already tells the user what was picked up.
func TestDone_ClaimNext_SuccessfulClaim_SuppressesNextLine(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	b := job.MustAdd(t, db, root, "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if !strings.Contains(stdout, "Claimed: "+b) {
		t.Fatalf("setup: Claimed line should fire:\n%s", stdout)
	}
	if strings.Contains(stdout, "Next: ") {
		t.Errorf("Next: line should be suppressed when Claimed: already fired:\n%s", stdout)
	}
}

// Improvement 3 (race fallback): when --claim-next loses the race (or
// finds nothing to claim), the Next: line is a useful fallback and must
// still fire if the DoneContext has a next sibling.
func TestDone_ClaimNext_RaceLost_NextStillFires(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	b := job.MustAdd(t, db, root, "B")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", a); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "bob", "claim", b); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, "--claim-next")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if strings.Contains(stdout, "Claimed: ") {
		t.Fatalf("setup: no successful claim expected:\n%s", stdout)
	}
	// The claim attempt failed; Next should still name b as the
	// useful fallback target.
	wantNext := "Next: " + b + " \"B\""
	if !strings.Contains(stdout, wantNext) {
		t.Errorf("Next: fallback should still fire on race-lost claim-next:\n%s", stdout)
	}
}

func TestDone_Md_NoteEcho_Single(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "A task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "shipped")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "Done: "+id+" \"A task\"") {
		t.Errorf("missing Done line:\n%s", stdout)
	}
	if !strings.Contains(stdout, `  note: 7 chars · "shipped"`) {
		t.Errorf("missing note echo:\n%s", stdout)
	}
}

func TestDone_Md_NoteEcho_Long_Truncates(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "A task")
	db.Close()

	// 124-rune body with spaces — past the 60-rune window, must ellipsis.
	body := strings.Repeat("abcdefghij ", 11) + "xyz"
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", body)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "124 chars") {
		t.Errorf("char count: want 124:\n%s", stdout)
	}
	if !strings.Contains(stdout, "…\"") {
		t.Errorf("expected ellipsis in preview:\n%s", stdout)
	}
}

// Hierarchical Next: walk — when the closed task's own root tree still
// has claimable work, we must stay in that tree even if another root
// tree has EARLIER sort_path. Current sort_path-based global fallback
// gets this wrong.
func TestDone_Next_HierarchicalWalk_StaysInRootTreeOverEarlierSortPath(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	// Other root is earlier (lower sort_order) and has its own leaf.
	otherRoot := job.MustAdd(t, db, "", "Other")
	_ = job.MustAdd(t, db, otherRoot, "OtherLeaf")
	// The tree we are working in.
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	_ = job.MustAdd(t, db, root, "B")
	c := job.MustAdd(t, db, root, "C")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	want := "Next: " + a + " \"A\""
	if !strings.Contains(stdout, want) {
		t.Errorf("Next must stay in the closed task's root tree:\n%s", stdout)
	}
}

// Walk up past an auto-closed parent and pick up an EARLIER sibling at
// the grandparent level before escaping to a different root tree. The
// current NextAfterParent path only looks forward; this exercises the
// backward branch on an ancestor level.
func TestDone_Next_HierarchicalWalk_EarlierSiblingAtGrandparent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	// Decoy: earlier root with its own leaf; sort_path will undercut the
	// correct answer under the current global fallback.
	decoy := job.MustAdd(t, db, "", "Decoy")
	_ = job.MustAdd(t, db, decoy, "DecoyLeaf")
	// Working root.
	working := job.MustAdd(t, db, "", "Working")
	workLeft := job.MustAdd(t, db, working, "WorkLeft")
	workSub := job.MustAdd(t, db, working, "WorkSub")
	closing := job.MustAdd(t, db, workSub, "Closing")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", closing)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	// Closing is the only child of WorkSub → WorkSub auto-closes.
	// Walk up to Working. Forward siblings of WorkSub: none. Earlier:
	// WorkLeft. Descend (it is a leaf) → WorkLeft.
	want := "Next: " + workLeft + " \"WorkLeft\""
	if !strings.Contains(stdout, want) {
		t.Errorf("Next should pick WorkLeft via the walk-up's earlier-sibling branch:\n%s", stdout)
	}
}

// P5 — When closing a leaf exhausts the forward sibling/parent chain but
// work remains elsewhere in the tree, Next: should fall back to the
// globally-next claimable leaf rather than going silent. Here P2 is the
// later root-level parent and c2a is its only leaf; closing c2a
// auto-closes P2, and there is no root sibling after P2 — but P1 still
// has c1a. The ack must surface c1a as the fallback.
func TestDone_Next_Fallback_WhenLastLeafOfLastRoot_OpenWorkRemainsEarlier(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	p1 := job.MustAdd(t, db, "", "P1")
	p2 := job.MustAdd(t, db, "", "P2")
	c1a := job.MustAdd(t, db, p1, "c1a")
	c2a := job.MustAdd(t, db, p2, "c2a")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c2a)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	want := "Next: " + c1a + " \"c1a\""
	if !strings.Contains(stdout, want) {
		t.Errorf("Next: should fall back to globally-next claimable leaf:\n%s", stdout)
	}
}

// P5 — Closing the last sibling (by sort_order) of a parent that does
// NOT auto-close (because earlier siblings are still open) currently
// leaves the ack silent on "what next." Fallback should name an earlier
// sibling in the same parent.
func TestDone_Next_Fallback_WhenHighestSortOrderSiblingClosesWithEarlierOpen(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	a := job.MustAdd(t, db, root, "A")
	_ = job.MustAdd(t, db, root, "B")
	c := job.MustAdd(t, db, root, "C")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	// A comes before B in sort_order; global frontier picks A first.
	want := "Next: " + a + " \"A\""
	if !strings.Contains(stdout, want) {
		t.Errorf("Next: should fall back to an earlier unblocked sibling:\n%s", stdout)
	}
}

func TestDone_Md_NoteEcho_Multi_PerItem(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, b, "-m", "ok")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	// Each closed target emits its own note preview sub-line.
	count := strings.Count(stdout, `  note: 2 chars · "ok"`)
	if count != 2 {
		t.Errorf("want 2 note previews, got %d:\n%s", count, stdout)
	}
}

// TestDone_StrictDefault_RefusesPending pins the CLI surface for the
// strict-default close gate: a plain `job done <id>` against a task with
// pending criteria exits non-zero with the greppable "cannot close: N
// pending criteria" prefix and names the override flag.
func TestDone_StrictDefault_RefusesPending(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "first criterion"}, {Label: "second criterion"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id)
	if err == nil {
		t.Fatal("expected strict-default refusal at the CLI")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot close: 2 pending criteria") {
		t.Errorf("missing greppable prefix:\n%s", msg)
	}
	if !strings.Contains(msg, "first criterion") || !strings.Contains(msg, "second criterion") {
		t.Errorf("error should list pending labels:\n%s", msg)
	}
	if !strings.Contains(msg, "--force-close-with-pending") {
		t.Errorf("error should name the override flag:\n%s", msg)
	}

	// And the task remained open: the strict gate is a true refusal, not a
	// warn-and-close.
	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status == "done" {
		t.Errorf("task should not have closed under strict-default; status=%q", task.Status)
	}
}

// TestDone_AllPassed_MarksAllPendingPassed pins the headline shorthand:
// `job done <id> --all-passed` marks every pending criterion passed before
// closing, and the close succeeds without strict-default refusal.
func TestDone_AllPassed_MarksAllPendingPassed(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "alpha"}, {Label: "beta"}, {Label: "gamma"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all-passed")
	if err != nil {
		t.Fatalf("done --all-passed: %v", err)
	}
	if !strings.Contains(stdout, "Marked 3 criteria passed before closing") {
		t.Errorf("missing audit line:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "done" {
		t.Errorf("status: got %q, want done", task.Status)
	}
	cs, err := job.GetCriteria(db, task.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	for _, c := range cs {
		if c.State != job.CriterionPassed {
			t.Errorf("criterion %q: got %q, want passed", c.Label, c.State)
		}
	}
	detail, _ := job.GetLatestEventDetail(db, task.ID, "done")
	if detail["criteria_bulk_state"] != "passed" {
		t.Errorf("done event criteria_bulk_state: got %v, want \"passed\"", detail["criteria_bulk_state"])
	}
}

// TestDone_AllSkipped_MarksAllPendingSkipped covers the --all=<state> form
// with skipped, the second-most-common close shape (criteria that don't
// apply rather than tasks accomplished).
func TestDone_AllSkipped_MarksAllPendingSkipped(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "p"}, {Label: "q"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all=skipped")
	if err != nil {
		t.Fatalf("done --all=skipped: %v", err)
	}
	if !strings.Contains(stdout, "Marked 2 criteria skipped before closing") {
		t.Errorf("missing audit line:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	cs, _ := job.GetCriteria(db, task.ID)
	for _, c := range cs {
		if c.State != job.CriterionSkipped {
			t.Errorf("criterion %q: got %q, want skipped", c.Label, c.State)
		}
	}
}

// TestDone_AllPassed_SkipsAlreadyMarked enforces the no-spurious-events
// criterion: if a criterion was already marked (e.g. failed earlier in the
// session), the bulk shorthand must leave it alone, both in DB state and in
// the event log.
func TestDone_AllPassed_SkipsAlreadyMarked(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "p1"}, {Label: "p2"}, {Label: "p3"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "p2", job.CriterionFailed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all-passed")
	if err != nil {
		t.Fatalf("done --all-passed: %v", err)
	}
	if !strings.Contains(stdout, "Marked 2 criteria passed before closing") {
		t.Errorf("audit line should report 2 (the still-pending count), not 3:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	cs, _ := job.GetCriteria(db, task.ID)
	for _, c := range cs {
		switch c.Label {
		case "p2":
			if c.State != job.CriterionFailed {
				t.Errorf("p2 should remain failed; got %q", c.State)
			}
		default:
			if c.State != job.CriterionPassed {
				t.Errorf("%s: got %q, want passed", c.Label, c.State)
			}
		}
	}
}

// TestDone_AllPassed_PerRowOverrideWins composes the bulk flag with an
// explicit --criterion override. The bulk default applies broadly; the
// explicit row wins on its own label.
func TestDone_AllPassed_PerRowOverrideWins(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "a"}, {Label: "b"}, {Label: "c"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all-passed", "--criterion", "b=skipped"); err != nil {
		t.Fatalf("done: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	cs, _ := job.GetCriteria(db, task.ID)
	for _, c := range cs {
		switch c.Label {
		case "b":
			if c.State != job.CriterionSkipped {
				t.Errorf("explicit override should win; b=%q, want skipped", c.State)
			}
		default:
			if c.State != job.CriterionPassed {
				t.Errorf("%s: got %q, want passed", c.Label, c.State)
			}
		}
	}
}

// TestDone_AllPassed_NoAuditLineWhenNothingTouched guards against a
// noisy/empty audit line when every criterion was already marked at claim
// time and the bulk flag has nothing to do.
func TestDone_AllPassed_NoAuditLineWhenNothingTouched(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{{Label: "only"}}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := job.RunSetCriterion(db, id, "only", job.CriterionPassed, "alice"); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all-passed")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if strings.Contains(stdout, "Marked 0 criteria") {
		t.Errorf("audit line should be omitted when nothing was touched:\n%s", stdout)
	}
}

// TestDone_AllInvalidState_Errors confirms the --all=<state> parser rejects
// states other than the legal vocabulary so a typo doesn't silently leave
// every criterion in some unintended state.
func TestDone_AllInvalidState_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{{Label: "x"}}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--all=bogus")
	if err == nil {
		t.Fatal("expected error for invalid --all state")
	}
}

// TestDone_ForceCloseWithPending_ClosesAndRecordsWaiver pins the CLI
// override path: --force-close-with-pending closes the task and the
// unmarked labels survive on the done event so a reviewer can see what
// was deferred.
func TestDone_ForceCloseWithPending_ClosesAndRecordsWaiver(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if _, err := job.RunAddCriteria(db, id, []job.Criterion{
		{Label: "deferred-A"}, {Label: "deferred-B"},
	}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "--force-close-with-pending"); err != nil {
		t.Fatalf("force-close should succeed: %v", err)
	}

	db = openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Status != "done" {
		t.Errorf("status: got %q, want done", task.Status)
	}
	detail, err := job.GetLatestEventDetail(db, task.ID, "done")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	waived, ok := detail["criteria_waived"].([]any)
	if !ok || len(waived) != 2 {
		t.Fatalf("criteria_waived: got %v, want a 2-element list", detail["criteria_waived"])
	}
}
