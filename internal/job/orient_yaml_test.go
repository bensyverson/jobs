package job

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// mBaVj — YAML renderer for orient output. RenderOrientYAML emits a top-level
// `orient:` header then a `tasks:` tree, per-node field order id → status →
// (closed) → desc → labels → blocks → criteria → notes last, criteria as
// {text, state}, target node flagged `target: true`, done nodes carrying a
// `closed` date. Output is valid YAML and round-trips.

// rtDoc mirrors the rendered document for round-trip decode assertions.
type rtDoc struct {
	Orient struct {
		Target    string   `yaml:"target"`
		Title     string   `yaml:"title"`
		Root      string   `yaml:"root"`
		Status    string   `yaml:"status"`
		BlockedBy []string `yaml:"blockedBy"`
		Blocks    []struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
		} `yaml:"blocks"`
		Criteria struct {
			Passed int `yaml:"passed"`
			Total  int `yaml:"total"`
		} `yaml:"criteria"`
		OwnNotes   []string `yaml:"own_notes"`
		WeighNotes []string `yaml:"weigh_notes"`
	} `yaml:"orient"`
	Tasks []rtNode `yaml:"tasks"`
}

type rtNode struct {
	Title    string   `yaml:"title"`
	ID       string   `yaml:"id"`
	Status   string   `yaml:"status"`
	Target   bool     `yaml:"target"`
	Closed   string   `yaml:"closed"`
	Desc     string   `yaml:"desc"`
	Labels   []string `yaml:"labels"`
	Blocks   []string `yaml:"blocks"`
	Criteria []struct {
		Text  string `yaml:"text"`
		State string `yaml:"state"`
	} `yaml:"criteria"`
	Notes    []string `yaml:"notes"`
	Children []rtNode `yaml:"children"`
}

func findRTNode(nodes []rtNode, id string) *rtNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
		if f := findRTNode(nodes[i].Children, id); f != nil {
			return f
		}
	}
	return nil
}

func renderOrient(t *testing.T, view *OrientView) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderOrientYAML(&buf, view); err != nil {
		t.Fatalf("RenderOrientYAML: %v", err)
	}
	return buf.String()
}

// Jm4 — Output has a top-level `orient:` header and a `tasks:` tree.
func TestRenderOrientYAML_HasOrientHeaderAndTasks(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	MustAdd(t, db, root, "Leaf")

	view, err := RunOrient(db, "", "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var top map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if _, ok := top["orient"]; !ok {
		t.Errorf("missing top-level `orient:` header:\n%s", out)
	}
	if _, ok := top["tasks"]; !ok {
		t.Errorf("missing top-level `tasks:` tree:\n%s", out)
	}
}

// cjx — Per-node order is id, status, desc, criteria, then notes last.
func TestRenderOrientYAML_NodeFieldOrder(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAddDesc(t, db, root, "Leaf", "leaf description")
	if _, err := RunAddCriteria(db, leaf, []Criterion{{Label: "crit one"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if err := RunNote(db, leaf, "a progress note", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}

	// Scope to the leaf so the rendered tree is a single node and key
	// positions are unambiguous.
	view, err := RunOrient(db, leaf, leaf, TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	// Restrict the search to the `tasks:` section; the header also carries
	// `criteria:`/`status:` and `own_notes:` (which contains the substring
	// `notes:`), which would otherwise pollute positional checks.
	ti := strings.Index(out, "\ntasks:")
	if ti < 0 {
		t.Fatalf("no tasks section:\n%s", out)
	}
	body := out[ti:]

	order := []string{"id:", "status:", "desc:", "criteria:", "notes:"}
	prev := -1
	for _, key := range order {
		idx := strings.Index(body, key)
		if idx < 0 {
			t.Fatalf("key %q missing from tasks section:\n%s", key, body)
		}
		if idx < prev {
			t.Errorf("key %q out of order (expected %v):\n%s", key, order, body)
		}
		prev = idx
	}
}

// xRo — Criteria render as {text, state}; the target node is flagged target true.
func TestRenderOrientYAML_CriteriaAndTargetFlag(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	if _, err := RunAddCriteria(db, leaf, []Criterion{{Label: "must hold"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}

	view, err := RunOrient(db, leaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var doc rtDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	node := findRTNode(doc.Tasks, leaf)
	if node == nil {
		t.Fatalf("leaf %s missing in decoded tree", leaf)
	}
	if !node.Target {
		t.Errorf("target node not flagged `target: true`:\n%s", out)
	}
	if len(node.Criteria) != 1 || node.Criteria[0].Text != "must hold" || node.Criteria[0].State != "pending" {
		t.Errorf("criteria not rendered as {text,state}: got %+v\n%s", node.Criteria, out)
	}
}

// BIW — Done nodes include a `closed` date; full descriptions are never truncated.
func TestRenderOrientYAML_ClosedDateAndFullDesc(t *testing.T) {
	db := SetupTestDB(t)
	longDesc := strings.Repeat("This is a long description that must survive folding without truncation. ", 8)
	root := MustAdd(t, db, "", "Root")
	available := MustAdd(t, db, root, "Available leaf")
	doneLeaf := MustAddDesc(t, db, root, "Done leaf", longDesc)
	MustDone(t, db, doneLeaf)

	view, err := RunOrient(db, available, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var doc rtDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	done := findRTNode(doc.Tasks, doneLeaf)
	if done == nil {
		t.Fatalf("done leaf %s missing in decoded tree", doneLeaf)
	}
	if done.Closed == "" {
		t.Errorf("done node missing `closed` date:\n%s", out)
	}
	if !strings.HasPrefix(done.Closed, "20") || len(done.Closed) != 10 {
		t.Errorf("closed not a YYYY-MM-DD date: %q", done.Closed)
	}
	// Full description round-trips intact (folding preserves content).
	if done.Desc != longDesc {
		t.Errorf("description truncated/altered:\ngot:  %q\nwant: %q", done.Desc, longDesc)
	}
}

// HGK — Output is valid YAML and round-trips through a yaml.v3 decode.
func TestRenderOrientYAML_RoundTrips(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Root", "root desc", "", []string{"docs"}, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	root := res.ShortID
	target := MustAddDesc(t, db, root, "Target leaf", "target desc")
	if _, err := RunAddCriteria(db, target, []Criterion{{Label: "x"}, {Label: "y"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	if _, err := RunSetCriterion(db, target, "x", CriterionPassed, TestActor); err != nil {
		t.Fatalf("RunSetCriterion: %v", err)
	}

	view, err := RunOrient(db, target, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	var doc rtDoc
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, out)
	}
	if doc.Orient.Target != target {
		t.Errorf("decoded orient.target: got %q, want %q", doc.Orient.Target, target)
	}
	if doc.Orient.Criteria.Passed != 1 || doc.Orient.Criteria.Total != 2 {
		t.Errorf("decoded criteria tally: got %+v, want {1 2}", doc.Orient.Criteria)
	}
	rootNode := findRTNode(doc.Tasks, root)
	if rootNode == nil {
		t.Fatalf("root %s missing in decoded tree", root)
	}
	if len(rootNode.Labels) != 1 || rootNode.Labels[0] != "docs" {
		t.Errorf("decoded root labels: got %v, want [docs]", rootNode.Labels)
	}
	leaf := findRTNode(doc.Tasks, target)
	if leaf == nil || leaf.Title != "Target leaf" {
		t.Errorf("decoded target leaf missing or wrong title: %+v", leaf)
	}
}
