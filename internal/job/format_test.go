package job

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func renderListString(db any, nodes []*TaskNode, blockers map[string][]string) string {
	var buf bytes.Buffer
	RenderMarkdownList(&buf, nodes, blockers, nil, 0)
	return buf.String()
}

func TestList_CheckboxOpen(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Open task")
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [ ] `" + id + "` Open task\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_CheckboxDone(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Closed task")
	MustDone(t, db, id)
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [x] `" + id + "` Closed task\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_ClaimedParens(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	baseTime := time.Unix(1_700_000_000, 0)
	CurrentNowFunc = func() time.Time { return baseTime }

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Claim me")
	if err := RunClaim(db, id, "45m", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	want := "- [ ] `" + id + "` Claim me (claimed by alice, 45m left)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestList_BlockedParens(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	if err := RunBlock(db, b, a, TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	if !strings.Contains(got, "`"+b+"` B (blocked on "+a+")") {
		t.Errorf("expected blocked parens:\n%s", got)
	}
}

func TestList_NestedIndentation(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	child := MustAdd(t, db, parent, "Child")
	nodes, err := runList(db, "", "", true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	blockers, _ := CollectBlockers(db, nodes)
	got := renderListString(db, nodes, blockers)
	wantParent := "- [ ] `" + parent + "` Parent\n"
	wantChild := "  - [ ] `" + child + "` Child\n"
	if !strings.Contains(got, wantParent) {
		t.Errorf("missing parent line:\n%s", got)
	}
	if !strings.Contains(got, wantChild) {
		t.Errorf("missing indented child line:\n%s", got)
	}
}

func TestRenderListEmpty_Fresh(t *testing.T) {
	var buf bytes.Buffer
	RenderListEmpty(&buf, 0, 0)
	want := "No tasks. Run 'job import plan.md' or 'job --as <name> add \"<title>\"' to get started.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRenderListEmpty_AllDone(t *testing.T) {
	var buf bytes.Buffer
	RenderListEmpty(&buf, 3, 3)
	want := "Nothing actionable. 3 tasks done. Run 'list all' to see the full tree.\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
