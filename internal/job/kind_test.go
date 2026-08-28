package job

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// mustSetKind flips a root's tree kind, failing the test on error.
func mustSetKind(t *testing.T, db *sql.DB, shortID string, kind TreeKind) *KindResult {
	t.Helper()
	res, err := RunSetKind(db, shortID, kind, TestActor)
	if err != nil {
		t.Fatalf("RunSetKind(%s, %s): %v", shortID, kind, err)
	}
	return res
}

func kindEvents(t *testing.T, db *sql.DB, shortID string) []EventEntry {
	t.Helper()
	all, err := RunLog(db, shortID, nil)
	if err != nil {
		t.Fatalf("RunLog(%s): %v", shortID, err)
	}
	var out []EventEntry
	for _, e := range all {
		if e.EventType == eventKindChanged {
			out = append(out, e)
		}
	}
	return out
}

func TestParseTreeKind(t *testing.T) {
	cases := []struct {
		in      string
		want    TreeKind
		wantErr bool
	}{
		{"task", KindTask, false},
		{"issue", KindIssue, false},
		{"Task", KindTask, false},
		{"  issue ", KindIssue, false},
		{"issues", "", true},
		{"", "", true},
		{"bug", "", true},
	}
	for _, c := range cases {
		got, err := ParseTreeKind(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseTreeKind(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTreeKind(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseTreeKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNewRootDefaultsToTaskKind(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Plan")
	child := MustAdd(t, db, root, "Leaf")

	if got := MustGet(t, db, root).Kind; got != KindTask {
		t.Errorf("root kind = %q, want %q", got, KindTask)
	}
	if got := MustGet(t, db, child).Kind; got != KindTask {
		t.Errorf("child kind = %q, want %q", got, KindTask)
	}
}

func TestRunAddKindCreatesIssueRoot(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAddKind(db, "", "Bugs", "", "", nil, TestActor, KindIssue)
	if err != nil {
		t.Fatalf("RunAddKind: %v", err)
	}
	if got := MustGet(t, db, res.ShortID).Kind; got != KindIssue {
		t.Errorf("kind = %q, want %q", got, KindIssue)
	}
}

func TestRunAddKindIssueUnderParentIsError(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Plan")
	_, err := RunAddKind(db, root, "Bug", "", "", nil, TestActor, KindIssue)
	if err == nil {
		t.Fatal("want error creating an issue as a child, got nil")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error %q should mention that kind is root-only", err)
	}
}

func TestSetKindOnRootRecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Bugs")

	res := mustSetKind(t, db, root, KindIssue)
	if !res.Changed {
		t.Error("KindResult.Changed = false, want true")
	}
	if res.From != KindTask || res.To != KindIssue {
		t.Errorf("KindResult from/to = %q/%q, want task/issue", res.From, res.To)
	}
	if got := MustGet(t, db, root).Kind; got != KindIssue {
		t.Errorf("kind = %q, want %q", got, KindIssue)
	}

	evs := kindEvents(t, db, root)
	if len(evs) != 1 {
		t.Fatalf("kind_changed events = %d, want 1", len(evs))
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(evs[0].Detail), &detail); err != nil {
		t.Fatalf("unmarshal detail %q: %v", evs[0].Detail, err)
	}
	if detail["from"] != "task" || detail["to"] != "issue" {
		t.Errorf("detail = %v, want from=task to=issue", detail)
	}
	if evs[0].Actor != TestActor {
		t.Errorf("actor = %q, want %q", evs[0].Actor, TestActor)
	}
}

func TestSetKindOnNonRootIsError(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Plan")
	child := MustAdd(t, db, root, "Leaf")

	if _, err := RunSetKind(db, child, KindIssue, TestActor); err == nil {
		t.Fatal("want error setting kind on a non-root, got nil")
	}
	if got := MustGet(t, db, child).Kind; got != KindTask {
		t.Errorf("child kind = %q after failed set, want %q", got, KindTask)
	}
	if evs := kindEvents(t, db, child); len(evs) != 0 {
		t.Errorf("kind_changed events on child = %d, want 0", len(evs))
	}
}

func TestSetKindSameKindIsNoOpWithoutEvent(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Plan")

	res := mustSetKind(t, db, root, KindTask)
	if res.Changed {
		t.Error("KindResult.Changed = true for a no-op set, want false")
	}
	if evs := kindEvents(t, db, root); len(evs) != 0 {
		t.Errorf("kind_changed events = %d for a no-op set, want 0", len(evs))
	}
}

func TestSetKindRoundTripLosesNothing(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAddDesc(t, db, "", "Bugs", "the description")
	child := MustAdd(t, db, root, "Leaf")
	if err := RunNote(db, child, "a note", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := RunAddCriteria(db, root, []Criterion{{Label: "crit"}}, TestActor); err != nil {
		t.Fatalf("criteria: %v", err)
	}
	before, err := RunLog(db, root, nil)
	if err != nil {
		t.Fatalf("log: %v", err)
	}

	mustSetKind(t, db, root, KindIssue)
	mustSetKind(t, db, root, KindTask)

	got := MustGet(t, db, root)
	if got.Kind != KindTask {
		t.Errorf("kind = %q after round trip, want %q", got.Kind, KindTask)
	}
	if got.Title != "Bugs" || got.Description != "the description" {
		t.Errorf("title/desc mutated: %q / %q", got.Title, got.Description)
	}
	if MustGet(t, db, child).Title != "Leaf" {
		t.Error("child lost")
	}
	crits, err := GetCriteria(db, got.ID)
	if err != nil {
		t.Fatalf("GetCriteria: %v", err)
	}
	if len(crits) != 1 {
		t.Errorf("criteria = %d, want 1", len(crits))
	}
	after, err := RunLog(db, root, nil)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	// Nothing is rewritten: the pre-existing history is a prefix of the new
	// history, with exactly the two kind_changed events appended.
	if len(after) != len(before)+2 {
		t.Fatalf("events = %d after round trip, want %d", len(after), len(before)+2)
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].EventType != before[i].EventType {
			t.Fatalf("event %d rewritten: %+v vs %+v", i, after[i], before[i])
		}
	}
	evs := kindEvents(t, db, root)
	if len(evs) != 2 {
		t.Fatalf("kind_changed events = %d, want 2", len(evs))
	}
}

func TestReparentIssueRootUnderParentIsError(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	bugs := MustAdd(t, db, "", "Bugs")
	mustSetKind(t, db, bugs, KindIssue)

	err := RunReparent(db, bugs, plan, "", "", TestActor)
	if err == nil {
		t.Fatal("want error reparenting an issue root, got nil")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error %q should point at `job kind`", err)
	}
	if got := MustGet(t, db, bugs); got.ParentID != nil {
		t.Error("issue root was reparented despite the error")
	}
}

func TestReparentTaskRootStillWorks(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	other := MustAdd(t, db, "", "Other")

	if err := RunReparent(db, other, plan, "", "", TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}
	if MustGet(t, db, other).ParentID == nil {
		t.Error("task root was not reparented")
	}
}

// --- default readers -------------------------------------------------------

// seedMixedRoots builds one task tree and one issue tree, each with a single
// available leaf, and returns (taskLeaf, issueRoot, issueLeaf).
func seedMixedRoots(t *testing.T, db *sql.DB) (string, string, string) {
	t.Helper()
	plan := MustAdd(t, db, "", "Plan")
	taskLeaf := MustAdd(t, db, plan, "Task leaf")
	bugs := MustAdd(t, db, "", "Bugs")
	issueLeaf := MustAdd(t, db, bugs, "Issue leaf")
	mustSetKind(t, db, bugs, KindIssue)
	return taskLeaf, bugs, issueLeaf
}

func TestNextExcludesIssueTrees(t *testing.T) {
	db := SetupTestDB(t)
	taskLeaf, _, _ := seedMixedRoots(t, db)

	got, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if got.ShortID != taskLeaf {
		t.Errorf("next = %s, want the task-tree leaf %s", got.ShortID, taskLeaf)
	}
}

func TestNextWithOnlyIssueTreesFindsNothing(t *testing.T) {
	db := SetupTestDB(t)
	bugs := MustAdd(t, db, "", "Bugs")
	MustAdd(t, db, bugs, "Issue leaf")
	mustSetKind(t, db, bugs, KindIssue)

	if _, err := RunNext(db, "", TestActor); err == nil {
		t.Fatal("want error: an issue tree is not default-visible work")
	}
}

func TestNextIssuesTargetsIssueTrees(t *testing.T) {
	db := SetupTestDB(t)
	_, _, issueLeaf := seedMixedRoots(t, db)

	got, err := RunNextFiltered(db, "", TestActor, "", false, true)
	if err != nil {
		t.Fatalf("RunNextFiltered(--issues): %v", err)
	}
	if got.ShortID != issueLeaf {
		t.Errorf("next --issues = %s, want the issue-tree leaf %s", got.ShortID, issueLeaf)
	}
}

func TestNextAllExcludesIssueTreesAndIssuesIncludesThem(t *testing.T) {
	db := SetupTestDB(t)
	taskLeaf, _, issueLeaf := seedMixedRoots(t, db)

	plain, err := RunNextAllFiltered(db, "", TestActor, "", false, false)
	if err != nil {
		t.Fatalf("RunNextAllFiltered: %v", err)
	}
	if len(plain) != 1 || plain[0].ShortID != taskLeaf {
		t.Errorf("next all = %v, want only %s", shortIDs(plain), taskLeaf)
	}

	issues, err := RunNextAllFiltered(db, "", TestActor, "", false, true)
	if err != nil {
		t.Fatalf("RunNextAllFiltered(--issues): %v", err)
	}
	if len(issues) != 1 || issues[0].ShortID != issueLeaf {
		t.Errorf("next all --issues = %v, want only %s", shortIDs(issues), issueLeaf)
	}
}

func shortIDs(ts []*Task) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ShortID)
	}
	return out
}

func TestNextWithExplicitIssueScopeOverridesTheDefault(t *testing.T) {
	db := SetupTestDB(t)
	_, issueRoot, issueLeaf := seedMixedRoots(t, db)

	got, err := RunNext(db, issueRoot, TestActor)
	if err != nil {
		t.Fatalf("RunNext(issueRoot): %v", err)
	}
	if got.ShortID != issueLeaf {
		t.Errorf("next %s = %s, want %s", issueRoot, got.ShortID, issueLeaf)
	}
}

// Focus is per kind (4GMzO), so a focus on an issue root scopes `--issues`
// and nothing else — the bare-next contract is pinned by
// TestNext_IgnoresAnIssueFocus in focus_kind_test.go.

func TestNextIssuesFallsBackToForestWideWithOnlyATaskFocus(t *testing.T) {
	db := SetupTestDB(t)
	plan := MustAdd(t, db, "", "Plan")
	MustAdd(t, db, plan, "Task leaf")
	bugs := MustAdd(t, db, "", "Bugs")
	issueLeaf := MustAdd(t, db, bugs, "Issue leaf")
	mustSetKind(t, db, bugs, KindIssue)

	if _, err := SetFocus(db, plan, TestActor); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	got, err := RunNextFiltered(db, "", TestActor, "", false, true)
	if err != nil {
		t.Fatalf("RunNextFiltered(--issues) with a task focus: %v", err)
	}
	if got.ShortID != issueLeaf {
		t.Errorf("next --issues = %s, want %s (no issue focus means forest-wide)", got.ShortID, issueLeaf)
	}
}

func TestClaimNextExcludesIssueTreesAndIssuesIncludesThem(t *testing.T) {
	db := SetupTestDB(t)
	taskLeaf, _, issueLeaf := seedMixedRoots(t, db)

	got, err := RunClaimNext(db, "", "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if got.ShortID != taskLeaf {
		t.Fatalf("claim --next = %s, want %s", got.ShortID, taskLeaf)
	}

	got, err = RunClaimNextFiltered(db, "", "", "", TestActor, false, false, true)
	if err != nil {
		t.Fatalf("RunClaimNextFiltered(--issues): %v", err)
	}
	if got.ShortID != issueLeaf {
		t.Errorf("claim --next --issues = %s, want %s", got.ShortID, issueLeaf)
	}
}

func TestOrientExcludesIssueTreesAndIssuesIncludesThem(t *testing.T) {
	db := SetupTestDB(t)
	taskLeaf, _, issueLeaf := seedMixedRoots(t, db)

	view, err := RunOrient(db, "", "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient: %v", err)
	}
	if view.Header.Target != taskLeaf {
		t.Errorf("orient target = %s, want %s", view.Header.Target, taskLeaf)
	}

	view, err = RunOrientOpts(db, "", "", TestActor, false, true)
	if err != nil {
		t.Fatalf("RunOrientOpts(--issues): %v", err)
	}
	if view.Header.Target != issueLeaf {
		t.Errorf("orient --issues target = %s, want %s", view.Header.Target, issueLeaf)
	}
}

func TestOrientWithExplicitIdOnIssueTreeWorks(t *testing.T) {
	db := SetupTestDB(t)
	_, _, issueLeaf := seedMixedRoots(t, db)

	view, err := RunOrient(db, issueLeaf, "", TestActor)
	if err != nil {
		t.Fatalf("RunOrient(issueLeaf): %v", err)
	}
	if view.Header.Target != issueLeaf {
		t.Errorf("orient target = %s, want %s", view.Header.Target, issueLeaf)
	}
}

// --- rendering -------------------------------------------------------------

func TestListMarksIssueRoots(t *testing.T) {
	db := SetupTestDB(t)
	_, issueRoot, _ := seedMixedRoots(t, db)

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	var sb strings.Builder
	RenderMarkdownList(&sb, nodes, nil, nil, 0)
	out := sb.String()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, issueRoot) {
			if !strings.Contains(line, "issue-tree") {
				t.Errorf("issue root line %q does not mark the tree kind", line)
			}
			return
		}
	}
	t.Fatalf("issue root %s not in list output:\n%s", issueRoot, out)
}

func TestListDoesNotMarkTaskRoots(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "Plan")

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	var sb strings.Builder
	RenderMarkdownList(&sb, nodes, nil, nil, 0)
	if strings.Contains(sb.String(), "issue-tree") {
		t.Errorf("task root marked as an issue tree:\n%s", sb.String())
	}
}

func TestInfoRendersKindOnIssueRootsOnly(t *testing.T) {
	db := SetupTestDB(t)
	_, issueRoot, issueLeaf := seedMixedRoots(t, db)

	info, err := RunInfo(db, issueRoot)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var sb strings.Builder
	RenderInfoMarkdown(&sb, info)
	if !strings.Contains(sb.String(), "kind:") && !strings.Contains(sb.String(), "Kind:") {
		t.Errorf("show on an issue root does not print the kind:\n%s", sb.String())
	}

	info, err = RunInfo(db, issueLeaf)
	if err != nil {
		t.Fatalf("RunInfo(leaf): %v", err)
	}
	sb.Reset()
	RenderInfoMarkdown(&sb, info)
	if strings.Contains(sb.String(), "Kind:") {
		t.Errorf("show on a child of an issue root should not print a kind:\n%s", sb.String())
	}
}

func TestKindChangedEventDescription(t *testing.T) {
	got := FormatEventDescription(eventKindChanged, `{"from":"task","to":"issue"}`)
	if !strings.Contains(got, "issue") || !strings.Contains(got, "task") {
		t.Errorf("FormatEventDescription(kind_changed) = %q, want both kinds named", got)
	}
	if got == eventKindChanged {
		t.Errorf("FormatEventDescription fell through to the default for %q", eventKindChanged)
	}
}

func TestJSONCarriesKindOnIssueRootsOnly(t *testing.T) {
	db := SetupTestDB(t)
	taskLeaf, issueRoot, _ := seedMixedRoots(t, db)

	info, err := RunInfo(db, issueRoot)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var sb strings.Builder
	RenderInfoJSON(&sb, info)
	var got map[string]any
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", sb.String(), err)
	}
	if got["kind"] != "issue" {
		t.Errorf("show --format=json on an issue root: kind = %v, want \"issue\"", got["kind"])
	}

	// A task-tree node carries no kind at all: task is the default, and an
	// always-present field would change every existing consumer's payload.
	info, err = RunInfo(db, taskLeaf)
	if err != nil {
		t.Fatalf("RunInfo(taskLeaf): %v", err)
	}
	sb.Reset()
	RenderInfoJSON(&sb, info)
	// json.Unmarshal merges into a non-nil map, so start from a fresh one.
	got = nil
	if err := json.Unmarshal([]byte(sb.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := got["kind"]; ok {
		t.Errorf("task-tree node should carry no kind in JSON: %s", sb.String())
	}
}
