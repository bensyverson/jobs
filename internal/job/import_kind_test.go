package job

import (
	"strings"
	"testing"
)

// 9s2qL — `kind: task|issue` in the import grammar. Kind is a property of the
// root only, so the importer refuses it on any child (and on every row when
// --parent makes every imported task a child).

func TestImport_KindIssueOnRoot(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"    children:\n" +
		"      - title: Router drops the trailing slash\n" +
		"  - title: Ship v1\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	if len(res.Tasks) != 3 {
		t.Fatalf("tasks: got %d, want 3", len(res.Tasks))
	}

	root, err := GetTaskByShortID(db, res.Tasks[0].ID)
	if err != nil {
		t.Fatalf("GetTaskByShortID: %v", err)
	}
	if root.Kind != KindIssue {
		t.Errorf("root kind = %q, want %q", root.Kind, KindIssue)
	}
	if res.Tasks[0].Kind != string(KindIssue) {
		t.Errorf("echo kind = %q, want %q", res.Tasks[0].Kind, KindIssue)
	}

	// Children of an issue root are ordinary tasks.
	child, err := GetTaskByShortID(db, res.Tasks[1].ID)
	if err != nil {
		t.Fatalf("GetTaskByShortID child: %v", err)
	}
	if child.Kind != KindTask {
		t.Errorf("child kind = %q, want %q", child.Kind, KindTask)
	}

	// A root with no kind key stays a task-tree, and the echo stays quiet.
	other, err := GetTaskByShortID(db, res.Tasks[2].ID)
	if err != nil {
		t.Fatalf("GetTaskByShortID other root: %v", err)
	}
	if other.Kind != KindTask {
		t.Errorf("default root kind = %q, want %q", other.Kind, KindTask)
	}
	if res.Tasks[2].Kind != "" {
		t.Errorf("default root echo kind = %q, want empty", res.Tasks[2].Kind)
	}

	// The created event carries the kind, as `add --kind issue` does.
	events, err := RunLog(db, res.Tasks[0].ID, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == "created" && strings.Contains(e.Detail, `"kind":"issue"`) {
			found = true
		}
	}
	if !found {
		t.Errorf("created event should carry kind=issue; events: %+v", events)
	}
}

func TestImport_KindTaskOnRoot_IsExplicitDefault(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Ship v1\n" +
		"    kind: task\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", false, "alice")
	if err != nil {
		t.Fatalf("RunImport: %v", err)
	}
	root, err := GetTaskByShortID(db, res.Tasks[0].ID)
	if err != nil {
		t.Fatalf("GetTaskByShortID: %v", err)
	}
	if root.Kind != KindTask {
		t.Errorf("root kind = %q, want %q", root.Kind, KindTask)
	}
}

func TestImport_KindOnChild_Errors(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Ship v1\n" +
		"    children:\n" +
		"      - title: Write tests\n" +
		"      - title: Bugs\n" +
		"        kind: issue\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected an error for kind on a child")
	}
	if !strings.Contains(err.Error(), "tasks[0].children[1]") {
		t.Errorf("error should name the row, got: %v", err)
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should name the key, got: %v", err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("failed import wrote tasks: %d vs %d", got, before)
	}
}

// Even `kind: task` on a child is refused — the key is meaningless there, and
// silently accepting it would leave a value no reader consults.
func TestImport_KindTaskOnChild_Errors(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Ship v1\n" +
		"    children:\n" +
		"      - title: Write tests\n" +
		"        kind: task\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected an error for kind on a child")
	}
	if !strings.Contains(err.Error(), "tasks[0].children[0]") {
		t.Errorf("error should name the row, got: %v", err)
	}
}

func TestImport_KindInvalidValue_Errors(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: bug\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, "", false, "alice")
	if err == nil {
		t.Fatal("expected an error for an invalid kind")
	}
	if !strings.Contains(err.Error(), "tasks[0]") || !strings.Contains(err.Error(), "task|issue") {
		t.Errorf("error should name the row and the valid values, got: %v", err)
	}
}

// With --parent, every imported entry is a child of an existing task, so no
// row may carry a kind — including the plan's own top-level entries.
func TestImport_KindWithParentFlag_Errors(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Existing root")
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"```\n"
	path := writeTempPlan(t, body)

	_, err := RunImport(db, path, parent, false, "alice")
	if err == nil {
		t.Fatal("expected an error for kind under --parent")
	}
	if !strings.Contains(err.Error(), "tasks[0]") {
		t.Errorf("error should name the row, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--parent") {
		t.Errorf("error should explain that --parent makes every row a child, got: %v", err)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("failed import wrote tasks: %d vs %d", got, before)
	}
}

func TestImport_KindDryRun(t *testing.T) {
	db := SetupTestDB(t)
	before := countRows(t, db, "tasks")

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"  - title: Ship v1\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", true, "alice")
	if err != nil {
		t.Fatalf("RunImport dry: %v", err)
	}
	if res.Tasks[0].Kind != string(KindIssue) {
		t.Errorf("dry-run kind = %q, want %q", res.Tasks[0].Kind, KindIssue)
	}
	if res.Tasks[1].Kind != "" {
		t.Errorf("dry-run kind for a default root = %q, want empty", res.Tasks[1].Kind)
	}
	if got := countRows(t, db, "tasks"); got != before {
		t.Errorf("dry-run wrote tasks: %d vs %d", got, before)
	}
}

// The two new keys are part of the grammar, so they must not trip the
// unknown-key ("ignored N key(s)") warning.
func TestImport_KindAndFoundIn_NoUnknownKeyWarning(t *testing.T) {
	db := SetupTestDB(t)

	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Bugs\n" +
		"    kind: issue\n" +
		"    children:\n" +
		"      - title: Router drops the trailing slash\n" +
		"        foundIn: Bugs\n" +
		"```\n"
	path := writeTempPlan(t, body)

	res, err := RunImport(db, path, "", true, "alice")
	if err != nil {
		t.Fatalf("RunImport dry: %v", err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "import grammar") {
			t.Errorf("kind/foundIn should be in the grammar, got warning: %s", w)
		}
	}
}
