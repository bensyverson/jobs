package main

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"gopkg.in/yaml.v3"
)

// 0aL1p — CLI wiring for `job orient`. These exercise the cobra command end
// to end against a real .jobs.db via the shared runCLI harness.

// fDB — `job orient` (no arg) emits YAML for the next available leaf.
func TestOrient_NoArg_EmitsYAML(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	leaf := job.MustAdd(t, db, root, "Leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "orient")
	if err != nil {
		t.Fatalf("orient: %v\n%s", err, stdout)
	}
	var doc struct {
		Orient struct {
			Target string `yaml:"target"`
		} `yaml:"orient"`
		Tasks []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	if doc.Orient.Target != leaf {
		t.Errorf("orient.target: got %q, want next leaf %q", doc.Orient.Target, leaf)
	}
	if len(doc.Tasks) == 0 {
		t.Errorf("expected a tasks tree:\n%s", stdout)
	}
}

// fDB — `job orient <id>` emits YAML targeting that id.
func TestOrient_PositionalID_EmitsYAML(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	branch := job.MustAdd(t, db, root, "Branch")
	leaf := job.MustAdd(t, db, branch, "Deep leaf")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "orient", leaf)
	if err != nil {
		t.Fatalf("orient %s: %v\n%s", leaf, err, stdout)
	}
	var doc struct {
		Orient struct {
			Target string `yaml:"target"`
			Root   string `yaml:"root"`
		} `yaml:"orient"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	if doc.Orient.Target != leaf {
		t.Errorf("orient.target: got %q, want %q", doc.Orient.Target, leaf)
	}
	if doc.Orient.Root != root {
		t.Errorf("orient.root: got %q, want whole-tree root %q", doc.Orient.Root, root)
	}
}

// ypr — --format defaults to yaml; --format md returns a clear not-implemented message.
func TestOrient_FormatDefaultYAML_MdNotImplemented(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Leaf")
	db.Close()

	// Default (no --format) is yaml.
	stdout, _, err := runCLI(t, dbFile, "orient")
	if err != nil {
		t.Fatalf("orient: %v", err)
	}
	if !strings.Contains(stdout, "orient:") {
		t.Errorf("default format is not YAML:\n%s", stdout)
	}

	// --format md is not yet implemented and must say so clearly.
	_, _, err = runCLI(t, dbFile, "orient", "--format", "md")
	if err == nil {
		t.Fatalf("expected error for --format md, got nil")
	}
	if !strings.Contains(err.Error(), "md") || !strings.Contains(strings.ToLower(err.Error()), "implement") {
		t.Errorf("md error not a clear not-implemented message: %v", err)
	}
}

// h9Z — --scope <id> narrows the rendered tree to that subtree.
func TestOrient_Scope_Narrows(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	branch := job.MustAdd(t, db, root, "Branch")
	leaf := job.MustAdd(t, db, branch, "Deep leaf")
	sibling := job.MustAdd(t, db, root, "OffScopeSibling")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "orient", leaf, "--scope", branch)
	if err != nil {
		t.Fatalf("orient --scope: %v\n%s", err, stdout)
	}
	if strings.Contains(stdout, sibling) || strings.Contains(stdout, "OffScopeSibling") {
		t.Errorf("off-scope sibling leaked into scoped render:\n%s", stdout)
	}
	if !strings.Contains(stdout, leaf) {
		t.Errorf("in-scope leaf missing from scoped render:\n%s", stdout)
	}
}

// E1c — Command is registered and appears in `job --help`.
func TestOrient_RegisteredInHelp(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(stdout, "orient") {
		t.Errorf("`orient` not listed in --help:\n%s", stdout)
	}
}

// Ps6Ss — `orient` is every session's first command; a first command that
// fails reads as a broken tool. An empty tree is a valid answer, not an
// error, so orient prints the same guidance `next` would and exits 0.

// e5g — an empty repo prints the plain no-tasks guidance and exits 0.
func TestOrient_EmptyRepo_ExitsZeroWithGuidance(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "orient")
	if err != nil {
		t.Fatalf("orient on empty repo: want exit 0, got error: %v\n%s", err, stdout)
	}
	var doc struct {
		Orient struct {
			Target  *string `yaml:"target"`
			Message string  `yaml:"message"`
		} `yaml:"orient"`
		Tasks []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	if doc.Orient.Target != nil {
		t.Errorf("orient.target: want null, got %q", *doc.Orient.Target)
	}
	wantMsg := "No available tasks. Run 'list all' to see blocked or claimed work."
	if doc.Orient.Message != wantMsg {
		t.Errorf("orient.message: got %q, want %q", doc.Orient.Message, wantMsg)
	}
	if len(doc.Tasks) != 0 {
		t.Errorf("expected no tasks in a genuinely empty repo:\n%s", stdout)
	}
}

// 1TF — a focused root with no available leaf prints the focused-root
// guidance (naming the root and the escape hatches) and exits 0.
func TestOrient_FocusedRootExhausted_ExitsZeroWithGuidance(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	rootA := job.MustAdd(t, db, "", "Root A")
	job.MustAdd(t, db, rootA, "A leaf")
	rootB := job.MustAdd(t, db, "", "Root B")
	leafB := job.MustAdd(t, db, rootB, "Only B leaf")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", leafB); err != nil {
		t.Fatalf("claim: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "orient")
	if err != nil {
		t.Fatalf("orient with exhausted focused root: want exit 0, got error: %v\n%s", err, stdout)
	}
	var doc struct {
		Orient struct {
			Target  *string `yaml:"target"`
			Message string  `yaml:"message"`
		} `yaml:"orient"`
		Tasks []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	if doc.Orient.Target != nil {
		t.Errorf("orient.target: want null, got %q", *doc.Orient.Target)
	}
	if !strings.Contains(doc.Orient.Message, rootB) || !strings.Contains(doc.Orient.Message, "focus --release") {
		t.Errorf("orient.message must name the focused root and the escape hatches: %q", doc.Orient.Message)
	}
	if len(doc.Tasks) == 0 {
		t.Errorf("expected the focused root's tree to still render:\n%s", stdout)
	}
}

// OLZ — `next` and `claim --next` are unchanged: an empty repo is still a
// non-zero exit for them, because the caller explicitly asked for a task.
func TestNextAndClaimNext_EmptyRepo_StillExitNonZero(t *testing.T) {
	dbFile := setupCLI(t)

	_, _, err := runCLI(t, dbFile, "next")
	if err == nil {
		t.Fatal("next on empty repo: expected non-zero exit, got nil error")
	}
	wantMsg := "No available tasks. Run 'list all' to see blocked or claimed work."
	if err.Error() != wantMsg {
		t.Errorf("next error: got %q, want %q", err.Error(), wantMsg)
	}

	_, _, err = runCLI(t, dbFile, "--as", "bob", "claim", "--next")
	if err == nil {
		t.Fatal("claim --next on empty repo: expected non-zero exit, got nil error")
	}
	if err.Error() != wantMsg {
		t.Errorf("claim --next error: got %q, want %q", err.Error(), wantMsg)
	}
}

// `orient --issues` with no issue-tree leaf available is an empty answer,
// not a failure — the same rule as the task-tree default.
func TestOrient_Issues_EmptyForest_ExitsZeroWithGuidance(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "orient", "--issues")
	if err != nil {
		t.Fatalf("orient --issues on an empty forest: %v", err)
	}
	if !strings.Contains(stdout, "target: null") || !strings.Contains(stdout, "No available tasks in any issue tree") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

// fwk0j — `orient --issues` on an exhausted issue forest must render the
// issue tree(s), never the task-focused tree it fell back to before this
// fix: a task root with an available leaf sat alongside an exhausted issue
// root, and RunOrientNoTasks never learned about --issues, so it rendered
// the task tree instead.
func TestOrient_Issues_ExhaustedForest_RendersIssueTreeNotTaskTree(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	taskRoot := job.MustAdd(t, db, "", "Plan")
	job.MustAdd(t, db, taskRoot, "Available task leaf")
	bugs := job.MustAdd(t, db, "", "Bugs")
	issueLeaf := job.MustAdd(t, db, bugs, "Only issue leaf")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "kind", bugs, "issue"); err != nil {
		t.Fatalf("kind %s issue: %v", bugs, err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", issueLeaf); err != nil {
		t.Fatalf("claim %s: %v", issueLeaf, err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "orient", "--issues")
	if err != nil {
		t.Fatalf("orient --issues with exhausted focused issue root: want exit 0, got error: %v\n%s", err, stdout)
	}
	var doc struct {
		Orient struct {
			Target  *string `yaml:"target"`
			Message string  `yaml:"message"`
		} `yaml:"orient"`
		Tasks []struct {
			ID string `yaml:"id"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	if doc.Orient.Target != nil {
		t.Errorf("orient.target: want null, got %q", *doc.Orient.Target)
	}
	if !strings.Contains(doc.Orient.Message, bugs) || !strings.Contains(doc.Orient.Message, "focus --release") {
		t.Errorf("orient.message must name the focused issue root and the escape hatches: %q", doc.Orient.Message)
	}
	if len(doc.Tasks) != 1 {
		t.Fatalf("tasks: got %d trees, want exactly 1 (the issue root)\n%s", len(doc.Tasks), stdout)
	}
	if doc.Tasks[0].ID != bugs {
		t.Errorf("rendered tree root: got %q, want the issue root %q\n%s", doc.Tasks[0].ID, bugs, stdout)
	}
	if strings.Contains(stdout, taskRoot) {
		t.Errorf("task root %s must not appear in the --issues no-tasks view:\n%s", taskRoot, stdout)
	}
}
