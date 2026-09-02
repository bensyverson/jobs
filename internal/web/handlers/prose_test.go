package handlers_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/job"
)

// Descriptions and notes are markdown prose: hard-wrapped lines reflow into
// one paragraph, blank lines separate paragraphs, bullets become a list.
// Every narrative surface — task page, peek sheet, plan row — renders the
// same blocks through the `prose` template func.

const proseDesc = "first line of the description\nwrapped by the author\n\n- bullet one\n- bullet two"

func TestTask_DescriptionRendersProseBlocks(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAddWithDesc(t, db, "alice", "with desc", proseDesc, nil, nil)
	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchTask(t, deps, id).Body.String())

	mustContain(t, body, `<div class="c-note c-prose"><p>first line of the description wrapped by the author</p><ul><li>bullet one</li><li>bullet two</li></ul></div>`)
}

func TestTask_NotesRenderProseBlocks(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAdd(t, db, "alice", "noted task", nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunNote(db, id, "one\ntwo\n\nthree", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{id}, false, "wrap up\nsecond line", nil, "alice", false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchTask(t, deps, id).Body.String())

	mustContain(t, body, `<div class="c-progress-note__body c-prose"><p>one two</p><p>three</p></div>`)
	mustContain(t, body, `<div class="c-note c-prose"><p>wrap up second line</p></div>`)
}

func TestTask_ProseEscapesHTML(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAddWithDesc(t, db, "alice", "hostile", "<script>alert(1)</script>", nil, nil)
	deps := newLogDeps(t, db)
	body := fetchTask(t, deps, id).Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatalf("raw HTML passed through the description:\n%s", body)
	}
	mustContain(t, body, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestPeek_DescriptionAndNotesRenderProseBlocks(t *testing.T) {
	db := setupLogTestDB(t)
	id := mustAddWithDesc(t, db, "alice", "with desc", proseDesc, nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunNote(db, id, "one\ntwo", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, id)

	mustContain(t, body, `<div class="c-note c-prose"><p>first line of the description wrapped by the author</p><ul><li>bullet one</li><li>bullet two</li></ul></div>`)
	mustContain(t, body, `<div class="c-progress-note__body c-prose"><p>one two</p></div>`)
}

// --- inline pass: code spans, links, and id autolinks ---

// proseLinkFixture creates a task carrying one criterion, plus a second
// task whose description mentions the first task's id bare, the criterion's
// id inside a code span, and an id-shaped word that names nothing. Returns
// the referenced task's id, its criterion's id, and the mentioning task's id.
func proseLinkFixture(t *testing.T, db *sql.DB) (referenced, criterion, mentioner string) {
	t.Helper()
	referenced = mustAdd(t, db, "alice", "the referenced task", nil, nil)
	if _, err := job.RunAddCriteria(db, referenced, []job.Criterion{{Label: "tests pass"}}, "alice"); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	task, err := job.GetTaskByShortID(db, referenced)
	if err != nil || task == nil {
		t.Fatalf("GetTaskByShortID(%q): %v", referenced, err)
	}
	crits, err := job.GetCriteria(db, task.ID)
	if err != nil || len(crits) != 1 {
		t.Fatalf("GetCriteria: %v (%d rows)", err, len(crits))
	}
	criterion = crits[0].ShortID

	desc := "blocked on " + referenced + " until `" + criterion + "` is green; banana is not a task"
	mentioner = mustAddWithDesc(t, db, "alice", "the mentioning task", desc, nil, nil)
	return referenced, criterion, mentioner
}

// proseLinkNeedles is what a rendered body must contain: the bare task id
// as a link, the backticked criterion id as a link wrapping the <code>, and
// the id-shaped word left alone.
func proseLinkNeedles(referenced, criterion string) []string {
	return []string{
		`blocked on <a href="/tasks/` + referenced + `">` + referenced + `</a> until `,
		`<a href="/tasks/` + referenced + `#crit-` + criterion + `"><code>` + criterion + `</code></a>`,
		` is green; banana is not a task`,
	}
}

func TestTask_DescriptionLinksRecognisedIDs(t *testing.T) {
	db := setupLogTestDB(t)
	referenced, criterion, mentioner := proseLinkFixture(t, db)
	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchTask(t, deps, mentioner).Body.String())

	mustContainAll(t, body, proseLinkNeedles(referenced, criterion)...)
	if strings.Contains(body, `>banana</a>`) {
		t.Errorf("an id-shaped word that names nothing was linked:\n%s", body)
	}
}

func TestTask_CriteriaRowsCarryAnAnchorID(t *testing.T) {
	db := setupLogTestDB(t)
	referenced, criterion, _ := proseLinkFixture(t, db)
	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchTask(t, deps, referenced).Body.String())

	mustContain(t, body, `id="crit-`+criterion+`"`)
}

func TestPeek_DescriptionLinksRecognisedIDs(t *testing.T) {
	db := setupLogTestDB(t)
	referenced, criterion, mentioner := proseLinkFixture(t, db)
	deps := newLogDeps(t, db)
	body := mustFetchPeek(t, deps, mentioner)

	mustContainAll(t, body, proseLinkNeedles(referenced, criterion)...)
}

func TestPlan_RowDescriptionLinksRecognisedIDs(t *testing.T) {
	db := setupLogTestDB(t)
	referenced, criterion, _ := proseLinkFixture(t, db)
	deps := newLogDeps(t, db)
	body := fetchPlanAll(t, deps)

	mustContainAll(t, body, proseLinkNeedles(referenced, criterion)...)
}

func TestTask_NoteLinksRecognisedIDs(t *testing.T) {
	db := setupLogTestDB(t)
	referenced := mustAdd(t, db, "alice", "the referenced task", nil, nil)
	id := mustAdd(t, db, "alice", "noted task", nil, nil)
	mustClaim(t, db, id, "alice")
	if err := job.RunNote(db, id, "picked this up from "+referenced+", see [the plan](/plan)", nil, "alice"); err != nil {
		t.Fatalf("RunNote: %v", err)
	}
	deps := newLogDeps(t, db)
	body := stripInitialFrame(fetchTask(t, deps, id).Body.String())

	mustContainAll(t, body,
		`<a href="/tasks/`+referenced+`">`+referenced+`</a>`,
		`<a href="/plan">the plan</a>`,
	)
}

func TestPlan_RowDescriptionRendersProseBlocks(t *testing.T) {
	db := setupLogTestDB(t)
	mustAddWithDesc(t, db, "alice", "with desc", proseDesc, nil, nil)
	deps := newLogDeps(t, db)
	body := fetchPlanAll(t, deps)

	mustContain(t, body, `<div class="c-plan-row__desc c-prose"><p>first line of the description wrapped by the author</p><ul><li>bullet one</li><li>bullet two</li></ul></div>`)
}
