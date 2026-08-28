package job

import (
	"database/sql"
	"testing"

	"gopkg.in/yaml.v3"
)

// DEXTE — Done-node history is elided at OrientView assembly. Done leaves
// keep title/id/status/closed only; done containers additionally keep desc
// (the slice-level plan narrative); notes and criteria are dropped from all
// done nodes. Open and claimed nodes are untouched. RunOrientOpts with
// full=true restores the unelided view. Desc becomes an assembly-owned
// projection on OrientNode so renderers never read Task.Description.

// seedElisionTree seeds a root with a done leaf (desc + note + pending
// criterion, closed with a completion note), a done container (desc + note +
// passed criterion, cascade-closed by its only child), and an open target
// leaf. Returns the short ids.
func seedElisionTree(t *testing.T, db *sql.DB) (root, doneLeaf, container, containerChild, open string) {
	t.Helper()
	root = MustAdd(t, db, "", "Root")

	doneLeaf = MustAddDesc(t, db, root, "Done leaf", "done leaf desc")
	if err := RunNote(db, doneLeaf, "leaf progress note", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := RunAddCriteria(db, doneLeaf, []Criterion{{Label: "leaf criterion"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, _, err := RunDone(db, []string{doneLeaf}, false, "leaf completion note", nil, TestActor, true, ""); err != nil {
		t.Fatalf("done leaf: %v", err)
	}

	container = MustAddDesc(t, db, root, "Done container", "container desc")
	if err := RunNote(db, container, "container progress note", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	crits, err := RunAddCriteria(db, container, []Criterion{{Label: "container criterion"}}, TestActor)
	if err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunSetCriterion(db, container, crits[0].ShortID, CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}
	containerChild = MustAdd(t, db, container, "Container child")
	MustDone(t, db, containerChild) // cascade-closes the container

	open = MustAddDesc(t, db, root, "Open target leaf", "open desc")
	return root, doneLeaf, container, containerChild, open
}

// b9N — A done leaf node carries no desc, notes, or criteria in the
// assembled OrientView; its identity fields (status, closed) survive.
func TestRunOrient_DoneLeaf_ElidesDescNotesCriteria(t *testing.T) {
	db := SetupTestDB(t)
	_, doneLeaf, _, _, open := seedElisionTree(t, db)

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, doneLeaf)
	if node == nil {
		t.Fatalf("done leaf %s missing from tree", doneLeaf)
	}
	if node.Desc != "" {
		t.Errorf("done leaf Desc: got %q, want elided (empty)", node.Desc)
	}
	if len(node.Notes) != 0 {
		t.Errorf("done leaf Notes: got %v, want none", noteTexts(node.Notes))
	}
	if len(node.Criteria) != 0 {
		t.Errorf("done leaf Criteria: got %d, want none", len(node.Criteria))
	}
	if node.Task.Status != "done" || node.Closed <= 0 {
		t.Errorf("done leaf identity fields must survive: status=%q closed=%d", node.Task.Status, node.Closed)
	}
}

// 7vn — A done container node keeps desc but carries no notes or criteria.
func TestRunOrient_DoneContainer_KeepsDescDropsHistory(t *testing.T) {
	db := SetupTestDB(t)
	_, _, container, containerChild, open := seedElisionTree(t, db)

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, container)
	if node == nil {
		t.Fatalf("container %s missing from tree", container)
	}
	if node.Task.Status != "done" {
		t.Fatalf("precondition: container should have cascade-closed, status=%q", node.Task.Status)
	}
	if node.Desc != "container desc" {
		t.Errorf("done container Desc: got %q, want %q kept", node.Desc, "container desc")
	}
	if len(node.Notes) != 0 {
		t.Errorf("done container Notes: got %v, want none", noteTexts(node.Notes))
	}
	if len(node.Criteria) != 0 {
		t.Errorf("done container Criteria: got %d, want none", len(node.Criteria))
	}
	child := findOrientNode(view.Tree, containerChild)
	if child == nil {
		t.Fatalf("done container's child %s missing from tree (shape must survive)", containerChild)
	}
	if child.Desc != "" {
		t.Errorf("done child leaf Desc: got %q, want elided", child.Desc)
	}
}

// s6x — Open and claimed nodes keep desc, notes, and criteria exactly as
// before.
func TestRunOrient_OpenAndClaimedNodes_Unelided(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	open := MustAddDesc(t, db, root, "Open leaf", "open desc")
	claimed := MustAddDesc(t, db, root, "Claimed leaf", "claimed desc")
	for _, id := range []string{open, claimed} {
		if err := RunNote(db, id, "live note on "+id, nil, TestActor); err != nil {
			t.Fatalf("note: %v", err)
		}
		if _, err := RunAddCriteria(db, id, []Criterion{{Label: "live criterion"}}, TestActor); err != nil {
			t.Fatalf("RunAddCriteria: %v", err)
		}
	}
	MustClaim(t, db, claimed, "1h")

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	for id, wantDesc := range map[string]string{open: "open desc", claimed: "claimed desc"} {
		node := findOrientNode(view.Tree, id)
		if node == nil {
			t.Fatalf("node %s missing from tree", id)
		}
		if node.Desc != wantDesc {
			t.Errorf("%s Desc: got %q, want %q", id, node.Desc, wantDesc)
		}
		if !contains(noteTexts(node.Notes), "live note on "+id) {
			t.Errorf("%s Notes: got %v, want the live note kept", id, noteTexts(node.Notes))
		}
		if len(node.Criteria) != 1 {
			t.Errorf("%s Criteria: got %d, want 1", id, len(node.Criteria))
		}
	}
}

// mmr — RunOrientOpts with full=true produces today's unelided view.
func TestRunOrientOpts_Full_Unelided(t *testing.T) {
	db := SetupTestDB(t)
	_, doneLeaf, container, _, open := seedElisionTree(t, db)

	view, err := RunOrientOpts(db, open, "", TestActor, true, false)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	leaf := findOrientNode(view.Tree, doneLeaf)
	if leaf == nil {
		t.Fatalf("done leaf %s missing from tree", doneLeaf)
	}
	if leaf.Desc != "done leaf desc" {
		t.Errorf("full view done leaf Desc: got %q, want kept", leaf.Desc)
	}
	texts := noteTexts(leaf.Notes)
	if !contains(texts, "leaf progress note") || !contains(texts, "leaf completion note") {
		t.Errorf("full view done leaf Notes: got %v, want progress + completion notes", texts)
	}
	if len(leaf.Criteria) != 1 {
		t.Errorf("full view done leaf Criteria: got %d, want 1", len(leaf.Criteria))
	}
	cont := findOrientNode(view.Tree, container)
	if cont == nil {
		t.Fatalf("container %s missing from tree", container)
	}
	if !contains(noteTexts(cont.Notes), "container progress note") {
		t.Errorf("full view container Notes: got %v, want progress note kept", noteTexts(cont.Notes))
	}
}

// Rendered-YAML contract: in the default view a done leaf emits no desc,
// notes, or criteria keys, while a done container emits desc; the renderer
// reads the assembly-owned Desc, never Task.Description.
func TestRenderOrientYAML_DefaultView_ElidesDoneHistory(t *testing.T) {
	db := SetupTestDB(t)
	_, doneLeaf, container, _, open := seedElisionTree(t, db)

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var doc rtDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	leaf := findRTNode(doc.Tasks, doneLeaf)
	if leaf == nil {
		t.Fatalf("done leaf %s missing in decoded tree", doneLeaf)
	}
	if leaf.Desc != "" || len(leaf.Notes) != 0 || len(leaf.Criteria) != 0 {
		t.Errorf("done leaf must render bare: desc=%q notes=%v criteria=%v\n%s",
			leaf.Desc, leaf.Notes, leaf.Criteria, out)
	}
	if leaf.Closed == "" {
		t.Errorf("done leaf must keep its closed date:\n%s", out)
	}
	cont := findRTNode(doc.Tasks, container)
	if cont == nil {
		t.Fatalf("container %s missing in decoded tree", container)
	}
	if cont.Desc != "container desc" {
		t.Errorf("done container desc: got %q, want kept\n%s", cont.Desc, out)
	}
	if len(cont.Notes) != 0 {
		t.Errorf("done container notes must be elided, got %v\n%s", cont.Notes, out)
	}
}
