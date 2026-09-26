package handlers_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

func TestLog_ImportedRowNamesTheSourceAndCounts(t *testing.T) {
	db := setupLogTestDB(t)
	path := filepath.Join(t.TempDir(), "plan.md")
	body := "```yaml\ntasks:\n  - title: Ship it\n    children:\n      - title: Write it\n```\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunImport(db, path, "", false, "alice"); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	deps := newLogDeps(t, db)
	row := rowFor(t, fetchLog(t, deps, ""), "imported")
	mustContainAll(t, row,
		`c-log-row__verb--imported">imported<`,
		"from plan.md (2 tasks, 1 leaf)",
	)
}

func TestLog_ImportedRowNamesTheParentWhenNested(t *testing.T) {
	db := setupLogTestDB(t)
	parent := mustAdd(t, db, "alice", "Existing root", nil, nil)
	path := filepath.Join(t.TempDir(), "plan.md")
	body := "```yaml\ntasks:\n  - title: Ship it\n```\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunImport(db, path, parent, false, "alice"); err != nil {
		t.Fatalf("RunImport: %v", err)
	}

	deps := newLogDeps(t, db)
	row := rowFor(t, fetchLog(t, deps, ""), "imported")
	mustContainAll(t, row, "from plan.md under "+parent+" (1 task, 1 leaf)")
}

// imported is in the filter-chip vocabulary, so "how many plans were
// imported" is one click on the Log.
func TestLog_TypeChipsOfferImported(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")
	if !strings.Contains(body, "type=imported") {
		t.Errorf("the type chip strip does not offer imported\n---\n%s", body)
	}
}
