package handlers_test

import (
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

func TestPlan_RowDescriptionRendersProseBlocks(t *testing.T) {
	db := setupLogTestDB(t)
	mustAddWithDesc(t, db, "alice", "with desc", proseDesc, nil, nil)
	deps := newLogDeps(t, db)
	body := fetchPlanAll(t, deps)

	mustContain(t, body, `<div class="c-plan-row__desc c-prose"><p>first line of the description wrapped by the author</p><ul><li>bullet one</li><li>bullet two</li></ul></div>`)
}
