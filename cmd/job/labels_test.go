package main

import (
	"encoding/json"
	job "github.com/bensyverson/jobs/internal/job"
	"os"
	"strings"
	"testing"
)

// --- job add -l/--label ---

func TestAddCLI_WithOneLabel(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "-l", "foo", "My Task")
	if err != nil {
		t.Fatalf("add with label: %v", err)
	}
	id := strings.TrimSpace(stdout)
	if len(id) != 5 {
		t.Fatalf("expected 5-char ID in output, got %q", stdout)
	}

	db2 := openTestDB(t, dbFile)
	defer db2.Close()
	task := job.MustGet(t, db2, id)
	labels, err := job.GetLabels(db2, task.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "foo" {
		t.Errorf("labels: got %v, want [foo]", labels)
	}
}

func TestAddCLI_WithMultipleLabels(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "-l", "foo", "-l", "bar", "-l", "baz", "Multi")
	if err != nil {
		t.Fatalf("add with labels: %v", err)
	}
	id := strings.TrimSpace(stdout)

	db2 := openTestDB(t, dbFile)
	defer db2.Close()
	task := job.MustGet(t, db2, id)
	labels, _ := job.GetLabels(db2, task.ID)
	if len(labels) != 3 {
		t.Errorf("labels: got %v, want 3", labels)
	}
}

func TestAddCLI_LabelWithComma_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "--as", "alice", "add", "-l", "foo,bar", "Task")
	if err == nil {
		t.Fatal("expected error for label with comma")
	}
	if !strings.Contains(err.Error(), "may not contain ','") {
		t.Errorf("err should mention comma rule: %v", err)
	}
}

func TestAddCLI_NoLabel_NoRegression(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "Plain Task")
	if err != nil {
		t.Fatalf("add without label: %v", err)
	}
	id := strings.TrimSpace(stdout)
	if len(id) != 5 {
		t.Fatalf("expected 5-char ID, got %q", id)
	}
}

// --- Runner tests ---

func TestLabelAdd_Single_Happy(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	res, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor)
	if err != nil {
		t.Fatalf("job.RunLabelAdd: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "foo" {
		t.Errorf("Added: got %v, want [foo]", res.Added)
	}
	if len(res.Existing) != 0 {
		t.Errorf("Existing: got %v, want []", res.Existing)
	}

	task := job.MustGet(t, db, id)
	labels, err := job.GetLabels(db, task.ID)
	if err != nil {
		t.Fatalf("job.GetLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "foo" {
		t.Errorf("labels: got %v, want [foo]", labels)
	}

	detail, err := job.GetLatestEventDetail(db, task.ID, "labeled")
	if err != nil || detail == nil {
		t.Fatalf("labeled event missing: err=%v detail=%v", err, detail)
	}
	names, _ := detail["names"].([]any)
	if len(names) != 1 || names[0].(string) != "foo" {
		t.Errorf("event names: got %v, want [foo]", names)
	}
}

func TestLabelAdd_Variadic(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	res, err := job.RunLabelAdd(db, id, []string{"foo", "bar", "baz"}, job.TestActor)
	if err != nil {
		t.Fatalf("job.RunLabelAdd: %v", err)
	}
	if len(res.Added) != 3 {
		t.Errorf("Added: got %v, want 3", res.Added)
	}
	task := job.MustGet(t, db, id)
	labels, _ := job.GetLabels(db, task.ID)
	if len(labels) != 3 {
		t.Errorf("labels: got %v, want 3", labels)
	}

	// One event per call.
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'labeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("labeled events: got %d, want 1", n)
	}
}

func TestLabelAdd_Idempotent(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	if _, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	res, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 {
		t.Errorf("Added: got %v, want []", res.Added)
	}
	if len(res.Existing) != 1 || res.Existing[0] != "foo" {
		t.Errorf("Existing: got %v, want [foo]", res.Existing)
	}

	task := job.MustGet(t, db, id)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'labeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("labeled events: got %d, want 1 (no event on idempotent re-add)", n)
	}
}

func TestLabelAdd_Mixed_SomeNew_SomeExisting(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	if _, err := job.RunLabelAdd(db, id, []string{"bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	res, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0] != "foo" {
		t.Errorf("Added: got %v, want [foo]", res.Added)
	}
	if len(res.Existing) != 1 || res.Existing[0] != "bar" {
		t.Errorf("Existing: got %v, want [bar]", res.Existing)
	}

	task := job.MustGet(t, db, id)
	detail, _ := job.GetLatestEventDetail(db, task.ID, "labeled")
	names, _ := detail["names"].([]any)
	existing, _ := detail["existing"].([]any)
	if len(names) != 2 {
		t.Errorf("event names len: got %d, want 2", len(names))
	}
	if len(existing) != 1 {
		t.Errorf("event existing len: got %d, want 1", len(existing))
	}
}

func TestLabelAdd_EmptyName_Errors(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	_, err := job.RunLabelAdd(db, id, []string{""}, job.TestActor)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "label name is empty" {
		t.Errorf("err: got %q, want %q", err.Error(), "label name is empty")
	}
}

func TestLabelAdd_CommaInName_Errors(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	_, err := job.RunLabelAdd(db, id, []string{"foo,bar"}, job.TestActor)
	if err == nil {
		t.Fatal("expected error")
	}
	want := `label name "foo,bar" may not contain ','`
	if err.Error() != want {
		t.Errorf("err: got %q, want %q", err.Error(), want)
	}
}

func TestLabelAdd_WhitespaceTrimmed(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	if _, err := job.RunLabelAdd(db, id, []string{" foo "}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	task := job.MustGet(t, db, id)
	labels, _ := job.GetLabels(db, task.ID)
	if len(labels) != 1 || labels[0] != "foo" {
		t.Errorf("labels: got %v, want [foo]", labels)
	}
}

func TestLabelAdd_TaskNotFound_Errors(t *testing.T) {
	db := job.SetupTestDB(t)
	_, err := job.RunLabelAdd(db, "noExs", []string{"foo"}, job.TestActor)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"noExs" not found`) {
		t.Errorf("err: %v", err)
	}
}

func TestLabelAdd_DoesNotRequireClaim(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunLabelAdd(db, id, []string{"foo"}, "bob"); err != nil {
		t.Fatalf("bob should be able to label even when alice holds claim: %v", err)
	}
}

func TestLabelRemove_Happy(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}

	res, err := job.RunLabelRemove(db, id, []string{"foo"}, job.TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "foo" {
		t.Errorf("Removed: got %v, want [foo]", res.Removed)
	}
	task := job.MustGet(t, db, id)
	labels, _ := job.GetLabels(db, task.ID)
	if len(labels) != 1 || labels[0] != "bar" {
		t.Errorf("labels remaining: got %v, want [bar]", labels)
	}
}

func TestLabelRemove_Idempotent(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	res, err := job.RunLabelRemove(db, id, []string{"foo"}, job.TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed: got %v, want []", res.Removed)
	}
	if len(res.Absent) != 1 || res.Absent[0] != "foo" {
		t.Errorf("Absent: got %v, want [foo]", res.Absent)
	}

	task := job.MustGet(t, db, id)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'unlabeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("unlabeled events: got %d, want 0", n)
	}
}

func TestLabelRemove_PartiallyAbsent(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	res, err := job.RunLabelRemove(db, id, []string{"foo", "bar"}, job.TestActor)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "foo" {
		t.Errorf("Removed: got %v, want [foo]", res.Removed)
	}
	if len(res.Absent) != 1 || res.Absent[0] != "bar" {
		t.Errorf("Absent: got %v, want [bar]", res.Absent)
	}
}

// --- Schema & migration ---

func TestSchema_HasTaskLabelsTable(t *testing.T) {
	db := job.SetupTestDB(t)
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='task_labels'").Scan(&name)
	if err != nil {
		t.Fatalf("task_labels table missing: %v", err)
	}
}

// --- YAML import ---

func TestImport_PersistsLabels(t *testing.T) {
	db := job.SetupTestDB(t)
	tmp := t.TempDir() + "/plan.md"
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Root\n" +
		"    labels: [a, b]\n" +
		"```\n"
	if err := writeFile(tmp, body); err != nil {
		t.Fatal(err)
	}
	res, err := job.RunImport(db, tmp, "", false, job.TestActor)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Tasks) != 1 {
		t.Fatalf("tasks: %d", len(res.Tasks))
	}
	task := job.MustGet(t, db, res.Tasks[0].ID)
	labels, _ := job.GetLabels(db, task.ID)
	if len(labels) != 2 {
		t.Errorf("labels: got %v, want [a b]", labels)
	}

	// labeled event present alongside created.
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'labeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("labeled events: got %d, want 1", n)
	}
}

func TestImport_LabelValidation_RejectsCommaInName(t *testing.T) {
	db := job.SetupTestDB(t)
	tmp := t.TempDir() + "/plan.md"
	body := "```yaml\n" +
		"tasks:\n" +
		"  - title: Root\n" +
		"    labels: [\"a,b\"]\n" +
		"```\n"
	if err := writeFile(tmp, body); err != nil {
		t.Fatal(err)
	}
	_, err := job.RunImport(db, tmp, "", false, job.TestActor)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "labels[0]") {
		t.Errorf("err should reference labels path: %v", err)
	}
	if !strings.Contains(err.Error(), "may not contain ','") {
		t.Errorf("err should mention comma rule: %v", err)
	}
}

// --- Filters ---

func TestList_LabelFilter_MatchesOnly(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	if _, err := job.RunLabelAdd(db, a, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	nodes, err := job.RunListFiltered(db, job.ListFilter{Actor: job.TestActor, Label: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Task.ShortID != a {
		t.Errorf("nodes: got %+v, want [%s]; b=%s", nodes, a, b)
	}
}

func TestList_LabelFilter_NoMatch_Empty(t *testing.T) {
	db := job.SetupTestDB(t)
	job.MustAdd(t, db, "", "A")
	nodes, err := job.RunListFiltered(db, job.ListFilter{Actor: job.TestActor, Label: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Errorf("nodes: got %d, want 0", len(nodes))
	}
}

func TestNext_LabelFilter(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	if _, err := job.RunLabelAdd(db, b, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	task, err := job.RunNextFiltered(db, "", job.TestActor, "foo", false, false)
	if err != nil {
		t.Fatalf("next filtered: %v", err)
	}
	if task.ShortID != b {
		t.Errorf("next: got %s, want %s; a=%s", task.ShortID, b, a)
	}
}

func TestNextAll_LabelFilter(t *testing.T) {
	db := job.SetupTestDB(t)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	c := job.MustAdd(t, db, "", "C")
	if _, err := job.RunLabelAdd(db, a, []string{"x"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunLabelAdd(db, c, []string{"x"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	tasks, err := job.RunNextAllFiltered(db, "", job.TestActor, "x", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks: got %d, want 2 (a=%s b=%s c=%s)", len(tasks), a, b, c)
	}
}

// --- Display ---

func TestInfo_ShowsLabelsLine(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	info, err := job.RunInfo(db, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Labels) != 2 {
		t.Errorf("labels: got %v, want 2", info.Labels)
	}
}

func TestInfo_Json_IncludesLabels(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "info", id, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &arr); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1-element array, got %d", len(arr))
	}
	got := arr[0]
	labels, ok := got["labels"].([]any)
	if !ok {
		t.Fatalf("labels missing or wrong type: %v", got["labels"])
	}
	if len(labels) != 1 || labels[0].(string) != "foo" {
		t.Errorf("labels: got %v, want [foo]", labels)
	}
}

func TestInfo_Json_EmptyLabels(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "info", id, "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &arr); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1-element array, got %d", len(arr))
	}
	got := arr[0]
	if labels, ok := got["labels"].([]any); !ok || len(labels) != 0 {
		t.Errorf("labels: got %v, want []", got["labels"])
	}
}

func TestList_ShowsLabelsInParens(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "labels: foo") {
		t.Errorf("missing labels in parens:\n%s", stdout)
	}
}

func TestLog_LabeledEvent_Description(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	events, err := job.RunLog(db, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == "labeled" {
			desc := job.FormatEventDescription(e.EventType, e.Detail)
			want := "labeled: foo, bar"
			if desc != want {
				t.Errorf("desc: got %q, want %q", desc, want)
			}
			found = true
		}
	}
	if !found {
		t.Error("no labeled event in log")
	}
}

// --- CLI shape ---

func TestLabelCLI_Add_Md_Single_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", id, "foo")
	if err != nil {
		t.Fatal(err)
	}
	want := "Labeled: " + id + " (+foo)\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestLabelCLI_Add_Md_Mixed_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", id, "foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	want := "Labeled: " + id + " (+foo; already had: bar)\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestLabelCLI_AllExisting_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", id, "foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	want := "Already labeled: " + id + " (foo, bar)\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestLabelCLI_Remove_Shapes(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	if _, err := job.RunLabelAdd(db, id, []string{"foo", "bar"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "remove", id, "foo")
	if err != nil {
		t.Fatal(err)
	}
	want := "Unlabeled: " + id + " (-foo)\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}

	// Mixed
	stdout2, _, err := runCLI(t, dbFile, "--as", "alice", "label", "remove", id, "bar", "qux")
	if err != nil {
		t.Fatal(err)
	}
	wantMixed := "Unlabeled: " + id + " (-bar; was absent: qux)\n"
	if stdout2 != wantMixed {
		t.Errorf("got %q, want %q", stdout2, wantMixed)
	}

	// All absent
	stdout3, _, err := runCLI(t, dbFile, "--as", "alice", "label", "remove", id, "qux")
	if err != nil {
		t.Fatal(err)
	}
	wantAbsent := "Already unlabeled: " + id + " (qux)\n"
	if stdout3 != wantAbsent {
		t.Errorf("got %q, want %q", stdout3, wantAbsent)
	}
}

func TestLabelCLI_Json_Shape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "label", "add", id, "foo", "--format=json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if got["id"].(string) != id {
		t.Errorf("id: got %v, want %s", got["id"], id)
	}
	added, _ := got["added"].([]any)
	if len(added) != 1 || added[0].(string) != "foo" {
		t.Errorf("added: got %v", got["added"])
	}
	existing, _ := got["existing"].([]any)
	if len(existing) != 0 {
		t.Errorf("existing: got %v, want []", got["existing"])
	}
}

// --- Error polish ---

func TestDone_IncompleteSubtasks_SuggestsCascade(t *testing.T) {
	db := job.SetupTestDB(t)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child")

	_, _, err := job.RunDone(db, []string{parent}, false, "", nil, job.TestActor, false, "")
	if err == nil {
		t.Fatal("expected error")
	}
	wantSuffix := "(run 'job done --cascade " + parent + "' to close all)."
	if !strings.HasSuffix(err.Error(), wantSuffix) {
		t.Errorf("err should end with %q\n  got: %q", wantSuffix, err.Error())
	}
	if !strings.Contains(err.Error(), child) {
		t.Errorf("err should reference child id %s: %v", child, err)
	}
}

func TestClaim_AlreadyClaimedByYou_SuggestsHeartbeat(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatal(err)
	}
	err := job.RunClaim(db, id, "1h", "", "alice", false)
	if err == nil {
		t.Fatal("expected error")
	}
	want := "task " + id + " is already claimed by you. Use 'heartbeat' to refresh, or 'release' to stop."
	if err.Error() != want {
		t.Errorf("err:\n  got:  %q\n  want: %q", err.Error(), want)
	}
}

// writeFile is a tiny helper for tests so we don't import os everywhere.
func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o644)
}
