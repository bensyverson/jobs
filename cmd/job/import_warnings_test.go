package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// W19Ae — `job import` block-selection warnings surface on stderr (and never
// block the import).
func TestImportCLI_WarningsToStderr(t *testing.T) {
	dbFile := setupCLI(t)

	body := "```yaml\n" +
		"orient: ignore-me\n" +
		"tasks:\n" +
		"  - title: Real task\n" +
		"```\n"
	plan := filepath.Join(t.TempDir(), "plan.md")
	if err := os.WriteFile(plan, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, stderr, err := runCLI(t, dbFile, "import", "--as", "alice", "--dry-run", plan)
	if err != nil {
		t.Fatalf("import: %v\nstderr:%s", err, stderr)
	}
	if !strings.Contains(stderr, "warning:") || !strings.Contains(stderr, "import grammar") {
		t.Errorf("expected a grammar warning on stderr, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "orient") {
		t.Errorf("warning should name the ignored key on stderr:\n%s", stderr)
	}
	// The import is not blocked: the valid task still echoes on stdout.
	if !strings.Contains(stdout, "Real task") {
		t.Errorf("expected the task to import despite the warning:\n%s", stdout)
	}
}
