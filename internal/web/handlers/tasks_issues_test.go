package handlers_test

import (
	"database/sql"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// sectionOf returns the HTML of the c-section whose label heading is
// label, from the heading through the next </section>. Empty when the
// page has no such section — callers assert on that directly.
func sectionOf(t *testing.T, body, label string) string {
	t.Helper()
	head := `<h2 class="c-section__label t-label-caps">` + label + `</h2>`
	i := strings.Index(body, head)
	if i < 0 {
		return ""
	}
	rest := body[i:]
	before, _, ok := strings.Cut(rest, "</section>")
	if !ok {
		return rest
	}
	return before
}

func mustSetFoundIn(t *testing.T, db *sql.DB, task, source string) {
	t.Helper()
	if err := job.RunSetFoundIn(db, task, source, "alice"); err != nil {
		t.Fatalf("RunSetFoundIn(%q, %q): %v", task, source, err)
	}
}

// mustAddIssueRoot creates a root task and converts it to an
// issue-tree, returning its short id.
func TestTask_FoundIn_RendersLinkAndSourceStatus(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "The leaf that surfaced it", nil, nil)
	mustClaim(t, db, leaf, "alice")
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues", nil)
	bug := mustAdd(t, db, "alice", "Panics on empty tree", &issueRoot, nil)
	mustSetFoundIn(t, db, bug, leaf)

	deps := newLogDeps(t, db)
	body := fetchTask(t, deps, bug).Body.String()

	sec := sectionOf(t, body, "Found in")
	if sec == "" {
		t.Fatalf("no Found in section\n---\n%s", body)
	}
	mustContainAll(t, sec,
		`href="/tasks/`+leaf+`"`,
		leaf,
		"The leaf that surfaced it",
		"c-status-pill--active",
	)
}

func TestTask_NoFoundIn_OmitsSection(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "Plain task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchTask(t, deps, id).Body.String()
	if sectionOf(t, body, "Found in") != "" {
		t.Errorf("task with no found-in edge should not render a Found in section")
	}
}

func TestTask_Surfaced_RendersChecklistOfLinks(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "Working leaf", nil, nil)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues", nil)
	bugA := mustAdd(t, db, "alice", "First defect", &issueRoot, nil)
	bugB := mustAdd(t, db, "alice", "Second defect", &issueRoot, nil)
	mustSetFoundIn(t, db, bugA, leaf)
	mustSetFoundIn(t, db, bugB, leaf)
	mustClaim(t, db, bugB, "bob")

	deps := newLogDeps(t, db)
	body := fetchTask(t, deps, leaf).Body.String()

	sec := sectionOf(t, body, "Surfaced")
	if sec == "" {
		t.Fatalf("no Surfaced section\n---\n%s", body)
	}
	mustContainAll(t, sec,
		`role="list"`,
		`href="/tasks/`+bugA+`"`,
		`href="/tasks/`+bugB+`"`,
		"First defect",
		"Second defect",
		"c-status-pill--todo",
		"c-status-pill--active",
	)
}

func TestTask_NoSurfaced_OmitsSection(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "Plain task", nil, nil)

	deps := newLogDeps(t, db)
	body := fetchTask(t, deps, id).Body.String()
	if sectionOf(t, body, "Surfaced") != "" {
		t.Errorf("task that surfaced nothing should not render a Surfaced section")
	}
}

func TestTask_UnderIssueRoot_CarriesIssueVariant(t *testing.T) {
	db := setupLogTestDB(t)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues", nil)
	bug := mustAdd(t, db, "alice", "A defect", &issueRoot, nil)

	deps := newLogDeps(t, db)

	rootBody := fetchTask(t, deps, issueRoot).Body.String()
	mustContainAll(t, rootBody, "p-task--issue", "p-task__kind")

	childBody := fetchTask(t, deps, bug).Body.String()
	mustContainAll(t, childBody, "p-task--issue", "p-task__kind")
}

func TestTask_UnderTaskRoot_HasNoIssueVariant(t *testing.T) {
	db := setupLogTestDB(t)
	root := mustAdd(t, db, "alice", "A plan", nil, nil)
	leaf := mustAdd(t, db, "alice", "A leaf", &root, nil)

	deps := newLogDeps(t, db)
	for _, id := range []string{root, leaf} {
		body := fetchTask(t, deps, id).Body.String()
		if strings.Contains(body, "p-task--issue") {
			t.Errorf("task %s under a task root must not carry p-task--issue", id)
		}
		if !strings.Contains(body, `class="p-task"`) {
			t.Errorf("task %s should carry the base p-task class", id)
		}
		if strings.Contains(body, "p-task__kind") {
			t.Errorf("task %s under a task root must not render a kind marker", id)
		}
	}
}

func TestPeek_ShowsFoundInAndNotSurfaced(t *testing.T) {
	db := setupLogTestDB(t)
	leaf := mustAdd(t, db, "alice", "Working leaf", nil, nil)
	issueRoot := mustAddIssueRoot(t, db, "alice", "Issues", nil)
	bug := mustAdd(t, db, "alice", "A defect", &issueRoot, nil)
	mustSetFoundIn(t, db, bug, leaf)

	deps := newLogDeps(t, db)

	bugPeek := mustFetchPeek(t, deps, bug)
	mustContainAll(t, bugPeek, "Found in", `href="/tasks/`+leaf+`"`)

	leafPeek := mustFetchPeek(t, deps, leaf)
	if strings.Contains(leafPeek, "Surfaced") {
		t.Errorf("peek sheet must not render a Surfaced section\n---\n%s", leafPeek)
	}
}
