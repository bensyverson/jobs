package job

import (
	"database/sql"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Szx5CZ — The `orient:` header leads with the work: the target's
// description, criteria with text and state, and every note; then the leaves
// it is blocked by and the leaves it blocks, each with status and latest
// note; then one line per same-parent sibling leaf. Any prefix of the output
// carries the most useful context available at that length.

type headerFixture struct {
	root, parent, target, blocker, blocked, sibling, quiet, branch string
}

// seedHeaderFixture builds a parent with the target, a noted blocker, a leaf
// the target blocks, a noted sibling, a noteless sibling and a branch
// sibling (a container, which is not a sibling leaf).
func seedHeaderFixture(t *testing.T, db *sql.DB) headerFixture {
	t.Helper()
	var f headerFixture
	f.root = MustAdd(t, db, "", "Root")
	f.parent = MustAdd(t, db, f.root, "Parent")
	f.blocker = MustAdd(t, db, f.parent, "Blocker leaf")
	mustNotes(t, db, f.blocker, "blocker old note", "blocker latest note")
	f.target = MustAddDesc(t, db, f.parent, "Target leaf", "the target description")
	if _, err := RunAddCriteria(db, f.target, []Criterion{{Label: "target criterion one"}, {Label: "target criterion two"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunSetCriterion(db, f.target, "target criterion one", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	mustNotes(t, db, f.target, "target note one", "target note two", "target note three")
	f.blocked = MustAdd(t, db, f.parent, "Blocked leaf")
	mustNotes(t, db, f.blocked, "blocked latest note")
	f.sibling = MustAdd(t, db, f.parent, "Noted sibling")
	mustNotes(t, db, f.sibling, "sibling one", "sibling two")
	f.quiet = MustAdd(t, db, f.parent, "Quiet sibling")
	f.branch = MustAdd(t, db, f.parent, "Branch sibling")
	MustAdd(t, db, f.branch, "Branch child")
	if err := RunBlock(db, f.target, f.blocker, TestActor); err != nil {
		t.Fatalf("block target: %v", err)
	}
	if err := RunBlock(db, f.blocked, f.target, TestActor); err != nil {
		t.Fatalf("block blocked: %v", err)
	}
	return f
}

func TestRunOrient_Header_CarriesTargetInFull(t *testing.T) {
	db := SetupTestDB(t)
	f := seedHeaderFixture(t, db)

	view, err := RunOrient(db, f.target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	h := view.Header
	if h.Desc != "the target description" {
		t.Errorf("header Desc: got %q", h.Desc)
	}
	if len(h.Criteria) != 2 || h.Criteria[0].Label != "target criterion one" || h.Criteria[0].State != CriterionPassed ||
		h.Criteria[1].State != CriterionPending {
		t.Errorf("header Criteria: got %+v, want both criteria with their states", h.Criteria)
	}
	if got := noteTexts(h.Notes); len(got) != 3 || got[0] != "target note one" || got[2] != "target note three" {
		t.Errorf("header Notes: got %v, want all three in order", got)
	}
	if h.Status != "blocked" {
		t.Errorf("header Status: got %q, want blocked (the target has an open blocker)", h.Status)
	}
}

func TestRunOrient_Header_ListsDependenciesWithLatestNote(t *testing.T) {
	db := SetupTestDB(t)
	f := seedHeaderFixture(t, db)

	view, err := RunOrient(db, f.target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	h := view.Header
	if len(h.BlockedBy) != 1 {
		t.Fatalf("header BlockedBy: got %+v, want the one blocker", h.BlockedBy)
	}
	if b := h.BlockedBy[0]; b.ID != f.blocker || b.Title != "Blocker leaf" || b.Status != "available" || b.LatestNote != "blocker latest note" {
		t.Errorf("header BlockedBy[0]: got %+v", b)
	}
	if len(h.Blocks) != 1 {
		t.Fatalf("header Blocks: got %+v, want the one blocked leaf", h.Blocks)
	}
	if b := h.Blocks[0]; b.ID != f.blocked || b.Title != "Blocked leaf" || b.Status != "blocked" || b.LatestNote != "blocked latest note" {
		t.Errorf("header Blocks[0]: got %+v", b)
	}
}

func TestRunOrient_Header_ListsSiblingLeaves(t *testing.T) {
	db := SetupTestDB(t)
	f := seedHeaderFixture(t, db)

	view, err := RunOrient(db, f.target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	got := map[string]OrientSibling{}
	for _, s := range view.Header.Siblings {
		got[s.ID] = s
	}
	if _, ok := got[f.target]; ok {
		t.Errorf("the target is not its own sibling: %+v", view.Header.Siblings)
	}
	if _, ok := got[f.branch]; ok {
		t.Errorf("a branch sibling is not a sibling leaf: %+v", view.Header.Siblings)
	}
	if s := got[f.sibling]; s.Title != "Noted sibling" || s.Status != "available" || s.Notes != 2 {
		t.Errorf("noted sibling: got %+v, want title, available, 2 notes", s)
	}
	if s, ok := got[f.quiet]; !ok || s.Notes != 0 {
		t.Errorf("quiet sibling: got %+v (present=%v), want listed with 0 notes", s, ok)
	}
	if len(view.Header.Siblings) != 4 {
		t.Errorf("siblings: got %d, want 4 (blocker, blocked, noted, quiet)", len(view.Header.Siblings))
	}

	out := renderOrient(t, view)
	header := out[:strings.Index(out, "\ntasks:")]
	if strings.Contains(header, "weigh_notes") || strings.Contains(header, "own_notes") {
		t.Errorf("weigh_notes and own_notes are retired:\n%s", header)
	}
	line := "- {id: " + f.sibling + ", title: Noted sibling, status: available, notes: 2}"
	if !strings.Contains(header, line) {
		t.Errorf("sibling must render on one line %q:\n%s", line, header)
	}
}

func TestRenderOrientYAML_Header_TargetLeadsTheOutput(t *testing.T) {
	db := SetupTestDB(t)
	f := seedHeaderFixture(t, db)

	view, err := RunOrient(db, f.target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	first := len(out)
	for _, other := range []string{"Blocker leaf", "Blocked leaf", "Noted sibling", "Quiet sibling", "Root", "Parent"} {
		if i := strings.Index(out, other); i >= 0 && i < first {
			first = i
		}
	}
	for _, own := range []string{"the target description", "target criterion one", "target criterion two"} {
		i := strings.Index(out, own)
		if i < 0 || i > first {
			t.Errorf("%q must appear before any other task's content (at %d, first other at %d):\n%s", own, i, first, out)
		}
	}

	var doc struct {
		Orient struct {
			Desc     string `yaml:"desc"`
			Criteria []struct {
				Text  string `yaml:"text"`
				State string `yaml:"state"`
			} `yaml:"criteria"`
			Notes     []string `yaml:"notes"`
			BlockedBy []struct {
				ID         string `yaml:"id"`
				LatestNote string `yaml:"latest_note"`
			} `yaml:"blockedBy"`
		} `yaml:"orient"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if doc.Orient.Desc != "the target description" || len(doc.Orient.Criteria) != 2 || len(doc.Orient.Notes) != 3 {
		t.Errorf("decoded header: %+v", doc.Orient)
	}
	if len(doc.Orient.BlockedBy) != 1 || doc.Orient.BlockedBy[0].LatestNote != "blocker latest note" {
		t.Errorf("decoded header blockedBy: %+v", doc.Orient.BlockedBy)
	}
}

// The header carries the target in full, so the tree's target node is
// identity only in the default view; --full keeps it whole.
func TestRunOrient_TreeTarget_NotDuplicatedFromHeader(t *testing.T) {
	db := SetupTestDB(t)
	f := seedHeaderFixture(t, db)

	view, err := RunOrient(db, f.target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, f.target)
	if !node.Target || node.Desc != "" || len(node.Notes) != 0 || len(node.Criteria) != 0 {
		t.Errorf("tree target: target=%v desc=%q notes=%v criteria=%d, want identity only",
			node.Target, node.Desc, noteTexts(node.Notes), len(node.Criteria))
	}

	full, err := RunOrientOpts(db, f.target, "", TestActor, true, false)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	node = findOrientNode(full.Tree, f.target)
	if node.Desc == "" || len(node.Notes) != 3 || len(node.Criteria) != 2 {
		t.Errorf("--full tree target must stay whole: desc=%q notes=%d criteria=%d", node.Desc, len(node.Notes), len(node.Criteria))
	}
}
