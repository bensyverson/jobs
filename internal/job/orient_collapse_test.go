package job

import (
	"regexp"
	"strings"
	"testing"
)

// gphmMz — Done work collapses in the elided tree. A closed leaf renders on
// one line (title, id, close date). A closed container whose whole subtree is
// closed keeps its description and a count of its leaves, and renders no leaf
// rows. Done leaves under a container that is still open stay, one line each.
// The most recent completion note still prints; --full is unchanged.

func TestRenderOrientYAML_DoneLeaf_OneLine(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	done := MustAddDesc(t, db, root, "Finished leaf", "spent desc")
	MustDone(t, db, done)
	canceled := MustAdd(t, db, root, "Dropped leaf")
	if _, _, _, err := RunCancel(db, []string{canceled}, "not needed", false, false, false, TestActor); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if got := findOrientNode(view.Tree, done).Shape; got != OrientShapeLine {
		t.Errorf("done leaf Shape: got %v, want OrientShapeLine", got)
	}
	out := renderOrient(t, view)
	doneLine := regexp.MustCompile(`(?m)^\s*- \{title: Finished leaf, id: ` + done + `, done: "\d{4}-\d{2}-\d{2}"\}$`)
	if !doneLine.MatchString(out) {
		t.Errorf("done leaf must render as one line {title, id, done: date}:\n%s", out)
	}
	canceledLine := regexp.MustCompile(`(?m)^\s*- \{title: Dropped leaf, id: ` + canceled + `, canceled: "\d{4}-\d{2}-\d{2}"\}$`)
	if !canceledLine.MatchString(out) {
		t.Errorf("canceled leaf must render as one line {title, id, canceled: date}:\n%s", out)
	}
}

func TestRunOrient_DoneContainer_CollapsesToDescAndCount(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	container := MustAddDesc(t, db, root, "Finished slice", "slice narrative")
	first := MustAdd(t, db, container, "First child")
	second := MustAdd(t, db, container, "Second child")
	MustDone(t, db, first)
	MustDone(t, db, second) // cascade-closes the container
	target := MustAdd(t, db, root, "Target")
	// A later note-bearing close elsewhere takes the breadcrumb, so the
	// container keeps no rows at all.
	other := MustAdd(t, db, root, "Other finished leaf")
	if _, _, err := RunDone(db, []string{other}, false, "latest close", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done other: %v", err)
	}

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, container)
	if node.Shape != OrientShapeCollapsed {
		t.Errorf("done container Shape: got %v, want OrientShapeCollapsed", node.Shape)
	}
	if node.Desc != "slice narrative" || node.DoneLeaves != 2 || len(node.Children) != 0 {
		t.Errorf("collapsed container: desc=%q doneLeaves=%d children=%d, want desc, 2, 0",
			node.Desc, node.DoneLeaves, len(node.Children))
	}
	out := renderOrient(t, view)
	if !strings.Contains(out, "done_leaves: 2") {
		t.Errorf("collapsed container must render its done-leaf count:\n%s", out)
	}
	if strings.Contains(out, first) || strings.Contains(out, second) {
		t.Errorf("collapsed container must render no child rows:\n%s", out)
	}
}

func TestRunOrient_DoneLeavesUnderOpenContainer_StayAsLines(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	container := MustAdd(t, db, root, "Open slice")
	doneA := MustAdd(t, db, container, "Done A")
	doneB := MustAdd(t, db, container, "Done B")
	MustDone(t, db, doneA)
	MustDone(t, db, doneB)
	target := MustAdd(t, db, container, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	for _, id := range []string{doneA, doneB} {
		node := findOrientNode(view.Tree, id)
		if node == nil {
			t.Fatalf("done leaf %s under an open container must still render", id)
		}
		if node.Shape != OrientShapeLine {
			t.Errorf("done leaf %s Shape: got %v, want OrientShapeLine", id, node.Shape)
		}
	}
}

// The breadcrumb survives a collapse: when the most recent note-bearing
// close sits inside a fully done container, that one leaf keeps its row.
func TestRunOrient_CompletionNote_SurvivesCollapse(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	container := MustAdd(t, db, root, "Finished slice")
	quiet := MustAdd(t, db, container, "Quiet child")
	MustDone(t, db, quiet)
	last := MustAdd(t, db, container, "Last child")
	if _, _, err := RunDone(db, []string{last}, false, "what just happened", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done last: %v", err)
	}
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, container)
	if node.Shape != OrientShapeCollapsed || node.DoneLeaves != 2 {
		t.Errorf("container: shape=%v doneLeaves=%d, want collapsed with 2", node.Shape, node.DoneLeaves)
	}
	if findOrientNode(view.Tree, quiet) != nil {
		t.Errorf("quiet child %s must collapse away", quiet)
	}
	kept := findOrientNode(view.Tree, last)
	if kept == nil || kept.CompletionNote != "what just happened" {
		t.Fatalf("the breadcrumb leaf must keep its row and completion note: %+v", kept)
	}
	if out := renderOrient(t, view); !strings.Contains(out, "completion_note: what just happened") {
		t.Errorf("completion note must render:\n%s", out)
	}
}

func TestRunOrientOpts_Full_NoCollapse(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	container := MustAddDesc(t, db, root, "Finished slice", "slice narrative")
	child := MustAddDesc(t, db, container, "Child", "child desc")
	MustDone(t, db, child)
	target := MustAdd(t, db, root, "Target")

	view, err := RunOrientOpts(db, target, "", TestActor, true, false)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	var walk func(n *OrientNode)
	walk = func(n *OrientNode) {
		if n.Shape != OrientShapeBlock {
			t.Errorf("--full node %s Shape: got %v, want OrientShapeBlock", n.Task.ShortID, n.Shape)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(view.Tree)
	out := renderOrient(t, view)
	if !strings.Contains(out, "child desc") || strings.Contains(out, "done_leaves") || strings.Contains(out, "- {title:") {
		t.Errorf("--full must render every node in block form:\n%s", out)
	}
}
