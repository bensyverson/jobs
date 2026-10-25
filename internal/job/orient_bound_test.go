package job

import (
	"database/sql"
	"strings"
	"testing"
)

// y9yCyl — Note history on open nodes is bounded: a node that isn't closed
// prints only its latest substantive note plus a count of the earlier ones
// and the `job show` that carries them. Canceled nodes elide as done ones do.
// The target keeps every note; --full keeps every note everywhere.

func mustNotes(t *testing.T, db *sql.DB, id string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if err := RunNote(db, id, text, nil, TestActor); err != nil {
			t.Fatalf("note %s: %v", id, err)
		}
	}
}

func TestRunOrient_OpenNode_KeepsOnlyLatestNote(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	mustNotes(t, db, root, "session one", "session two", "session three")
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, root)
	if got := noteTexts(node.Notes); len(got) != 1 || got[0] != "session three" {
		t.Errorf("open node Notes: got %v, want only the latest [session three]", got)
	}
	if node.EarlierNotes != 2 {
		t.Errorf("open node EarlierNotes: got %d, want 2", node.EarlierNotes)
	}

	out := renderOrient(t, view)
	want := "earlier_notes: '+2 earlier: job show " + root + "'"
	if !strings.Contains(out, want) {
		t.Errorf("rendered output missing %q:\n%s", want, out)
	}
	if strings.Contains(out, "session one") {
		t.Errorf("earlier note leaked into the default view:\n%s", out)
	}
}

func TestRunOrient_OpenNode_OneNote_NoCount(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	mustNotes(t, db, root, "the only note")
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, root)
	if got := noteTexts(node.Notes); len(got) != 1 || got[0] != "the only note" {
		t.Errorf("open node Notes: got %v, want [the only note]", got)
	}
	if node.EarlierNotes != 0 {
		t.Errorf("open node EarlierNotes: got %d, want 0", node.EarlierNotes)
	}
	if out := renderOrient(t, view); strings.Contains(out, "earlier_notes") {
		t.Errorf("a single note must render with no count:\n%s", out)
	}
}

func TestRunOrient_TargetNotes_NeverTruncated(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	target := MustAdd(t, db, root, "Target")
	mustNotes(t, db, target, "first", "second", "third")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if got := noteTexts(view.Header.Notes); len(got) != 3 {
		t.Errorf("header notes: got %v, want all three", got)
	}
	if node := findOrientNode(view.Tree, target); node.EarlierNotes != 0 {
		t.Errorf("target EarlierNotes: got %d, want 0 (the target is never truncated)", node.EarlierNotes)
	}
}

func TestRunOrient_CanceledNode_ElidedLikeDone(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	canceled := MustAddDesc(t, db, root, "Canceled leaf", "canceled desc")
	mustNotes(t, db, canceled, "canceled progress")
	if _, err := RunAddCriteria(db, canceled, []Criterion{{Label: "never met"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, _, _, err := RunCancel(db, []string{canceled}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, canceled)
	if node.Desc != "" || len(node.Notes) != 0 || len(node.Criteria) != 0 {
		t.Errorf("canceled node must elide: desc=%q notes=%v criteria=%d",
			node.Desc, noteTexts(node.Notes), len(node.Criteria))
	}
}

func TestRunOrientOpts_Full_KeepsEveryNoteOnOpenNodes(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	mustNotes(t, db, root, "one", "two", "three")
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrientOpts(db, target, "", TestActor, true, false)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	node := findOrientNode(view.Tree, root)
	if got := noteTexts(node.Notes); len(got) != 3 {
		t.Errorf("--full open node Notes: got %v, want all three", got)
	}
	if out := renderOrient(t, view); strings.Contains(out, "earlier_notes") {
		t.Errorf("--full must not render an earlier-notes count:\n%s", out)
	}
}
