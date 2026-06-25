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
