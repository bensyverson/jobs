package handlers_test

import (
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// rowFor returns the single log row whose c-log-row--<type> modifier
// matches eventType. Fails the test when there isn't exactly one.
func rowFor(t *testing.T, body, eventType string) string {
	t.Helper()
	var hits []string
	for _, row := range splitLogRows(body) {
		if strings.Contains(row, `c-log-row c-log-row--`+eventType+`"`) {
			hits = append(hits, row)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("want exactly one %s row, got %d\n---\n%s", eventType, len(hits), body)
	}
	return hits[0]
}

func TestLog_KindChangedRowReadsAsKindConversion(t *testing.T) {
	db := setupLogTestDB(t)
	root := mustAdd(t, db, "alice", "A plan", nil, nil)
	if _, err := job.RunSetKind(db, root, job.KindIssue, "alice"); err != nil {
		t.Fatalf("RunSetKind: %v", err)
	}

	deps := newLogDeps(t, db)
	row := rowFor(t, fetchLog(t, deps, ""), "kind_changed")
	mustContainAll(t, row,
		`c-log-row__verb--kind_changed">kind<`,
		"task-tree → issue-tree",
	)
}

func TestLog_FoundInSetRowLinksTheSource(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "Working leaf", nil, nil)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues")
	bug := mustAdd(t, db, "alice", "A defect", &issueRoot, nil)
	mustSetFoundIn(t, db, bug, leaf)

	deps := newLogDeps(t, db)
	row := rowFor(t, fetchLog(t, deps, ""), "found_in_set")
	mustContainAll(t, row,
		`c-log-row__verb--found_in_set">found in<`,
		`href="/tasks/`+leaf+`"`,
	)
}

func TestLog_FoundInClearedRowNamesThePriorSource(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "Working leaf", nil, nil)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues")
	bug := mustAdd(t, db, "alice", "A defect", &issueRoot, nil)
	mustSetFoundIn(t, db, bug, leaf)
	if err := job.RunClearFoundIn(db, bug, "alice"); err != nil {
		t.Fatalf("RunClearFoundIn: %v", err)
	}

	deps := newLogDeps(t, db)
	row := rowFor(t, fetchLog(t, deps, ""), "found_in_cleared")
	mustContainAll(t, row,
		"cleared",
		`href="/tasks/`+leaf+`"`,
	)
}

func TestLog_TypeChipsEnumerateKindAndFoundIn(t *testing.T) {
	db := setupLogTestDB(t)
	deps := newLogDeps(t, db)
	body := fetchLog(t, deps, "")
	mustContainAll(t, body,
		"type=kind_changed",
		"type=found_in_set",
		"type=found_in_cleared",
	)
}

func TestLog_TypeFilterSelectsFoundInAndKindRows(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "Working leaf", nil, nil)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues")
	bug := mustAdd(t, db, "alice", "A defect", &issueRoot, nil)
	mustSetFoundIn(t, db, bug, leaf)
	if _, err := job.RunSetKind(db, issueRoot, job.KindTask, "alice"); err != nil {
		t.Fatalf("RunSetKind: %v", err)
	}

	deps := newLogDeps(t, db)

	foundOnly := fetchLog(t, deps, "type=found_in_set")
	rows := splitLogRows(foundOnly)
	if len(rows) != 1 || !strings.Contains(rows[0], "c-log-row--found_in_set") {
		t.Errorf("?type=found_in_set should return only the found_in_set row, got %d rows\n---\n%s", len(rows), foundOnly)
	}

	kindOnly := fetchLog(t, deps, "type=kind_changed")
	rows = splitLogRows(kindOnly)
	if len(rows) != 1 || !strings.Contains(rows[0], "c-log-row--kind_changed") {
		t.Errorf("?type=kind_changed should return only the kind_changed row, got %d rows\n---\n%s", len(rows), kindOnly)
	}
}
