package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

// mustAddIssueRoot creates a root task and converts it to an issue
// tree, mirroring `job add <title> --kind issue`.
func mustAddIssueRoot(t *testing.T, db *sql.DB, actor, title string, labels []string) string {
	t.Helper()
	res, err := job.RunAddKind(db, "", title, "", "", labels, actor, job.KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind(%q): %v", title, err)
	}
	return res.ShortID
}

// fetchIssues drives the Issues handler at a path, optionally with an
// {id} path value bound (the scoped route). Returns status + body so
// the 404 cases can assert without a t.Fatal in the helper.
func fetchIssues(t *testing.T, deps handlers.Deps, path, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if id != "" {
		req.SetPathValue("id", id)
	}
	w := httptest.NewRecorder()
	handlers.Issues(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func fetchPlanPath(t *testing.T, deps handlers.Deps, path, id string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if id != "" {
		req.SetPathValue("id", id)
	}
	w := httptest.NewRecorder()
	handlers.Plan(deps).ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func mustNotContainStr(t *testing.T, body, needle string) {
	t.Helper()
	if strings.Contains(body, needle) {
		t.Fatalf("body unexpectedly contains %q", needle)
	}
}

// mainOnly slices the rendered <main> out of a page. The layout also
// ships the whole forest as the scrubber's initial-frame JSON island,
// so "this tree is not on this page" has to be asserted against the
// rendered region, not the raw body.
func mainOnly(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<main id="main"`)
	end := strings.Index(body, "</main>")
	if start < 0 || end < start {
		t.Fatalf("no <main> in body")
	}
	return body[start:end]
}

// --- kind partition ---

func TestPlan_ExcludesIssueRoots(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Ship the docs site", nil, nil)
	issue := mustAddIssueRoot(t, db, "claude", "Issues", nil)
	_ = mustAdd(t, db, "claude", "orient panics on empty tree", &issue, nil)

	deps := newPlanDeps(t, db)
	main := mainOnly(t, fetchPlan(t, deps, ""))

	mustContain(t, main, "Ship the docs site")
	mustNotContainStr(t, main, "Issues</span>")
	mustNotContainStr(t, main, "orient panics on empty tree")
}

func TestIssues_RendersOnlyIssueRoots(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Ship the docs site", nil, nil)
	issue := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	_ = mustAdd(t, db, "claude", "orient panics on empty tree", &issue, nil)

	deps := newPlanDeps(t, db)
	code, body := fetchIssues(t, deps, "/issues", "")
	if code != 200 {
		t.Fatalf("GET /issues: status %d", code)
	}

	main := mainOnly(t, body)
	mustContain(t, main, "Bug pile")
	mustContain(t, main, "orient panics on empty tree")
	mustNotContainStr(t, main, "Ship the docs site")
}

func TestIssues_SectionCarriesViewAttributes(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAddIssueRoot(t, db, "claude", "Bug pile", nil)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")

	mustContain(t, body, `data-plan-view="issue"`)
	mustContain(t, body, `data-plan-base="/issues"`)
}

func TestPlan_SectionCarriesViewAttributes(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Ship the docs site", nil, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `data-plan-view="task"`)
	mustContain(t, body, `data-plan-base="/plan"`)
}

// --- meta line ---

func TestIssues_MetaLineCountsOpenAndClosedInSevenDays(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	_ = mustAdd(t, db, "claude", "Bug one", &root, nil)
	_ = mustAdd(t, db, "claude", "Bug two", &root, nil)
	closed := mustAdd(t, db, "claude", "Bug three", &root, nil)
	if _, _, err := job.RunDone(db, []string{closed}, false, "", nil, "claude", true, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")

	mustContain(t, body, "2 open · 1 closed in 7d")
}

func TestIssues_MetaLineIsAbsentFromPlan(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	_ = mustAdd(t, db, "claude", "Bug one", &root, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustNotContainStr(t, mainOnly(t, body), "closed in 7d")
}

// --- scoped views ---

func TestIssues_ScopedToRootShowsOnlyThatTree(t *testing.T) {
	db := setupPlanTestDB(t)
	a := mustAddIssueRoot(t, db, "claude", "Pile A", nil)
	_ = mustAdd(t, db, "claude", "Bug in A", &a, nil)
	b := mustAddIssueRoot(t, db, "claude", "Pile B", nil)
	_ = mustAdd(t, db, "claude", "Bug in B", &b, nil)

	deps := newPlanDeps(t, db)
	code, body := fetchIssues(t, deps, "/issues/"+a, a)
	if code != 200 {
		t.Fatalf("GET /issues/%s: status %d", a, code)
	}

	main := mainOnly(t, body)
	mustContain(t, main, "Bug in A")
	mustNotContainStr(t, main, "Bug in B")
	// Filter URLs stay inside the scope so a label click doesn't
	// silently widen the view back to every issue root.
	mustContain(t, body, `data-plan-base="/issues/`+a+`"`)
}

func TestIssues_ScopedRootHonoursLabelFilter(t *testing.T) {
	db := setupPlanTestDB(t)
	a := mustAddIssueRoot(t, db, "claude", "Pile A", nil)
	_ = mustAdd(t, db, "claude", "Parser bug", &a, []string{"parser"})
	_ = mustAdd(t, db, "claude", "Render bug", &a, []string{"render"})

	deps := newPlanDeps(t, db)
	code, body := fetchIssues(t, deps, "/issues/"+a+"?label=parser", a)
	if code != 200 {
		t.Fatalf("status %d", code)
	}

	main := mainOnly(t, body)
	mustContain(t, main, "Parser bug")
	mustNotContainStr(t, main, "Render bug")
}

func TestIssues_ScopedToUnknownRootIs404(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAddIssueRoot(t, db, "claude", "Pile A", nil)

	deps := newPlanDeps(t, db)
	code, _ := fetchIssues(t, deps, "/issues/zzzzz", "zzzzz")
	if code != 404 {
		t.Fatalf("GET /issues/zzzzz: status %d, want 404", code)
	}
}

func TestIssues_ScopedToTaskRootIs404(t *testing.T) {
	db := setupPlanTestDB(t)
	plan := mustAdd(t, db, "claude", "Ship the docs site", nil, nil)

	deps := newPlanDeps(t, db)
	code, _ := fetchIssues(t, deps, "/issues/"+plan, plan)
	if code != 404 {
		t.Fatalf("GET /issues/<task root>: status %d, want 404", code)
	}
}

func TestPlan_ScopedToRootShowsOnlyThatTree(t *testing.T) {
	db := setupPlanTestDB(t)
	a := mustAdd(t, db, "claude", "Plan A", nil, nil)
	_ = mustAdd(t, db, "claude", "Leaf in A", &a, nil)
	b := mustAdd(t, db, "claude", "Plan B", nil, nil)
	_ = mustAdd(t, db, "claude", "Leaf in B", &b, nil)

	deps := newPlanDeps(t, db)
	code, body := fetchPlanPath(t, deps, "/plan/"+a, a)
	if code != 200 {
		t.Fatalf("GET /plan/%s: status %d", a, code)
	}

	main := mainOnly(t, body)
	mustContain(t, main, "Leaf in A")
	mustNotContainStr(t, main, "Leaf in B")
}

// --- header tab ---

func TestHeader_IssuesTabCarriesOpenCountOnEveryPage(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	_ = mustAdd(t, db, "claude", "Bug one", &root, nil)
	_ = mustAdd(t, db, "claude", "Bug two", &root, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `href="/issues"`)
	mustContain(t, body, `class="c-tab__count">2</span>`)
}

func TestHeader_IssuesTabHasNoCountWhenZeroOpen(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Ship the docs site", nil, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `href="/issues"`)
	mustNotContainStr(t, body, "c-tab__count")
}

func TestHeader_IssuesTabIsActiveOnIssues(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAddIssueRoot(t, db, "claude", "Bug pile", nil)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")

	mustContain(t, body, `href="/issues" class="c-tab c-tab--active"`)
}

// --- chrome parameterisation ---

func TestIssues_FilterChromeTargetsIssuesBase(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	_ = mustAdd(t, db, "claude", "Parser bug", &root, []string{"parser"})

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")

	// Show tabs and label pills compose against /issues, not /plan.
	mustContain(t, body, `href="/issues?show=archived"`)
	mustContain(t, body, `href="/issues?label=parser"`)
	mustNotContainStr(t, body, `href="/plan?show=archived"`)
}

func TestIssues_EmptyDatabaseRendersQuietPlaceholder(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Ship the docs site", nil, nil)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")

	mustContain(t, body, "c-plan-empty")
	mustContain(t, body, "No open issues.")
}

// --- archive rule: per child on Issues, per root on Plan ---
//
// An issue root never closes on its own (the auto-close cascade is
// exempt from it), so a per-root archive rule leaves the Archived tab
// empty forever and piles every closed bug under Active. On Issues
// the unit of archiving is therefore each direct child subtree.

// mustCloseTask closes a task, failing the test on error.
func mustCloseTask(t *testing.T, db *sql.DB, shortID string) {
	t.Helper()
	if _, _, err := job.RunDone(db, []string{shortID}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone(%q): %v", shortID, err)
	}
}

// issuePileWithOneClosedChild seeds one issue root holding one open
// and one closed child, and returns the three short ids.
func issuePileWithOneClosedChild(t *testing.T, db *sql.DB) (root, open, closed string) {
	t.Helper()
	root = mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	open = mustAdd(t, db, "claude", "Open bug", &root, nil)
	closed = mustAdd(t, db, "claude", "Closed bug", &root, nil)
	mustCloseTask(t, db, closed)
	return root, open, closed
}

func TestIssues_ShowActive_HidesClosedChildrenAndKeepsTheRootRow(t *testing.T) {
	db := setupPlanTestDB(t)
	root, open, closed := issuePileWithOneClosedChild(t, db)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues", "")
	main := mainOnly(t, body)

	mustContain(t, main, `id="task-`+root+`"`)
	mustContain(t, main, `id="task-`+open+`"`)
	mustNotContainStr(t, main, `id="task-`+closed+`"`)
}

func TestIssues_ShowArchived_ShowsOnlyClosedChildrenUnderTheRoot(t *testing.T) {
	db := setupPlanTestDB(t)
	root, open, closed := issuePileWithOneClosedChild(t, db)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues?show=archived", "")
	main := mainOnly(t, body)

	mustContain(t, main, `id="task-`+root+`"`)
	mustContain(t, main, `id="task-`+closed+`"`)
	mustNotContainStr(t, main, `id="task-`+open+`"`)
}

func TestIssues_ShowAll_ShowsEveryChild(t *testing.T) {
	db := setupPlanTestDB(t)
	root, open, closed := issuePileWithOneClosedChild(t, db)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues?show=all", "")
	main := mainOnly(t, body)

	mustContain(t, main, `id="task-`+root+`"`)
	mustContain(t, main, `id="task-`+open+`"`)
	mustContain(t, main, `id="task-`+closed+`"`)
}

func TestIssues_ArchiveIsPerSubtreeNotPerChildRow(t *testing.T) {
	// A closed child with an open descendant still carries work, so it
	// belongs under Active — the same subtree rule Plan applies to a
	// root, one level down.
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	child := mustAdd(t, db, "claude", "Half-closed bug", &root, nil)
	grandchild := mustAdd(t, db, "claude", "Still open follow-up", &child, nil)
	// `done` refuses a task with open subtasks, so the mixed subtree is
	// built the way a person builds one: close both, then reopen the
	// follow-up.
	mustCloseTask(t, db, grandchild)
	mustCloseTask(t, db, child)
	if _, err := job.RunReopen(db, grandchild, false, "claude"); err != nil {
		t.Fatalf("RunReopen(%q): %v", grandchild, err)
	}

	deps := newPlanDeps(t, db)
	_, activeBody := fetchIssues(t, deps, "/issues", "")
	active := mainOnly(t, activeBody)
	mustContain(t, active, `id="task-`+child+`"`)
	mustContain(t, active, `id="task-`+grandchild+`"`)

	_, archivedBody := fetchIssues(t, deps, "/issues?show=archived", "")
	mustNotContainStr(t, mainOnly(t, archivedBody), `id="task-`+child+`"`)
}

func TestIssues_RootRowSurvivesWhenEveryChildFiltersOut(t *testing.T) {
	// The pile is the point: a root whose children all fall on the
	// other side of the tab still renders, so the reader sees an empty
	// archive under a named pile rather than an empty page.
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Bug pile", nil)
	open := mustAdd(t, db, "claude", "Open bug", &root, nil)

	deps := newPlanDeps(t, db)
	_, body := fetchIssues(t, deps, "/issues?show=archived", "")
	main := mainOnly(t, body)

	mustContain(t, main, `id="task-`+root+`"`)
	mustNotContainStr(t, main, `id="task-`+open+`"`)
	mustNotContainStr(t, main, "c-plan-empty")
}

func TestPlan_ArchiveRuleStaysPerRoot(t *testing.T) {
	// Plan is untouched: a root with any open work stays whole under
	// Active — closed children included — and never shows under
	// Archived.
	db := setupPlanTestDB(t)
	root := mustAdd(t, db, "claude", "Ship the docs site", nil, nil)
	open := mustAdd(t, db, "claude", "Open leaf", &root, nil)
	closed := mustAdd(t, db, "claude", "Closed leaf", &root, nil)
	mustCloseTask(t, db, closed)

	deps := newPlanDeps(t, db)
	active := mainOnly(t, fetchPlan(t, deps, ""))
	mustContain(t, active, `id="task-`+root+`"`)
	mustContain(t, active, `id="task-`+open+`"`)
	mustContain(t, active, `id="task-`+closed+`"`)

	archived := mainOnly(t, fetchPlan(t, deps, "show=archived"))
	mustNotContainStr(t, archived, `id="task-`+root+`"`)
	mustNotContainStr(t, archived, `id="task-`+closed+`"`)
}

// A pile closed outright — `job done` on the root, which the domain only
// allows once every child is closed — is archived as a unit, like a plan
// root. Otherwise it would sit on Active forever as an empty pile.
func TestIssues_ClosedRootIsArchivedAsAUnit(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddIssueRoot(t, db, "claude", "Old pile", nil)
	child := mustAdd(t, db, "claude", "Fixed bug", &root, nil)
	mustCloseTask(t, db, child)
	mustCloseTask(t, db, root)

	deps := newPlanDeps(t, db)
	_, active := fetchIssues(t, deps, "/issues", "")
	mustNotContainStr(t, mainOnly(t, active), `id="task-`+root+`"`)

	_, archived := fetchIssues(t, deps, "/issues?show=archived", "")
	main := mainOnly(t, archived)
	mustContain(t, main, `id="task-`+root+`"`)
	mustContain(t, main, `id="task-`+child+`"`)
}
