package signals

import (
	"fmt"
	"reflect"
	"testing"
)

// BuildSubwayFromInput is the public entry point used by the
// POST /home/graph endpoint: a JSON-friendly task+block bundle in,
// a fully-built Subway out. The contract is that an input bundle
// equivalent to a live world produces the same Subway as
// buildSubway(newTestWorld(...)).

func TestBuildSubwayFromInput_MatchesEquivalentWorld(t *testing.T) {
	// Reference tree with G claimed and H done. Mirrors a scenario
	// the existing subway_test.go exercises so the assertion catches
	// a structural mismatch in either direction.
	tasks := referenceTree(map[string]string{
		"G": "claimed",
		"H": "done",
	})
	world := newTestWorld(tasks)
	wantSubway := buildSubway(world)

	in := SubwayInput{}
	for i, td := range tasks {
		in.Tasks = append(in.Tasks, SubwayInputTask{
			ShortID:       td.short,
			Status:        td.status,
			ParentShortID: td.parent,
			// Match newTestWorld's sort-key assignment so the child
			// ordering inside each parent matches.
			SortKey: fmt.Sprintf("%06d", i+1),
		})
	}
	gotSubway := BuildSubwayFromInput(in)

	if !reflect.DeepEqual(wantSubway, gotSubway) {
		t.Fatalf("BuildSubwayFromInput diverged from buildSubway\n  want: %+v\n  got:  %+v", wantSubway, gotSubway)
	}
}

func TestBuildSubwayFromInput_HonorsBlockerStatuses(t *testing.T) {
	// G is blocked by B; B is "done" so the block should NOT count
	// as an open blocker (matches loadGraphWorld's bookkeeping).
	tasks := referenceTree(map[string]string{
		"B": "done",
		"G": "claimed",
	})
	in := SubwayInput{}
	for _, td := range tasks {
		in.Tasks = append(in.Tasks, SubwayInputTask{
			ShortID:       td.short,
			Title:         td.short,
			Status:        td.status,
			ParentShortID: td.parent,
		})
	}
	in.Blocks = []SubwayInputBlock{
		{BlockerShortID: "B", BlockedShortID: "G"},
	}

	got := BuildSubwayFromInput(in)
	// G's anchor edge should be SubwayEdgeBranch (open), not BranchClosed,
	// because B is done.
	for _, e := range got.Edges {
		if e.ToShortID == "G" && e.Kind == SubwayEdgeBranchClosed {
			t.Fatalf("expected G's branch edge to be open (B is done), got BranchClosed")
		}
	}
}

func TestBuildSubwayFromInput_OpenBlockerClosesBranch(t *testing.T) {
	// Two leaf focals in distinct subtrees so each focal's parent
	// gets its own line (the parent-boundary rule absorbs same-parent
	// focals into one line). D under B, H under G; B blocks G, so
	// G's branch ingress must render BranchClosed.
	tasks := referenceTree(map[string]string{
		"D": "claimed",
		"H": "claimed",
	})
	in := SubwayInput{}
	for _, td := range tasks {
		in.Tasks = append(in.Tasks, SubwayInputTask{
			ShortID:       td.short,
			Title:         td.short,
			Status:        td.status,
			ParentShortID: td.parent,
		})
	}
	in.Blocks = []SubwayInputBlock{
		{BlockerShortID: "B", BlockedShortID: "G"},
	}

	got := BuildSubwayFromInput(in)
	closed := false
	for _, e := range got.Edges {
		if e.ToShortID == "G" && e.Kind == SubwayEdgeBranchClosed {
			closed = true
		}
	}
	if !closed {
		t.Fatalf("expected G's branch edge to be BranchClosed (B has open blocker), got: %+v", got.Edges)
	}
}

func TestBuildSubwayFromInput_EmptyInputReturnsEmptySubway(t *testing.T) {
	got := BuildSubwayFromInput(SubwayInput{})
	if len(got.Lines) != 0 || len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("expected empty Subway, got %+v", got)
	}
}
