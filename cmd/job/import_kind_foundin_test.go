package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 9s2qL — the `kind` and `foundIn` keys through the CLI: the dry-run checklist
// reports what they would create, and neither trips the unknown-key warning.

const kindFoundInPlan = "```yaml\n" +
	"tasks:\n" +
	"  - title: Ship v1\n" +
	"    children:\n" +
	"      - title: Wire it into the router\n" +
	"        ref: router\n" +
	"  - title: Bugs\n" +
	"    kind: issue\n" +
	"    children:\n" +
	"      - title: Router drops the trailing slash\n" +
	"        foundIn: router\n" +
	"```\n"

func writeCLIPlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return path
}

func TestImportCLI_DryRunReportsKindAndFoundIn(t *testing.T) {
	dbFile := setupCLI(t)
	plan := writeCLIPlan(t, kindFoundInPlan)

	stdout, stderr, err := runCLI(t, dbFile, "import", "--as", "alice", "--dry-run", plan)
	if err != nil {
		t.Fatalf("import: %v\nstderr:%s", err, stderr)
	}
	if !strings.Contains(stdout, "Bugs (issue-tree)") {
		t.Errorf("dry-run should mark the issue root:\n%s", stdout)
	}
	if strings.Contains(stdout, "Ship v1 (task-tree)") {
		t.Errorf("dry-run should stay quiet about the default kind:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(found in <new-2>)") {
		t.Errorf("dry-run should report the found-in edge:\n%s", stdout)
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("kind/foundIn are grammar keys; no warning expected:\n%s", stderr)
	}
}

func TestImportCLI_KindOnChildFails(t *testing.T) {
	dbFile := setupCLI(t)
	plan := writeCLIPlan(t, "```yaml\n"+
		"tasks:\n"+
		"  - title: Ship v1\n"+
		"    children:\n"+
		"      - title: Bugs\n"+
		"        kind: issue\n"+
		"```\n")

	_, _, err := runCLI(t, dbFile, "import", "--as", "alice", plan)
	if err == nil {
		t.Fatal("expected the import to fail")
	}
	if !strings.Contains(err.Error(), "tasks[0].children[0]") {
		t.Errorf("error should name the row, got: %v", err)
	}
}

func TestImportCLI_RealRunRecordsKindAndFoundIn(t *testing.T) {
	dbFile := setupCLI(t)
	plan := writeCLIPlan(t, kindFoundInPlan)

	stdout, stderr, err := runCLI(t, dbFile, "import", "--as", "alice", plan)
	if err != nil {
		t.Fatalf("import: %v\nstderr:%s", err, stderr)
	}
	if !strings.Contains(stdout, "Bugs (issue-tree)") {
		t.Errorf("echo should mark the issue root:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(found in ") {
		t.Errorf("echo should report the found-in edge:\n%s", stdout)
	}

	// Drive the real readers: `show` on the bug names its source.
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 imported tasks, got:\n%s", stdout)
	}
	bugID := strings.Fields(lines[3])[0]
	rootID := strings.Fields(lines[2])[0]

	show, _, err := runCLI(t, dbFile, "show", bugID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(show, "Found in:") {
		t.Errorf("show should print the Found in: line:\n%s", show)
	}

	showRoot, _, err := runCLI(t, dbFile, "show", rootID)
	if err != nil {
		t.Fatalf("show root: %v", err)
	}
	if !strings.Contains(showRoot, "Kind:") || !strings.Contains(showRoot, "issue") {
		t.Errorf("show should print Kind: issue on the imported root:\n%s", showRoot)
	}
}
