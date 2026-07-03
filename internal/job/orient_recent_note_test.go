package job

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// FbpDr — The default (elided) orient view carries a single "what just
// happened" breadcrumb: CompletionNote is populated on exactly one node, the
// most recently closed done task that has a completion note (done-event
// recency, ties broken by event id). Noteless closes — cascade-closed
// containers foremost — never blank the breadcrumb; the latest note-bearing
// close surfaces instead. The full view leaves CompletionNote empty because
// completion notes already fold into Notes there.

// CUL — Only the most recently closed task in the rendered tree emits
// completion_note.
func TestRunOrient_CompletionNote_OnlyMostRecentClose(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	first := MustAdd(t, db, root, "First done leaf")
	second := MustAdd(t, db, root, "Second done leaf")
	open := MustAdd(t, db, root, "Open target")

	if _, _, err := RunDone(db, []string{first}, false, "first close note", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done first: %v", err)
	}
	if _, _, err := RunDone(db, []string{second}, false, "second close note", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done second: %v", err)
	}

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if got := findOrientNode(view.Tree, second).CompletionNote; got != "second close note" {
		t.Errorf("most recent close CompletionNote: got %q, want %q", got, "second close note")
	}
	if got := findOrientNode(view.Tree, first).CompletionNote; got != "" {
		t.Errorf("earlier close CompletionNote: got %q, want empty", got)
	}
}

// A noteless later close (the cascade-closed container case) must not blank
// the breadcrumb: the latest note-bearing close still surfaces.
func TestRunOrient_CompletionNote_SkipsNotelessLaterClose(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	noted := MustAdd(t, db, root, "Noted done leaf")
	container := MustAdd(t, db, root, "Container")
	child := MustAdd(t, db, container, "Container child")
	open := MustAdd(t, db, root, "Open target")

	if _, _, err := RunDone(db, []string{noted}, false, "the real breadcrumb", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done noted: %v", err)
	}
	// Cascade-closes the container with no completion note of its own.
	MustDone(t, db, child)

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if got := findOrientNode(view.Tree, noted).CompletionNote; got != "the real breadcrumb" {
		t.Errorf("note-bearing close CompletionNote: got %q, want %q", got, "the real breadcrumb")
	}
	for _, id := range []string{container, child} {
		if got := findOrientNode(view.Tree, id).CompletionNote; got != "" {
			t.Errorf("noteless close %s CompletionNote: got %q, want empty", id, got)
		}
	}
}

// DDC — A tree with no closed tasks emits no completion_note anywhere.
func TestRunOrient_CompletionNote_NoClosedTasks(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	open := MustAdd(t, db, root, "Open leaf")
	MustAdd(t, db, root, "Another open leaf")

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	var walk func(n *OrientNode)
	walk = func(n *OrientNode) {
		if n.CompletionNote != "" {
			t.Errorf("node %s CompletionNote: got %q, want empty", n.Task.ShortID, n.CompletionNote)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(view.Tree)
}

// The full view already folds completion notes into Notes; CompletionNote
// stays empty there so the same text never renders twice.
func TestRunOrientOpts_Full_NoCompletionNoteAnnotation(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	done := MustAdd(t, db, root, "Done leaf")
	open := MustAdd(t, db, root, "Open target")
	if _, _, err := RunDone(db, []string{done}, false, "folded into notes", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	view, err := RunOrientOpts(db, open, "", TestActor, true)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
	}
	node := findOrientNode(view.Tree, done)
	if node.CompletionNote != "" {
		t.Errorf("full view CompletionNote: got %q, want empty (already in Notes)", node.CompletionNote)
	}
	if !contains(noteTexts(node.Notes), "folded into notes") {
		t.Errorf("full view Notes: got %v, want completion note folded in", noteTexts(node.Notes))
	}
}

// Rendered-YAML contract: completion_note appears on exactly one node and is
// absent (not empty-valued) everywhere else.
func TestRenderOrientYAML_CompletionNote_SingleKey(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	first := MustAdd(t, db, root, "First done leaf")
	second := MustAdd(t, db, root, "Second done leaf")
	open := MustAdd(t, db, root, "Open target")
	if _, _, err := RunDone(db, []string{first}, false, "old note", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done first: %v", err)
	}
	if _, _, err := RunDone(db, []string{second}, false, "fresh note", nil, TestActor, false, ""); err != nil {
		t.Fatalf("done second: %v", err)
	}

	view, err := RunOrient(db, open, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var doc struct {
		Tasks []yaml.Node `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	count := 0
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content)-1; i += 2 {
				if n.Content[i].Value == "completion_note" {
					count++
					if n.Content[i+1].Value != "fresh note" {
						t.Errorf("completion_note body: got %q, want %q", n.Content[i+1].Value, "fresh note")
					}
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for i := range doc.Tasks {
		walk(&doc.Tasks[i])
	}
	if count != 1 {
		t.Errorf("completion_note keys in output: got %d, want exactly 1\n%s", count, out)
	}
}
