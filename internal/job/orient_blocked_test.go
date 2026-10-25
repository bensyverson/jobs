package job

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// gvIWzf — A leaf with an open blocker renders `status: blocked` and names its
// blockers, so the frontier can be read from the tree. Before, a leaf blocked
// by the target rendered `status: available` with no blocker.

func TestRunOrient_BlockedLeaf_RendersBlockedWithBlockers(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	target := MustAdd(t, db, root, "Target")
	waiting := MustAdd(t, db, root, "Waiting on the target")
	if err := RunBlock(db, waiting, target, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, waiting)
	if node.Status != "blocked" {
		t.Errorf("blocked leaf Status: got %q, want blocked", node.Status)
	}
	if len(node.BlockedBy) != 1 || node.BlockedBy[0] != target {
		t.Errorf("blocked leaf BlockedBy: got %v, want [%s]", node.BlockedBy, target)
	}

	out := renderOrient(t, view)
	var doc struct {
		Tasks []struct {
			Children []struct {
				ID        string   `yaml:"id"`
				Status    string   `yaml:"status"`
				BlockedBy []string `yaml:"blockedBy"`
			} `yaml:"children"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	for _, c := range doc.Tasks[0].Children {
		if c.ID != waiting {
			continue
		}
		if c.Status != "blocked" || len(c.BlockedBy) != 1 || c.BlockedBy[0] != target {
			t.Errorf("rendered blocked leaf: status=%q blockedBy=%v\n%s", c.Status, c.BlockedBy, out)
		}
		return
	}
	t.Fatalf("blocked leaf %s missing from rendered tree:\n%s", waiting, out)
}

func TestRunOrient_LeafWithDoneBlockers_RendersAvailable(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	blocker := MustAdd(t, db, root, "Finished blocker")
	waiting := MustAdd(t, db, root, "Was waiting")
	if err := RunBlock(db, waiting, blocker, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	MustDone(t, db, blocker)

	view, err := RunOrient(db, waiting, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	node := findOrientNode(view.Tree, waiting)
	if node.Status != "available" {
		t.Errorf("leaf with only done blockers: Status got %q, want available", node.Status)
	}
	if len(node.BlockedBy) != 0 {
		t.Errorf("leaf with only done blockers: BlockedBy got %v, want none", node.BlockedBy)
	}
}
