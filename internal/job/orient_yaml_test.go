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
		Target   string `yaml:"target"`
		Title    string `yaml:"title"`
		Root     string `yaml:"root"`
		Status   string `yaml:"status"`
		Desc     string `yaml:"desc"`
		Criteria []struct {
			Text  string `yaml:"text"`
			State string `yaml:"state"`
		} `yaml:"criteria"`
		Notes     []string `yaml:"notes"`
		BlockedBy []struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
		} `yaml:"blockedBy"`
		Blocks []struct {
			ID    string `yaml:"id"`
			Title string `yaml:"title"`
		} `yaml:"blocks"`
		Siblings []struct {
			ID    string `yaml:"id"`
			Notes int    `yaml:"notes"`
		} `yaml:"siblings"`
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

	target := MustAdd(t, db, root, "Target")

	// Scope to the (non-target) leaf so the rendered tree is a single node
	// and key positions are unambiguous. The target itself renders identity
	// only in the tree; its content lives in the header.
	view, err := RunOrient(db, target, leaf, TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	out := renderOrient(t, view)

	// Restrict the search to the `tasks:` section; the header also carries
	// `criteria:`/`status:`/`notes:`, which would otherwise pollute
	// positional checks.
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

// xRo — Criteria render as {text, state}, in the header for the target and
// in the tree for every other open node; the target node is flagged target
// true.
func TestRenderOrientYAML_CriteriaAndTargetFlag(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	if _, err := RunAddCriteria(db, leaf, []Criterion{{Label: "must hold"}}, TestActor); err != nil {
		t.Fatalf("RunAddCriteria: %v", err)
	}
	other := MustAdd(t, db, root, "Other leaf")
	if _, err := RunAddCriteria(db, other, []Criterion{{Label: "also holds"}}, TestActor); err != nil {
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
	if c := doc.Orient.Criteria; len(c) != 1 || c[0].Text != "must hold" || c[0].State != "pending" {
		t.Errorf("header criteria not rendered as {text,state}: got %+v\n%s", c, out)
	}
	otherNode := findRTNode(doc.Tasks, other)
	if otherNode == nil {
		t.Fatalf("leaf %s missing in decoded tree", other)
	}
	if c := otherNode.Criteria; len(c) != 1 || c[0].Text != "also holds" || c[0].State != "pending" {
		t.Errorf("tree criteria not rendered as {text,state}: got %+v\n%s", c, out)
	}
}

// BIW — Done nodes include a `closed` date; full descriptions are never
// truncated. Desc folding is asserted through the full view since the
// default elides done-leaf descriptions; the closed date renders in both.
func TestRenderOrientYAML_ClosedDateAndFullDesc(t *testing.T) {
	db := SetupTestDB(t)
	longDesc := strings.Repeat("This is a long description that must survive folding without truncation. ", 8)
	root := MustAdd(t, db, "", "Root")
	available := MustAdd(t, db, root, "Available leaf")
	doneLeaf := MustAddDesc(t, db, root, "Done leaf", longDesc)
	MustDone(t, db, doneLeaf)

	view, err := RunOrientOpts(db, available, "", TestActor, true, false)
	if err != nil {
		t.Fatalf("RunOrientOpts: %v", err)
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
	if c := doc.Orient.Criteria; len(c) != 2 || c[0].State != "passed" || c[1].State != "pending" {
		t.Errorf("decoded header criteria: got %+v, want x passed, y pending", c)
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
