package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job add` keeps the strict `<parent> <title>` positional order. Instead
// of auto-detecting which arg is which (DWIM that breaks on short titles
// that collide with the short-id shape), the parser branches the error
// on what's actually wrong and points the operator at the right fix.

// c1U: two-arg call with unresolved leading positional → hint about order.
func TestAdd_TwoArgs_UnresolvedLeadingPositional_HintsAtOrder(t *testing.T) {
	dbFile := setupCLI(t)

	// `Child A` doesn't resolve as a short id and no positional `--parent`
	// matches it — the user almost certainly transposed the args.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "add", "Child A", "actual-title")
	if err == nil {
		t.Fatal("expected an error for transposed positional args")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "add: no such parent") {
		t.Errorf("error should lead with a stable, greppable prefix `add: no such parent`; got: %q", msg)
	}
	if !strings.Contains(msg, "add <parent> <title>") {
		t.Errorf("hint should name the correct positional order; got: %q", msg)
	}
	if !strings.Contains(msg, "--parent") {
		t.Errorf("hint should mention --parent as the disambiguator; got: %q", msg)
	}
}

// fTd: single-arg call where the arg matches an existing short id refuses
// the create and prompts the user to either supply a title or pass
// --parent="" for the literal-title intent.
func TestAdd_SingleArg_MatchesExistingShortID_Refuses(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	existing := job.MustAdd(t, db, "", "Existing parent task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "add", existing)
	if err == nil {
		t.Fatal("expected an error: single-arg add with an existing short id should refuse")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "add: ambiguous single arg") {
		t.Errorf("error should lead with a stable, greppable prefix `add: ambiguous single arg`; got: %q", msg)
	}
	if !strings.Contains(msg, existing) {
		t.Errorf("error should name the ambiguous short id; got: %q", msg)
	}
	if !strings.Contains(msg, "add "+existing+" <title>") {
		t.Errorf("error should suggest supplying a title; got: %q", msg)
	}
	if !strings.Contains(msg, `--parent=""`) {
		t.Errorf("error should offer --parent=\"\" as the literal-title escape; got: %q", msg)
	}

	// The task itself should not have been created.
	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	nodes, err := job.RunListFiltered(db, job.ListFilter{})
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	for _, n := range nodes {
		if n.Task.Title == existing {
			t.Errorf("refused add should not have created a task literally titled %q", existing)
		}
	}
}

// fTd/escape hatch: with --parent="", the literal-title intent is honored
// and a root task with the title equal to the short-id-shaped string is
// created.
func TestAdd_SingleArg_MatchesExistingShortID_LiteralTitleWithExplicitParentFlag(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	existing := job.MustAdd(t, db, "", "Existing parent task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "add", existing, "--parent="); err != nil {
		t.Fatalf("literal-title escape hatch should succeed: %v", err)
	}
	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	nodes, err := job.RunListFiltered(db, job.ListFilter{})
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	var found bool
	for _, n := range nodes {
		if n.Task.Title == existing {
			found = true
		}
	}
	if !found {
		t.Errorf("--parent=\"\" should create a root task literally titled %q", existing)
	}
}

// WO8: single-arg call with a non-short-id string still creates a root
// task with that title — no new friction for the common case.
func TestAdd_SingleArg_PlainTitle_CreatesRoot(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "Write the thing")
	if err != nil {
		t.Fatalf("plain single-arg add should succeed: %v", err)
	}
	created := strings.TrimSpace(strings.SplitN(stdout, "\n", 2)[0])
	if created == "" {
		t.Fatalf("expected a new short id on stdout; got:\n%s", stdout)
	}
}

// VLN: two-arg call with a valid leading parent short id still creates the
// child as before — happy path is unchanged.
func TestAdd_TwoArgs_ValidParent_CreatesChild(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", parent, "Child")
	if err != nil {
		t.Fatalf("two-arg add with valid parent should succeed: %v", err)
	}
	child := strings.TrimSpace(strings.SplitN(stdout, "\n", 2)[0])
	if child == "" {
		t.Fatalf("expected a new short id on stdout; got:\n%s", stdout)
	}

	db = openTestDB(t, dbFile)
	t.Cleanup(func() { db.Close() })
	task, err := job.GetTaskByShortID(db, child)
	if err != nil {
		t.Fatalf("GetTaskByShortID: %v", err)
	}
	if task == nil {
		t.Fatalf("created task %q not found", child)
	}
	if task.ParentID == nil {
		t.Errorf("child should be parented under %s; got nil parent", parent)
	}
}
