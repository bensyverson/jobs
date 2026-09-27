package handlers_test

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/assets"
	"github.com/bensyverson/jobs/internal/web/handlers"
	"github.com/bensyverson/jobs/internal/web/templates"
)

func setupPlanTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newPlanDeps(t *testing.T, db *sql.DB) handlers.Deps {
	t.Helper()
	m, err := assets.BuildManifest()
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	e, err := templates.New(m)
	if err != nil {
		t.Fatalf("templates.New: %v", err)
	}
	return handlers.Deps{DB: db, Templates: e}
}

// mustAddWithDesc mirrors mustAdd but lets the test set a description.
// Separate helper so the existing log-test mustAdd signature stays
// narrow — most tests don't care about descriptions.
func mustAddWithDesc(t *testing.T, db *sql.DB, actor, title, desc string, parent *string, labels []string) string {
	t.Helper()
	parentID := ""
	if parent != nil {
		parentID = *parent
	}
	res, err := job.RunAdd(db, parentID, title, desc, "", labels, actor)
	if err != nil {
		t.Fatalf("RunAdd(%q, %q): %v", actor, title, err)
	}
	return res.ShortID
}

func fetchPlan(t *testing.T, deps handlers.Deps, query string) string {
	t.Helper()
	url := "/plan"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest("GET", url, nil)
	w := httptest.NewRecorder()
	handlers.Plan(deps).ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("GET %s: status %d, body=%s", url, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestPlan_RendersTreeWithRootAndChildren(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAdd(t, db, "claude", "Root task", nil, []string{"web"})
	_ = mustAdd(t, db, "claude", "Child one", &root, nil)
	_ = mustAdd(t, db, "claude", "Child two", &root, []string{"tests"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `c-plan-row`)
	mustContain(t, body, `Root task`)
	mustContain(t, body, `Child one`)
	mustContain(t, body, `Child two`)
	// Children are nested inside a subtree container.
	mustContain(t, body, `c-plan-subtree`)
	// Labels render as label pills on the row.
	mustContain(t, body, `data-label="web"`)
	mustContain(t, body, `data-label="tests"`)
	// Each label pill carries an inline --label-color so colors.js
	// doesn't have to repaint on DOMContentLoaded (no flash on /plan).
	mustContain(t, body, `style="--label-color: hsl(212 40% 50%)"`)
	mustContain(t, body, `style="--label-color: hsl(141 40% 50%)"`)
	// The short id renders as a link to the task detail page.
	mustContain(t, body, `href="/tasks/`+root+`"`)
}

// Helper: most tests pre-date the archive filter and seed their DBs
// with done/canceled roots that would be hidden by default. They use
// fetchPlanAll to bypass the archive partition and assert against the
// full forest.
func fetchPlanAll(t *testing.T, deps handlers.Deps) string {
	return fetchPlan(t, deps, "show=all")
}

func TestPlan_StatusPillsReflectTaskState(t *testing.T) {
	db := setupPlanTestDB(t)
	// available (todo) root
	todoID := mustAdd(t, db, "claude", "Todo task", nil, nil)
	_ = todoID
	// active: claimed
	activeID := mustAdd(t, db, "claude", "Active task", nil, nil)
	mustClaim(t, db, activeID, "claude")
	// done
	doneID := mustAdd(t, db, "claude", "Done task", nil, nil)
	if _, _, err := job.RunDone(db, []string{doneID}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlanAll(t, deps)

	mustContain(t, body, `c-status-pill--todo`)
	mustContain(t, body, `c-status-pill--active`)
	mustContain(t, body, `c-status-pill--done`)
}

func TestPlan_BlockedRowShowsBlockedByAffordance(t *testing.T) {
	db := setupPlanTestDB(t)
	blockerID := mustAdd(t, db, "claude", "Blocker title to hover", nil, nil)
	blockedID := mustAdd(t, db, "claude", "Blocked task", nil, nil)
	if err := job.RunBlock(db, blockedID, blockerID, "claude"); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `c-status-pill--blocked`)
	mustContain(t, body, `c-plan-row__blocked-by`)
	mustContain(t, body, `Blocked by`)
	// Blocker link is an in-page anchor to the blocker's row, not a
	// navigation to /tasks/<id>. Keeps the reader on the plan document.
	mustContain(t, body, `href="#task-`+blockerID+`"`)
	// And the full blocker title is exposed via the hover tooltip so
	// users can parse "Blocked by <id>" without navigation — still useful
	// when the blocker lives inside a currently-collapsed subtree.
	mustContain(t, body, `title="Blocker title to hover"`)
}

func TestPlan_EveryRowCarriesAnchorID(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAdd(t, db, "claude", "Root", nil, nil)
	child := mustAdd(t, db, "claude", "Child", &root, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `id="task-`+root+`"`)
	mustContain(t, body, `id="task-`+child+`"`)
}

func TestPlan_AutoCollapsesFullyDoneSubtree(t *testing.T) {
	db := setupPlanTestDB(t)
	activeRoot := mustAdd(t, db, "claude", "Active root", nil, nil)
	_ = mustAdd(t, db, "claude", "Open child", &activeRoot, nil)

	doneRoot := mustAdd(t, db, "claude", "Done root", nil, nil)
	doneChild := mustAdd(t, db, "claude", "Done child", &doneRoot, nil)
	if _, _, err := job.RunDone(db, []string{doneChild}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone child: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{doneRoot}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone root: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlanAll(t, deps)

	// Active root's subtree is expanded by default.
	mustContain(t, body, `data-plan-task="`+activeRoot+`" data-collapsed="false"`)
	// Done root's subtree is collapsed by default (no open descendants).
	mustContain(t, body, `data-plan-task="`+doneRoot+`" data-collapsed="true"`)
}

func TestPlan_ParentRollsUpToActiveWhenDescendantIsClaimed(t *testing.T) {
	db := setupPlanTestDB(t)
	// Parent is available (todo in its own right); a grandchild is
	// claimed. The plan view should render the parent as active so the
	// tree shows at a glance where the work is happening.
	parent := mustAdd(t, db, "claude", "Parent", nil, nil)
	child := mustAdd(t, db, "claude", "Child", &parent, nil)
	grandchild := mustAdd(t, db, "claude", "Grandchild", &child, nil)
	mustClaim(t, db, grandchild, "claude")

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// Parent and child rows roll up to active. Matched via a small
	// helper so the assertion tolerates attribute reshuffles on the row
	// (id, data-plan-task, data-collapsed all share the opening tag).
	assertRowHasClass(t, body, parent, "c-plan-row--status-active")
	assertRowHasClass(t, body, child, "c-plan-row--status-active")
}

func TestPlan_RollupDoesNotOverrideDoneParent(t *testing.T) {
	db := setupPlanTestDB(t)
	// Degenerate but possible: a done parent with a reopened descendant.
	// The done parent's own status should win — rollup doesn't resurrect
	// a closed branch visually.
	parent := mustAdd(t, db, "claude", "Done parent", nil, nil)
	child := mustAdd(t, db, "claude", "Reopened child", &parent, nil)
	// Closed by hand with --cascade: a parent that merely auto-closed would
	// reopen along with its child.
	if _, _, err := job.RunDone(db, []string{parent}, true, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone parent --cascade: %v", err)
	}
	if _, err := job.RunReopen(db, child, false, "claude"); err != nil {
		t.Fatalf("RunReopen child: %v", err)
	}
	mustClaim(t, db, child, "claude")

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	assertRowHasClass(t, body, parent, "c-plan-row--status-done")
}

func TestPlan_NotesRenderInCollapsibleDetails(t *testing.T) {
	db := setupPlanTestDB(t)
	id := mustAdd(t, db, "claude", "Task with a note", nil, nil)
	if err := job.RunNote(db, id, "progress check-in text", nil, "claude"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// Notes live in a <details> block, collapsed by default (no open attr).
	mustContain(t, body, `<details class="c-plan-notes-group"`)
	if strings.Contains(body, `<details class="c-plan-notes-group" open`) {
		t.Errorf("notes <details> should be collapsed by default (no open attribute)")
	}
	mustContain(t, body, `progress check-in text`)
	mustContain(t, body, `c-plan-note`)
}

func TestPlan_LeafWithDescriptionIsCollapsible(t *testing.T) {
	db := setupPlanTestDB(t)
	// A leaf task with a description is "technically still collapsible"
	// because the description is part of what collapses. A done leaf
	// should start collapsed so the page stays skimmable.
	active := mustAddWithDesc(t, db, "claude", "Active leaf", "An open description.", nil, nil)
	done := mustAddWithDesc(t, db, "claude", "Done leaf", "A closed description.", nil, nil)
	if _, _, err := job.RunDone(db, []string{done}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlanAll(t, deps)

	// Active leaf with a description: collapsible but expanded.
	mustContain(t, body, `data-plan-task="`+active+`" data-collapsed="false"`)
	// Done leaf with a description: collapsible and collapsed.
	mustContain(t, body, `data-plan-task="`+done+`" data-collapsed="true"`)
	// Description still rendered in the DOM — CSS hides it when collapsed.
	mustContain(t, body, `A closed description.`)
}

func TestPlan_RowWithoutDescriptionOrChildrenHasNoDisclosure(t *testing.T) {
	db := setupPlanTestDB(t)
	id := mustAdd(t, db, "claude", "Bare leaf", nil, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// A row with nothing to hide has no data-collapsed attribute and
	// no disclosure button.
	if strings.Contains(body, `data-plan-task="`+id+`" data-collapsed=`) {
		t.Errorf("bare leaf %q should not carry data-collapsed (nothing to hide)", id)
	}
	// Isolate the single row's markup and confirm no disclosure button.
	idx := strings.Index(body, `data-plan-task="`+id+`"`)
	if idx == -1 {
		t.Fatalf("row %q not rendered", id)
	}
	rowEnd := strings.Index(body[idx:], "</div>")
	fragment := body[idx : idx+rowEnd]
	if strings.Contains(fragment, `c-plan-row__disclosure`) {
		t.Errorf("bare leaf %q should not render a disclosure button, got:\n%s", id, fragment)
	}
}

func TestPlan_EmptyDatabaseRendersQuietPlaceholder(t *testing.T) {
	db := setupPlanTestDB(t)
	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	mustContain(t, body, `c-plan-empty`)
}

func TestPlan_FilterTabsRenderInChrome(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A task", nil, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// Active/Archived/All tabs (inert in p4-ssr; wired in p4-archive).
	mustContain(t, body, `Active`)
	mustContain(t, body, `Archived`)
	mustContain(t, body, `>All<`)
}

func TestPlan_LabelFilter_KeepsMatchingTasksAndAncestors(t *testing.T) {
	db := setupPlanTestDB(t)
	// Tree:
	//   rootA (no label) → childA1 (label=web) → leafA1a (no label)
	//   rootB (no label) → childB1 (label=other)
	// ?label=web keeps rootA (ancestor of a match), childA1 (match);
	// drops leafA1a (unlabeled descendant of a match is filtered too,
	// per the spec's "scopes the tree to tasks matching the label,
	// keeping ancestor chain visible for context"), rootB, childB1.
	rootA := mustAdd(t, db, "claude", "Root A", nil, nil)
	childA1 := mustAdd(t, db, "claude", "Child A1 web", &rootA, []string{"web"})
	leafA1a := mustAdd(t, db, "claude", "Leaf A1a", &childA1, nil)

	rootB := mustAdd(t, db, "claude", "Root B", nil, nil)
	childB1 := mustAdd(t, db, "claude", "Child B1 other", &rootB, []string{"other"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "label=web")

	mustContain(t, body, `id="task-`+rootA+`"`)
	mustContain(t, body, `id="task-`+childA1+`"`)
	if strings.Contains(body, `id="task-`+leafA1a+`"`) {
		t.Errorf("leafA1a %q (unlabeled descendant of a match) should be filtered out by ?label=web", leafA1a)
	}
	if strings.Contains(body, `id="task-`+rootB+`"`) {
		t.Errorf("rootB %q should be filtered out by ?label=web", rootB)
	}
	if strings.Contains(body, `id="task-`+childB1+`"`) {
		t.Errorf("childB1 %q should be filtered out by ?label=web", childB1)
	}
}

func TestPlan_LabelStrip_AllPillActiveWhenNoFilter(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// Leftmost "All" pill carries the --active state when nothing is
	// selected, and points at /plan to clear filters.
	mustContain(t, body, `<a href="/plan" class="c-label-pill c-label-pill--all c-label-pill--active">any</a>`)
	// No data-label pill is active in this state.
	if strings.Contains(body, `c-label-pill--active" data-label=`) {
		t.Errorf("no filter → no data-label pill should be --active")
	}
}

func TestPlan_LabelStrip_AllPillNotActiveWhenFiltering(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "label=web")

	mustContain(t, body, `<a href="/plan" class="c-label-pill c-label-pill--all">any</a>`)
	// Toggling the active "web" pill removes web → href="/plan".
	mustContain(t, body, `<a href="/plan" class="c-label-pill c-label-pill--active" data-label="web" style="--label-color: hsl(212 40% 50%)">web</a>`)
}

func TestPlan_LabelStrip_TopFiveByOpenTaskFrequency(t *testing.T) {
	db := setupPlanTestDB(t)
	// Six labels with descending counts: a×6, b×5, c×4, d×3, e×2, f×1.
	// Strip should keep a-e and drop f.
	mustAddMany := func(label string, n int) {
		for i := range n {
			mustAdd(t, db, "claude", label+"-"+strconv.Itoa(i), nil, []string{label})
		}
	}
	mustAddMany("a", 6)
	mustAddMany("b", 5)
	mustAddMany("c", 4)
	mustAddMany("d", 3)
	mustAddMany("e", 2)
	mustAddMany("f", 1)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	strip := extractFilterBar(t, body)
	for _, want := range []string{"a", "b", "c", "d", "e"} {
		if !strings.Contains(strip, `data-label="`+want+`"`) {
			t.Errorf("strip should include top-5 label %q", want)
		}
	}
	if strings.Contains(strip, `data-label="f"`) {
		t.Errorf("strip should not include 6th-most-frequent label %q", "f")
	}
}

func TestPlan_LabelStrip_SelectedLabelOutsideTopFiveStillAppears(t *testing.T) {
	db := setupPlanTestDB(t)
	// Same shape as TopFive — but ?label=f should pull "f" into the
	// strip even though it's outside the top-5, so the selection isn't
	// orphaned.
	mustAddMany := func(label string, n int) {
		for i := range n {
			mustAdd(t, db, "claude", label+"-"+strconv.Itoa(i), nil, []string{label})
		}
	}
	for _, l := range []string{"a", "b", "c", "d", "e"} {
		mustAddMany(l, 6)
	}
	mustAddMany("f", 1)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "label=f")

	mustContain(t, body, `c-label-pill c-label-pill--active" data-label="f"`)
}

func TestPlan_LabelStrip_StripPillTogglesSelection(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})
	_ = mustAdd(t, db, "claude", "B", nil, []string{"dashboard"})

	deps := newPlanDeps(t, db)

	// With ?label=web,dashboard, the web strip pill removes web.
	body := fetchPlan(t, deps, "label=web,dashboard")
	mustContain(t, body, `<a href="/plan?label=dashboard" class="c-label-pill c-label-pill--active" data-label="web" style="--label-color: hsl(212 40% 50%)">web</a>`)
	mustContain(t, body, `<a href="/plan?label=web" class="c-label-pill c-label-pill--active" data-label="dashboard" style="--label-color: hsl(162 40% 50%)">dashboard</a>`)

	// With ?label=web alone, the dashboard strip pill adds dashboard.
	body = fetchPlan(t, deps, "label=web")
	mustContain(t, body, `<a href="/plan?label=dashboard,web" class="c-label-pill" data-label="dashboard" style="--label-color: hsl(162 40% 50%)">dashboard</a>`)
}

func TestPlan_LabelFilter_MultiSelectIsORSemantic(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "Web only", nil, []string{"web"})
	_ = mustAdd(t, db, "claude", "Dashboard only", nil, []string{"dashboard"})
	_ = mustAdd(t, db, "claude", "Other only", nil, []string{"other"})

	deps := newPlanDeps(t, db)
	body := stripInitialFrame(fetchPlan(t, deps, "label=web,dashboard"))

	// Both web-only and dashboard-only tasks should be visible (OR);
	// other-labeled tasks should drop out.
	mustContain(t, body, `Web only`)
	mustContain(t, body, `Dashboard only`)
	if strings.Contains(body, `Other only`) {
		t.Errorf("?label=web,dashboard should exclude an other-only task")
	}
}

func TestPlan_InlineLabelPillIsClickableAndAddsLabel(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})
	_ = mustAdd(t, db, "claude", "B", nil, []string{"css"})

	deps := newPlanDeps(t, db)

	// With no filter: inline pill on row A points at /plan?label=web.
	body := fetchPlan(t, deps, "")
	mustContain(t, body, `<a href="/plan?label=web" class="c-label-pill" data-label="web" style="--label-color: hsl(212 40% 50%)">web</a>`)

	// With ?label=web: inline pill on row B (label=css) adds css to
	// the existing selection.
	body = fetchPlan(t, deps, "label=web")
	mustContain(t, body, `<a href="/plan?label=css,web" class="c-label-pill" data-label="css" style="--label-color: hsl(319 40% 50%)">css</a>`)
}

func TestPlan_LabelFilter_UnknownLabelShowsEmptyState(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "label=nonesuch")

	mustContain(t, body, `c-plan-empty`)
}

func TestPlan_ShowFilter_ActiveIsDefaultAndHidesArchivedRoots(t *testing.T) {
	db := setupPlanTestDB(t)
	activeRoot := mustAdd(t, db, "claude", "Active root", nil, nil)
	_ = mustAdd(t, db, "claude", "Open child", &activeRoot, nil)

	archivedRoot := mustAdd(t, db, "claude", "Archived root", nil, nil)
	child := mustAdd(t, db, "claude", "Done child", &archivedRoot, nil)
	if _, _, err := job.RunDone(db, []string{child}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone child: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{archivedRoot}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone root: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "") // default = show=active

	mustContain(t, body, `id="task-`+activeRoot+`"`)
	if strings.Contains(body, `id="task-`+archivedRoot+`"`) {
		t.Errorf("default ?show=active should hide fully-done root %q", archivedRoot)
	}
}

func TestPlan_ShowFilter_ArchivedShowsOnlyArchivedRoots(t *testing.T) {
	db := setupPlanTestDB(t)
	activeRoot := mustAdd(t, db, "claude", "Active root", nil, nil)
	_ = mustAdd(t, db, "claude", "Open child", &activeRoot, nil)

	archivedRoot := mustAdd(t, db, "claude", "Archived root", nil, nil)
	if _, _, err := job.RunDone(db, []string{archivedRoot}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "show=archived")

	mustContain(t, body, `id="task-`+archivedRoot+`"`)
	if strings.Contains(body, `id="task-`+activeRoot+`"`) {
		t.Errorf("?show=archived should hide open root %q", activeRoot)
	}
}

func TestPlan_ShowFilter_AllShowsBoth(t *testing.T) {
	db := setupPlanTestDB(t)
	activeRoot := mustAdd(t, db, "claude", "Active root", nil, nil)
	archivedRoot := mustAdd(t, db, "claude", "Archived root", nil, nil)
	if _, _, err := job.RunDone(db, []string{archivedRoot}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "show=all")

	mustContain(t, body, `id="task-`+activeRoot+`"`)
	mustContain(t, body, `id="task-`+archivedRoot+`"`)
}

func TestPlan_ShowFilter_TabsAreWiredAndMarkTheActiveMode(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, nil)

	deps := newPlanDeps(t, db)

	// Default (active): Active tab is active, others are plain.
	body := fetchPlan(t, deps, "")
	mustContain(t, body, `<a href="/plan" class="c-tab c-tab--active">Active</a>`)
	mustContain(t, body, `<a href="/plan?show=archived" class="c-tab">Archived</a>`)
	mustContain(t, body, `<a href="/plan?show=all" class="c-tab">All</a>`)

	// ?show=archived: Archived is active.
	body = fetchPlan(t, deps, "show=archived")
	mustContain(t, body, `<a href="/plan" class="c-tab">Active</a>`)
	mustContain(t, body, `<a href="/plan?show=archived" class="c-tab c-tab--active">Archived</a>`)

	// ?show=all: All is active.
	body = fetchPlan(t, deps, "show=all")
	mustContain(t, body, `<a href="/plan?show=all" class="c-tab c-tab--active">All</a>`)
}

func TestPlan_ShowFilter_PreservesLabelSelectionOnTabSwitch(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "label=web")

	// Switching to archived should keep ?label=web; switching to active
	// clears ?show= (since active is the default).
	mustContain(t, body, `<a href="/plan?label=web&amp;show=archived" class="c-tab">Archived</a>`)
	mustContain(t, body, `<a href="/plan?label=web&amp;show=all" class="c-tab">All</a>`)
}

func TestPlan_ShowFilter_LabelPillPreservesShowParam(t *testing.T) {
	db := setupPlanTestDB(t)
	_ = mustAdd(t, db, "claude", "A", nil, []string{"web"})
	doneID := mustAdd(t, db, "claude", "B", nil, []string{"other"})
	if _, _, err := job.RunDone(db, []string{doneID}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "show=all")

	// The "any" pill on show=all should link to /plan?show=all (clears
	// labels, keeps show). Other label pills should carry show=all too.
	mustContain(t, body, `<a href="/plan?show=all" class="c-label-pill c-label-pill--all c-label-pill--active">any</a>`)
	// A label pill should propagate show=all in its toggle URL.
	if !strings.Contains(body, `?label=web&amp;show=all`) && !strings.Contains(body, `?label=other&amp;show=all`) {
		t.Errorf("label pill toggle URL should preserve show=all")
	}
}

// TestPlan_FilterComposition is the broad table-driven check that
// ?label= and ?show= compose correctly. Each case names the visible
// roots (short IDs) it expects to find in the body and asserts every
// other root is absent. Catches regressions where a future filter
// short-circuits the wrong branch in the chain. (?at= time-travel
// composition lands with p8 and adds rows to this table.)
func TestPlan_FilterComposition(t *testing.T) {
	db := setupPlanTestDB(t)
	rootA := mustAdd(t, db, "claude", "Root A active web", nil, []string{"web"})
	_ = mustAdd(t, db, "claude", "Child A1 css", &rootA, []string{"css"})
	rootB := mustAdd(t, db, "claude", "Root B archived web", nil, []string{"web"})
	if _, _, err := job.RunDone(db, []string{rootB}, false, "", nil, "claude", false, ""); err != nil {
		t.Fatalf("RunDone B: %v", err)
	}
	rootC := mustAdd(t, db, "claude", "Root C active other", nil, []string{"other"})

	all := []string{rootA, rootB, rootC}

	deps := newPlanDeps(t, db)

	cases := []struct {
		name    string
		query   string
		visible []string
	}{
		{"default active", "", []string{rootA, rootC}},
		{"show=archived", "show=archived", []string{rootB}},
		{"show=all", "show=all", []string{rootA, rootB, rootC}},
		{"label=web (default active)", "label=web", []string{rootA}},
		{"label=web + show=archived", "label=web&show=archived", []string{rootB}},
		{"label=web + show=all", "label=web&show=all", []string{rootA, rootB}},
		{"label=other (default active)", "label=other", []string{rootC}},
		{"label=other + show=archived", "label=other&show=archived", nil},
		{"label=web,other (default active)", "label=web,other", []string{rootA, rootC}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := fetchPlan(t, deps, c.query)
			present := make(map[string]bool)
			for _, id := range all {
				if strings.Contains(body, `id="task-`+id+`"`) {
					present[id] = true
				}
			}
			wantSet := make(map[string]bool)
			for _, id := range c.visible {
				wantSet[id] = true
				if !present[id] {
					t.Errorf("expected %q visible, was absent", id)
				}
			}
			for _, id := range all {
				if !wantSet[id] && present[id] {
					t.Errorf("expected %q absent, was visible", id)
				}
			}
		})
	}
}

// TestPlan_RecursiveRenderingStructure exercises depth-3 nesting and
// confirms the recursive plan-node template produces matching nested
// c-plan-subtree wrappers, indentation-correct ids, and a per-row
// disclosure where (and only where) it has a purpose.
func TestPlan_RecursiveRenderingStructure(t *testing.T) {
	db := setupPlanTestDB(t)
	root := mustAddWithDesc(t, db, "claude", "Depth 0 root", "with description", nil, nil)
	child := mustAddWithDesc(t, db, "claude", "Depth 1 child", "", &root, nil)
	grand := mustAddWithDesc(t, db, "claude", "Depth 2 leaf", "", &child, nil)

	deps := newPlanDeps(t, db)
	body := fetchPlan(t, deps, "")

	// Three rows.
	for _, id := range []string{root, child, grand} {
		mustContain(t, body, `id="task-`+id+`"`)
	}
	// Two nested c-plan-subtree wrappers (root→child, child→grand).
	if got := strings.Count(body, `class="c-plan-subtree"`); got != 2 {
		t.Errorf("expected 2 c-plan-subtree wrappers (root→child, child→grand), got %d", got)
	}
	// Root is collapsible (description) → has disclosure button. Child
	// is collapsible (has children) → also has disclosure. Grand is a
	// bare leaf → no disclosure.
	rootHas := rowHasDisclosure(t, body, root)
	childHas := rowHasDisclosure(t, body, child)
	grandHas := rowHasDisclosure(t, body, grand)
	if !rootHas {
		t.Error("root should have a disclosure button (description present)")
	}
	if !childHas {
		t.Error("child should have a disclosure button (has children)")
	}
	if grandHas {
		t.Error("grand-leaf should not have a disclosure button (no children, no description)")
	}
	// Heading levels: depth-0 → t-heading-lg, depth-1 → t-heading-md,
	// deeper rows use default body weight.
	mustContain(t, body, `t-heading-lg">Depth 0 root`)
	mustContain(t, body, `t-heading-md">Depth 1 child`)
	if strings.Contains(body, `t-heading">Depth 2 leaf`) ||
		strings.Contains(body, `t-heading-lg">Depth 2 leaf`) ||
		strings.Contains(body, `t-heading-md">Depth 2 leaf`) {
		t.Errorf("depth-2 leaf should not carry a heading-class modifier")
	}
}

// rowHasDisclosure returns whether the row tagged with the given
// short id renders a c-plan-row__disclosure button. Scoped to just
// the row's outer markup so a sibling's disclosure doesn't pollute
// the result.
func rowHasDisclosure(t *testing.T, body, shortID string) bool {
	t.Helper()
	marker := `id="task-` + shortID + `"`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("row %q not found", shortID)
	}
	// Bracket the row's opening tag and its title-line; the disclosure
	// is the row's first child if present.
	tail := body[idx:]
	before, _, ok := strings.Cut(tail, `<div class="c-plan-row__title">`)
	if !ok {
		return false
	}
	return strings.Contains(before, `c-plan-row__disclosure`)
}

// extractFilterBar returns just the markup inside the plan view's
// <section class="c-filter-bar">…</section>. Lets strip-only tests
// avoid matching the inline label pills that decorate task rows.
func extractFilterBar(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<section class="c-filter-bar"`)
	if start == -1 {
		t.Fatalf("c-filter-bar section not found in body")
	}
	end := strings.Index(body[start:], `</section>`)
	if end == -1 {
		t.Fatalf("filter-bar section is not closed")
	}
	return body[start : start+end]
}

// assertRowHasClass finds the row tagged with data-plan-task="<shortID>"
// and confirms its class attribute contains `wantClass`. Resilient to
// attribute reordering inside the opening <div> tag.
func assertRowHasClass(t *testing.T, body, shortID, wantClass string) {
	t.Helper()
	marker := `data-plan-task="` + shortID + `"`
	idx := strings.Index(body, marker)
	if idx == -1 {
		t.Fatalf("row %q not found in body", shortID)
	}
	tagStart := strings.LastIndex(body[:idx], "<div ")
	tagEnd := strings.Index(body[idx:], ">")
	if tagStart == -1 || tagEnd == -1 {
		t.Fatalf("could not bracket row %q opening tag", shortID)
	}
	opening := body[tagStart : idx+tagEnd+1]
	if !strings.Contains(opening, wantClass) {
		t.Errorf("row %q missing class %q in opening tag:\n%s", shortID, wantClass, opening)
	}
}
