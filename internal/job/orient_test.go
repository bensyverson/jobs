package job

import (
	"slices"
	"testing"
)

// KpOeX — RunOrient domain model + assembly. These tests are written
// red-first against the seven acceptance criteria for the task. RunOrient
// assembles an OrientView{Header, Tree} for a target leaf (or the next
// available leaf when no id is given), reusing RunNext, GetAncestors,
// getChildren, getNotesForTask, GetCriteria, GetBlockers/GetBlocked.

// findOrientNode walks the assembled tree for a node by short id.
func findOrientNode(n *OrientNode, shortID string) *OrientNode {
	if n == nil {
		return nil
	}
	if n.Task.ShortID == shortID {
		return n
	}
	for _, c := range n.Children {
		if f := findOrientNode(c, shortID); f != nil {
			return f
		}
	}
	return nil
}

func noteTexts(notes []NoteEntry) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, n.Text)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}

// mRq — No-arg orient targets the next available leaf (matches RunNext).
func TestRunOrient_NoArg_TargetsNextLeaf(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	first := MustAdd(t, db, root, "First leaf")
	MustAdd(t, db, root, "Second leaf")

	want, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if want.ShortID != first {
		t.Fatalf("precondition: expected next leaf %s, got %s", first, want.ShortID)
	}

	view, err := RunOrient(db, "", "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Header.Target != want.ShortID {
		t.Errorf("Header.Target: got %q, want %q (next available leaf)", view.Header.Target, want.ShortID)
	}
}

// as8 — Positional id sets the target; default render scope is its whole
// root tree.
func TestRunOrient_PositionalTarget_ScopeIsWholeRootTree(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	branch := MustAdd(t, db, root, "Branch")
	leaf := MustAdd(t, db, branch, "Deep leaf")

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Header.Target != leaf {
		t.Errorf("Header.Target: got %q, want %q", view.Header.Target, leaf)
	}
	if view.Tree.Task.ShortID != root {
		t.Errorf("tree root: got %q, want whole root tree %q", view.Tree.Task.ShortID, root)
	}
	if view.Header.Root != root {
		t.Errorf("Header.Root: got %q, want %q", view.Header.Root, root)
	}
	target := findOrientNode(view.Tree, leaf)
	if target == nil {
		t.Fatalf("target leaf %s missing from rendered tree", leaf)
	}
	if !target.Target {
		t.Errorf("target node %s not flagged Target=true", leaf)
	}
}

// eUG — --scope <id> limits the rendered tree to that subtree.
func TestRunOrient_Scope_LimitsToSubtree(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	branch := MustAdd(t, db, root, "Branch")
	leaf := MustAdd(t, db, branch, "Deep leaf")
	sibling := MustAdd(t, db, root, "Off-scope sibling")

	view, err := RunOrient(db, leaf, branch, TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Tree.Task.ShortID != branch {
		t.Errorf("scoped tree root: got %q, want %q", view.Tree.Task.ShortID, branch)
	}
	if findOrientNode(view.Tree, sibling) != nil {
		t.Errorf("off-scope sibling %s should not appear in subtree render", sibling)
	}
	if findOrientNode(view.Tree, leaf) == nil {
		t.Errorf("in-scope leaf %s missing from subtree render", leaf)
	}
}

// w4K — Header criteria tally reports passed/total for the target.
func TestRunOrient_CriteriaTally(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	if _, err := RunAddCriteria(db, leaf, []Criterion{
		{Label: "a"}, {Label: "b"}, {Label: "c"}, {Label: "d"},
	}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunSetCriterion(db, leaf, "a", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Header.Criteria.Passed != 1 || view.Header.Criteria.Total != 4 {
		t.Errorf("criteria tally: got {passed:%d total:%d}, want {passed:1 total:4}",
			view.Header.Criteria.Passed, view.Header.Criteria.Total)
	}
}

// ba8 — weigh_notes lists the target's same-parent sibling-leaf ids that
// carry notes.
func TestRunOrient_WeighNotes_SiblingLeavesWithNotes(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	parent := MustAdd(t, db, root, "Parent")
	target := MustAdd(t, db, parent, "Target leaf")
	notedLeaf := MustAdd(t, db, parent, "Sibling leaf with note")
	MustAdd(t, db, parent, "Sibling leaf without note")
	branchSibling := MustAdd(t, db, parent, "Sibling branch with note")
	MustAdd(t, db, branchSibling, "Child of branch sibling")

	if err := RunNote(db, notedLeaf, "weigh this", nil, TestActor); err != nil {
		t.Fatalf("note notedLeaf: %v", err)
	}
	if err := RunNote(db, branchSibling, "non-leaf note", nil, TestActor); err != nil {
		t.Fatalf("note branchSibling: %v", err)
	}

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	got := view.Header.WeighNotes
	if len(got) != 1 || got[0] != notedLeaf {
		t.Errorf("weigh_notes: got %v, want [%s] (only the noted sibling leaf)", got, notedLeaf)
	}
}

// UkH — own_notes inlines the target's own prior notes (empty when none).
func TestRunOrient_OwnNotes_InlinesTargetNotes(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	if err := RunNote(db, leaf, "own progress one", nil, TestActor); err != nil {
		t.Fatalf("note 1: %v", err)
	}
	if err := RunNote(db, leaf, "own progress two", nil, TestActor); err != nil {
		t.Fatalf("note 2: %v", err)
	}

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	texts := noteTexts(view.Header.OwnNotes)
	if len(texts) != 2 || texts[0] != "own progress one" || texts[1] != "own progress two" {
		t.Errorf("own_notes: got %v, want [own progress one, own progress two]", texts)
	}
}

func TestRunOrient_OwnNotes_EmptyWhenNone(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if len(view.Header.OwnNotes) != 0 {
		t.Errorf("own_notes: got %v, want empty", noteTexts(view.Header.OwnNotes))
	}
}

// Labels: each node carries its labels so the renderer can emit them.
func TestRunOrient_NodeCarriesLabels(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Root", "", "", []string{"docs", "cli"}, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	root := res.ShortID
	leaf := MustAdd(t, db, root, "Leaf")

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if got := view.Tree.Labels; len(got) != 2 || !contains(got, "docs") || !contains(got, "cli") {
		t.Errorf("root labels: got %v, want [docs cli]", got)
	}
}

// Closed: a done node carries the close timestamp so the renderer can emit a date.
func TestRunOrient_DoneNodeCarriesClosed(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	available := MustAdd(t, db, root, "Available leaf")
	doneLeaf := MustAdd(t, db, root, "Done leaf")
	MustDone(t, db, doneLeaf)

	view, err := RunOrient(db, available, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, doneLeaf)
	if node == nil {
		t.Fatalf("done leaf %s missing from tree", doneLeaf)
	}
	if node.Closed <= 0 {
		t.Errorf("done node Closed: got %d, want a positive timestamp", node.Closed)
	}
	avail := findOrientNode(view.Tree, available)
	if avail.Closed != 0 {
		t.Errorf("available node Closed: got %d, want 0", avail.Closed)
	}
}

// ypv — Notes include `noted` events and completion notes; churn (heartbeat,
// claimed, claim_expired, released, moved, labeled, blocked) is excluded.
func TestRunOrient_Notes_IncludeNotedAndCompletion_ExcludeChurn(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	available := MustAdd(t, db, root, "Available leaf")
	doneLeaf := MustAdd(t, db, root, "Done leaf")

	if err := RunNote(db, doneLeaf, "progress before close", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	// A claim produces churn events (claimed) that must NOT surface as notes.
	MustClaim(t, db, doneLeaf, "1h")
	if _, _, err := RunDone(db, []string{doneLeaf}, false, "wrapped it up", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	// Done-leaf notes only surface in the full view (the default elides done
	// history); the note-filtering contract itself is unchanged.
	view, err := RunOrientOpts(db, available, "", TestActor, true)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	node := findOrientNode(view.Tree, doneLeaf)
	if node == nil {
		t.Fatalf("done leaf %s missing from tree", doneLeaf)
	}
	texts := noteTexts(node.Notes)
	if len(texts) != 2 {
		t.Fatalf("node notes: got %v, want exactly [progress before close, wrapped it up]", texts)
	}
	if !contains(texts, "progress before close") {
		t.Errorf("missing noted-event body: %v", texts)
	}
	if !contains(texts, "wrapped it up") {
		t.Errorf("missing completion note: %v", texts)
	}
}
