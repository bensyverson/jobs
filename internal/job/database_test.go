package job

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolveDBPath_FlagWins(t *testing.T) {
	t.Setenv("JOBS_DB", "/should/be/ignored")
	got := ResolveDBPath("/tmp/x.db")
	if got != "/tmp/x.db" {
		t.Errorf("ResolveDBPath(%q) = %q, want %q", "/tmp/x.db", got, "/tmp/x.db")
	}
}

func TestResolveDBPath_EnvWins(t *testing.T) {
	t.Setenv("JOBS_DB", "/tmp/env-chosen.db")
	got := ResolveDBPath("")
	if got != "/tmp/env-chosen.db" {
		t.Errorf("ResolveDBPath(\"\") = %q, want %q", got, "/tmp/env-chosen.db")
	}
}

func TestResolveDBPath_WalksUp(t *testing.T) {
	t.Setenv("JOBS_DB", "")
	root := t.TempDir()
	a := filepath.Join(root, "a")
	c := filepath.Join(a, "b", "c")
	if err := os.MkdirAll(c, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbFile := filepath.Join(a, ".jobs.db")
	if err := os.WriteFile(dbFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	t.Chdir(c)

	got := ResolveDBPath("")
	// Resolve symlinks for compare; macOS tempdirs often involve /var -> /private/var.
	wantAbs, _ := filepath.EvalSymlinks(dbFile)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != wantAbs {
		t.Errorf("ResolveDBPath = %q, want %q", got, dbFile)
	}
}

func TestResolveDBPath_NoAncestor_FallsBackToCwd(t *testing.T) {
	t.Setenv("JOBS_DB", "")
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(sub)

	got := ResolveDBPath("")
	if got != defaultDBName {
		t.Errorf("ResolveDBPath = %q, want literal %q", got, defaultDBName)
	}
}

// --- Add ---

func TestRunAdd_RootTask(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Root task", "", "", nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	id := res.ShortID
	if len(id) != 6 {
		t.Fatalf("expected 6-char ID, got %q", id)
	}

	task := MustGet(t, db, id)
	if task.Title != "Root task" {
		t.Errorf("title: got %q, want %q", task.Title, "Root task")
	}
	if task.ParentID != nil {
		t.Errorf("parent_id: got %d, want nil", *task.ParentID)
	}
	if task.Status != "available" {
		t.Errorf("status: got %q, want %q", task.Status, "available")
	}
	if task.SortOrder != 0 {
		t.Errorf("sort_order: got %d, want 0", task.SortOrder)
	}
}

func TestRunAdd_Subtask(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cres, err := RunAdd(db, pid, "Child", "", "", nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	cid := cres.ShortID

	child := MustGet(t, db, cid)
	if child.ParentID == nil {
		t.Fatal("parent_id: got nil, want non-nil")
	}
	parent := MustGet(t, db, pid)
	if *child.ParentID != parent.ID {
		t.Errorf("parent_id: got %d, want %d", *child.ParentID, parent.ID)
	}
}

func TestRunAdd_WithDescription(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAddDesc(t, db, "", "Task", "Some description")
	task := MustGet(t, db, id)
	if task.Description != "Some description" {
		t.Errorf("description: got %q, want %q", task.Description, "Some description")
	}
}

func TestRunAdd_SortOrder(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First")
	id2 := MustAdd(t, db, "", "Second")
	id3 := MustAdd(t, db, "", "Third")

	t1 := MustGet(t, db, id1)
	t2 := MustGet(t, db, id2)
	t3 := MustGet(t, db, id3)

	if t1.SortOrder >= t2.SortOrder {
		t.Errorf("First sort_order %d >= Second %d", t1.SortOrder, t2.SortOrder)
	}
	if t2.SortOrder >= t3.SortOrder {
		t.Errorf("Second sort_order %d >= Third %d", t2.SortOrder, t3.SortOrder)
	}
}

func TestRunAdd_Before(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First")
	id2 := MustAdd(t, db, "", "Last")
	id3res, err := RunAdd(db, "", "Middle", "", id2, nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd before: %v", err)
	}
	id3 := id3res.ShortID

	t1 := MustGet(t, db, id1)
	t2 := MustGet(t, db, id2)
	t3 := MustGet(t, db, id3)

	if t3.SortOrder >= t2.SortOrder {
		t.Errorf("Middle sort_order %d should be < Last %d", t3.SortOrder, t2.SortOrder)
	}
	if t1.SortOrder >= t3.SortOrder {
		t.Errorf("First sort_order %d should be < Middle %d", t1.SortOrder, t3.SortOrder)
	}
}

func TestRunAdd_ParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunAdd(db, "noExs", "Task", "", "", nil, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

func TestRunAdd_BeforeNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunAdd(db, "", "Task", "", "noExs", nil, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent before target")
	}
}

func TestRunAdd_BeforeNotSibling(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	otherID := MustAdd(t, db, "", "Other root")
	_, err := RunAdd(db, pid, "Child", "", otherID, nil, TestActor)
	if err == nil {
		t.Fatal("expected error when before target is not a sibling")
	}
}

func TestRunAdd_WithSingleLabel(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Labeled Task", "", "", []string{"foo"}, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	task := MustGet(t, db, res.ShortID)
	labels, err := GetLabels(db, task.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	if len(labels) != 1 || labels[0] != "foo" {
		t.Errorf("labels: got %v, want [foo]", labels)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'labeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("labeled events: got %d, want 1", n)
	}
}

func TestRunAdd_WithMultipleLabels(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Multi-labeled", "", "", []string{"a", "b", "c"}, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	task := MustGet(t, db, res.ShortID)
	labels, _ := GetLabels(db, task.ID)
	if len(labels) != 3 {
		t.Errorf("labels: got %v, want 3", labels)
	}
}

func TestRunAdd_LabelWithComma_Errors(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunAdd(db, "", "Task", "", "", []string{"foo,bar"}, TestActor)
	if err == nil {
		t.Fatal("expected error for label with comma")
	}
	if !strings.Contains(err.Error(), "may not contain ','") {
		t.Errorf("err: %v", err)
	}
}

func TestRunAdd_WithoutLabels_NoLabeledEvent(t *testing.T) {
	db := SetupTestDB(t)
	res, err := RunAdd(db, "", "Unlabeled", "", "", nil, TestActor)
	if err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	task := MustGet(t, db, res.ShortID)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM events WHERE task_id = ? AND event_type = 'labeled'", task.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("labeled events: got %d, want 0", n)
	}
}

func TestRunAdd_CreatedEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAddDesc(t, db, "", "My task", "my desc")
	task := MustGet(t, db, id)

	detail, err := GetLatestEventDetail(db, task.ID, "created")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected created event, got nil")
	}
	if detail["title"] != "My task" {
		t.Errorf("title: got %v, want %q", detail["title"], "My task")
	}
	if detail["description"] != "my desc" {
		t.Errorf("description: got %v, want %q", detail["description"], "my desc")
	}
}

// --- List ---

func TestRunList_Empty(t *testing.T) {
	db := SetupTestDB(t)
	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestRunList_RootTasks(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "Task A")
	MustAdd(t, db, "", "Task B")

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(nodes))
	}
	if nodes[0].Task.Title != "Task A" {
		t.Errorf("first: got %q, want %q", nodes[0].Task.Title, "Task A")
	}
	if nodes[1].Task.Title != "Task B" {
		t.Errorf("second: got %q, want %q", nodes[1].Task.Title, "Task B")
	}
}

func TestRunList_Subtasks(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, pid, "Child A")
	MustAdd(t, db, pid, "Child B")

	nodes, err := runList(db, pid, TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 children, got %d", len(nodes))
	}
}

func TestRunList_TreeStructure(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")
	MustAdd(t, db, cid, "Grandchild")

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("roots: got %d, want 1", len(nodes))
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("children: got %d, want 1", len(nodes[0].Children))
	}
	if len(nodes[0].Children[0].Children) != 1 {
		t.Fatalf("grandchildren: got %d, want 1", len(nodes[0].Children[0].Children))
	}
}

func TestRunList_DefaultExcludesDone(t *testing.T) {
	db := SetupTestDB(t)
	idAvail := MustAdd(t, db, "", "Available")
	idDone := MustAdd(t, db, "", "Done")
	MustDone(t, db, idDone)

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Task.ShortID != idAvail {
		t.Errorf("got %s, want %s", nodes[0].Task.ShortID, idAvail)
	}
}

func TestRunList_AllIncludesDone(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "Available")
	idDone := MustAdd(t, db, "", "Done")
	MustDone(t, db, idDone)

	nodes, err := runList(db, "", TestActor, true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestRunList_DoneChildHidden(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cidDone := MustAdd(t, db, pid, "Done child")
	MustAdd(t, db, pid, "Available child")
	MustDone(t, db, cidDone)

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("roots: got %d, want 1", len(nodes))
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("visible children: got %d, want 1", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Task.Title != "Available child" {
		t.Errorf("got %q, want %q", nodes[0].Children[0].Task.Title, "Available child")
	}
}

func TestRunList_ParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := runList(db, "noExs", TestActor, false)
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

// --- Done ---

func TestRunDone_LeafTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	closed, _, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone: %v", err)
	}
	if len(closed) != 1 || len(closed[0].CascadeClosed) != 0 {
		t.Errorf("closed: got %+v, want 1 target with no cascade", closed)
	}

	task := MustGet(t, db, id)
	if task.Status != "done" {
		t.Errorf("status: got %q, want %q", task.Status, "done")
	}
}

func TestRunDone_WithNote(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if _, _, err := RunDone(db, []string{id}, false, "abc1234", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	task := MustGet(t, db, id)
	if task.CompletionNote == nil || *task.CompletionNote != "abc1234" {
		t.Errorf("note: got %v, want %q", task.CompletionNote, "abc1234")
	}
}

func TestRunDone_IncompleteChildren(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, pid, "Incomplete child")

	_, _, err := RunDone(db, []string{pid}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected error for incomplete children")
	}
	if !strings.Contains(err.Error(), "Incomplete child") || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("error should mention incomplete children: %v", err)
	}
	if !strings.Contains(err.Error(), "--cascade") {
		t.Errorf("error should suggest --cascade: %v", err)
	}
}

func TestRunDone_CascadeClosesChildren(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")

	closed, _, err := RunDone(db, []string{pid}, true, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone --cascade: %v", err)
	}
	if len(closed) != 1 || len(closed[0].CascadeClosed) != 1 || closed[0].CascadeClosed[0] != cid {
		t.Errorf("closed: got %+v, want 1 target cascading [%s]", closed, cid)
	}

	child := MustGet(t, db, cid)
	if child.Status != "done" {
		t.Errorf("child status: got %q, want %q", child.Status, "done")
	}
}

func TestRunDone_CascadeNested(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")
	gcid := MustAdd(t, db, cid, "Grandchild")

	closed, _, err := RunDone(db, []string{pid}, true, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone --cascade: %v", err)
	}
	if len(closed) != 1 || len(closed[0].CascadeClosed) != 2 {
		t.Errorf("closed cascade count: got %+v, want 2", closed)
	}

	for _, id := range []string{cid, gcid} {
		task := MustGet(t, db, id)
		if task.Status != "done" {
			t.Errorf("%s status: got %q, want %q", id, task.Status, "done")
		}
	}
}

func TestRunDone_AlreadyDone(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustDone(t, db, id)

	closed, alreadyDone, err := RunDone(db, []string{id}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("RunDone on already-done should be idempotent: %v", err)
	}
	if len(alreadyDone) != 1 || alreadyDone[0] != id {
		t.Errorf("alreadyDone: got %v, want [%s]", alreadyDone, id)
	}
	if len(closed) != 0 {
		t.Errorf("closed: got %+v, want 0", closed)
	}
}

func TestRunDone_BlockedTaskSucceeds(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	closed, _, err := RunDone(db, []string{blocked}, false, "", nil, TestActor, false, "")
	if err != nil {
		t.Fatalf("done on blocked task should succeed: %v", err)
	}
	if len(closed) != 1 {
		t.Errorf("closed: got %+v, want 1", closed)
	}
}

func TestRunDone_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, _, err := RunDone(db, []string{"noExs"}, false, "", nil, TestActor, false, "")
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunDone_DoneEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if _, _, err := RunDone(db, []string{id}, false, "abc1234", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "done")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected done event")
	}
	if detail["note"] != "abc1234" {
		t.Errorf("note: got %v, want %q", detail["note"], "abc1234")
	}
	if detail["cascade"] != false {
		t.Errorf("cascade: got %v, want false", detail["cascade"])
	}
}

func TestRunDone_CascadeEventRecordsChildren(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")

	if _, _, err := RunDone(db, []string{pid}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone: %v", err)
	}

	parent := MustGet(t, db, pid)
	detail, err := GetLatestEventDetail(db, parent.ID, "done")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	children, ok := detail["cascade_closed"].([]any)
	if !ok {
		t.Fatalf("cascade_closed: got %T, want []any", detail["cascade_closed"])
	}
	if len(children) != 1 {
		t.Fatalf("cascade_closed: got %d, want 1", len(children))
	}
	if children[0] != cid {
		t.Errorf("cascade_closed[0]: got %v, want %s", children[0], cid)
	}
}

// --- Reopen ---

func TestRunReopen_DoneTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustDone(t, db, id)

	reopened, err := RunReopen(db, id, false, TestActor)
	if err != nil {
		t.Fatalf("RunReopen: %v", err)
	}
	if len(reopened) != 0 {
		t.Errorf("reopened children: got %d, want 0", len(reopened))
	}

	task := MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status: got %q, want %q", task.Status, "available")
	}
	if task.CompletionNote != nil {
		t.Errorf("completion_note: got %v, want nil", task.CompletionNote)
	}
}

func TestRunReopen_CascadeReopensChildren(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")

	if _, _, err := RunDone(db, []string{pid}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone --cascade: %v", err)
	}

	reopened, err := RunReopen(db, pid, true, TestActor)
	if err != nil {
		t.Fatalf("RunReopen: %v", err)
	}
	if len(reopened) != 1 || reopened[0] != cid {
		t.Errorf("reopened: got %v, want [%s]", reopened, cid)
	}

	child := MustGet(t, db, cid)
	if child.Status != "available" {
		t.Errorf("child status: got %q, want %q", child.Status, "available")
	}
}

func TestRunReopen_AvailableTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	_, err := RunReopen(db, id, false, TestActor)
	if err == nil {
		t.Fatal("expected error when reopening available task")
	}
}

func TestRunReopen_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunReopen(db, "noExs", false, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunReopen_ReopenedEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustDone(t, db, id)

	if _, err := RunReopen(db, id, false, TestActor); err != nil {
		t.Fatalf("RunReopen: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "reopened")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected reopened event")
	}
}

func TestRunReopen_CascadeNested(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")
	gcid := MustAdd(t, db, cid, "Grandchild")

	if _, _, err := RunDone(db, []string{pid}, true, "", nil, TestActor, false, ""); err != nil {
		t.Fatalf("RunDone --cascade: %v", err)
	}

	reopened, err := RunReopen(db, pid, true, TestActor)
	if err != nil {
		t.Fatalf("RunReopen: %v", err)
	}
	if len(reopened) != 2 {
		t.Errorf("reopened: got %d, want 2", len(reopened))
	}

	for _, id := range []string{cid, gcid} {
		task := MustGet(t, db, id)
		if task.Status != "available" {
			t.Errorf("%s status: got %q, want %q", id, task.Status, "available")
		}
	}
}

// --- Edit ---

func TestRunEdit_ChangesTitle(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Old title")

	nt := "New title"
	if err := RunEdit(db, id, &nt, nil, TestActor); err != nil {
		t.Fatalf("RunEdit: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Title != "New title" {
		t.Errorf("title: got %q, want %q", task.Title, "New title")
	}
}

func TestRunEdit_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Old title")

	nt := "New title"
	if err := RunEdit(db, id, &nt, nil, TestActor); err != nil {
		t.Fatalf("RunEdit: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "edited")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected edited event")
	}
	if detail["old_title"] != "Old title" {
		t.Errorf("old_title: got %v, want %q", detail["old_title"], "Old title")
	}
	if detail["new_title"] != "New title" {
		t.Errorf("new_title: got %v, want %q", detail["new_title"], "New title")
	}
}

func TestRunEdit_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	nt := "New title"
	err := RunEdit(db, "noExs", &nt, nil, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

// --- Note ---

func TestRunNote_LeavesEmptyDescriptionUnchanged(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunNote(db, id, "First note", nil, TestActor); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Description != "" {
		t.Errorf("description should remain empty, got %q", task.Description)
	}
}

func TestRunNote_DoesNotMutateExistingDescription(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAddDesc(t, db, "", "Task", "Original desc")

	if err := RunNote(db, id, "Added note", nil, TestActor); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Description != "Original desc" {
		t.Errorf("description should equal %q, got %q", "Original desc", task.Description)
	}
	if strings.Contains(task.Description, "Added note") {
		t.Errorf("description should not contain note body: %q", task.Description)
	}
}

func TestRunNote_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunNote(db, id, "A note", nil, TestActor); err != nil {
		t.Fatalf("RunNote: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected noted event")
	}
	if detail["text"] != "A note" {
		t.Errorf("text: got %v, want %q", detail["text"], "A note")
	}
	if _, present := detail["description_after"]; present {
		t.Errorf("description_after should no longer be recorded; detail=%v", detail)
	}
}

func TestRunNote_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	err := RunNote(db, "noExs", "A note", nil, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

// --- Move ---

func TestRunMove_Before(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First")
	id2 := MustAdd(t, db, "", "Second")

	if err := RunMove(db, id2, "before", id1, TestActor); err != nil {
		t.Fatalf("RunMove: %v", err)
	}

	t1 := MustGet(t, db, id1)
	t2 := MustGet(t, db, id2)
	if t2.SortOrder >= t1.SortOrder {
		t.Errorf("Second (sort %d) should be before First (sort %d)", t2.SortOrder, t1.SortOrder)
	}
}

func TestRunMove_After(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First")
	id2 := MustAdd(t, db, "", "Second")

	if err := RunMove(db, id1, "after", id2, TestActor); err != nil {
		t.Fatalf("RunMove: %v", err)
	}

	t1 := MustGet(t, db, id1)
	t2 := MustGet(t, db, id2)
	if t1.SortOrder <= t2.SortOrder {
		t.Errorf("First (sort %d) should be after Second (sort %d)", t1.SortOrder, t2.SortOrder)
	}
}

func TestRunMove_NotSiblings(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")
	other := MustAdd(t, db, "", "Other root")

	err := RunMove(db, cid, "before", other, TestActor)
	if err == nil {
		t.Fatal("expected error when moving non-siblings")
	}
}

func TestRunMove_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First")
	id2 := MustAdd(t, db, "", "Second")
	task2 := MustGet(t, db, id2)

	if err := RunMove(db, id2, "before", id1, TestActor); err != nil {
		t.Fatalf("RunMove: %v", err)
	}

	detail, err := GetLatestEventDetail(db, task2.ID, "moved")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected moved event")
	}
	if detail["direction"] != "before" {
		t.Errorf("direction: got %v, want %q", detail["direction"], "before")
	}
	if detail["relative_to"] != id1 {
		t.Errorf("relative_to: got %v, want %s", detail["relative_to"], id1)
	}
}

func TestRunMove_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	err := RunMove(db, id, "before", "noExs", TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent target")
	}
}

// --- Reparent ---

func TestRunReparent_MovesUnderNewParent(t *testing.T) {
	db := SetupTestDB(t)
	oldParent := MustAdd(t, db, "", "Old parent")
	child := MustAdd(t, db, oldParent, "Child")
	newParent := MustAdd(t, db, "", "New parent")

	if err := RunReparent(db, child, newParent, "", "", TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	got := MustGet(t, db, child)
	newP := MustGet(t, db, newParent)
	if got.ParentID == nil || *got.ParentID != newP.ID {
		t.Errorf("child parent = %v, want %d", got.ParentID, newP.ID)
	}
}

func TestRunReparent_PlacesAtEndOfNewParent(t *testing.T) {
	db := SetupTestDB(t)
	source := MustAdd(t, db, "", "Source")
	movee := MustAdd(t, db, source, "Movee")

	dest := MustAdd(t, db, "", "Dest")
	first := MustAdd(t, db, dest, "First")
	second := MustAdd(t, db, dest, "Second")

	if err := RunReparent(db, movee, dest, "", "", TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	got := MustGet(t, db, movee)
	t1 := MustGet(t, db, first)
	t2 := MustGet(t, db, second)
	if got.SortOrder <= t1.SortOrder || got.SortOrder <= t2.SortOrder {
		t.Errorf("movee sort_order = %d; should be > first(%d) and second(%d)",
			got.SortOrder, t1.SortOrder, t2.SortOrder)
	}
}

func TestRunReparent_ToRoot(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	child := MustAdd(t, db, parent, "Child")

	if err := RunReparent(db, child, "", "", "", TestActor); err != nil {
		t.Fatalf("RunReparent to root: %v", err)
	}

	got := MustGet(t, db, child)
	if got.ParentID != nil {
		t.Errorf("child parent_id = %v, want nil (root)", *got.ParentID)
	}
}

func TestRunReparent_BeforeSiblingInNewParent(t *testing.T) {
	db := SetupTestDB(t)
	source := MustAdd(t, db, "", "Source")
	movee := MustAdd(t, db, source, "Movee")
	dest := MustAdd(t, db, "", "Dest")
	first := MustAdd(t, db, dest, "First")
	second := MustAdd(t, db, dest, "Second")

	if err := RunReparent(db, movee, dest, "before", second, TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	got := MustGet(t, db, movee)
	tFirst := MustGet(t, db, first)
	tSecond := MustGet(t, db, second)
	if !(got.SortOrder > tFirst.SortOrder && got.SortOrder < tSecond.SortOrder) {
		t.Errorf("movee sort_order = %d; expected between first(%d) and second(%d)",
			got.SortOrder, tFirst.SortOrder, tSecond.SortOrder)
	}
}

func TestRunReparent_AfterSiblingInNewParent(t *testing.T) {
	db := SetupTestDB(t)
	source := MustAdd(t, db, "", "Source")
	movee := MustAdd(t, db, source, "Movee")
	dest := MustAdd(t, db, "", "Dest")
	first := MustAdd(t, db, dest, "First")
	second := MustAdd(t, db, dest, "Second")

	if err := RunReparent(db, movee, dest, "after", first, TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	got := MustGet(t, db, movee)
	tFirst := MustGet(t, db, first)
	tSecond := MustGet(t, db, second)
	if !(got.SortOrder > tFirst.SortOrder && got.SortOrder < tSecond.SortOrder) {
		t.Errorf("movee sort_order = %d; expected between first(%d) and second(%d)",
			got.SortOrder, tFirst.SortOrder, tSecond.SortOrder)
	}
}

func TestRunReparent_PreventsCycleSelf(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	err := RunReparent(db, id, id, "", "", TestActor)
	if err == nil {
		t.Fatal("expected error when reparenting under self")
	}
}

func TestRunReparent_PreventsCycleDescendant(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	mid := MustAdd(t, db, root, "Mid")
	leaf := MustAdd(t, db, mid, "Leaf")

	if err := RunReparent(db, root, leaf, "", "", TestActor); err == nil {
		t.Fatal("expected error when reparenting under own descendant")
	}
}

func TestRunReparent_RecordsEventWithPriorParent(t *testing.T) {
	db := SetupTestDB(t)
	oldParent := MustAdd(t, db, "", "Old")
	child := MustAdd(t, db, oldParent, "Child")
	newParent := MustAdd(t, db, "", "New")

	task := MustGet(t, db, child)
	if err := RunReparent(db, child, newParent, "", "", TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	detail, err := GetLatestEventDetail(db, task.ID, "reparented")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected reparented event")
	}
	if detail["prior_parent_id"] != oldParent {
		t.Errorf("prior_parent_id: got %v, want %s", detail["prior_parent_id"], oldParent)
	}
	if detail["new_parent_id"] != newParent {
		t.Errorf("new_parent_id: got %v, want %s", detail["new_parent_id"], newParent)
	}
}

func TestRunReparent_RecordsPriorRootParent(t *testing.T) {
	db := SetupTestDB(t)
	child := MustAdd(t, db, "", "RootChild")
	newParent := MustAdd(t, db, "", "New")

	task := MustGet(t, db, child)
	if err := RunReparent(db, child, newParent, "", "", TestActor); err != nil {
		t.Fatalf("RunReparent: %v", err)
	}

	detail, err := GetLatestEventDetail(db, task.ID, "reparented")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected reparented event")
	}
	if detail["prior_parent_id"] != "" {
		t.Errorf("prior_parent_id: got %v, want empty (root)", detail["prior_parent_id"])
	}
	if detail["new_parent_id"] != newParent {
		t.Errorf("new_parent_id: got %v, want %s", detail["new_parent_id"], newParent)
	}
}

func TestRunReparent_TaskNotFound(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	if err := RunReparent(db, "noExs", parent, "", "", TestActor); err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunReparent_NewParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	if err := RunReparent(db, id, "noExs", "", "", TestActor); err == nil {
		t.Fatal("expected error for non-existent new parent")
	}
}

// --- Split ---

func TestRunSplit_AddsChildrenToLeaf(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent leaf")

	res, err := RunSplit(db, parent, []string{"Child A", "Child B", "Child C"}, TestActor)
	if err != nil {
		t.Fatalf("RunSplit: %v", err)
	}
	if len(res.ChildShortIDs) != 3 {
		t.Fatalf("expected 3 children, got %d", len(res.ChildShortIDs))
	}
	parentTask := MustGet(t, db, parent)
	for i, cid := range res.ChildShortIDs {
		c := MustGet(t, db, cid)
		if c.ParentID == nil || *c.ParentID != parentTask.ID {
			t.Errorf("child %d (%s) parent_id mismatch", i, cid)
		}
	}
}

func TestRunSplit_OrdersChildrenInInputOrder(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")

	res, err := RunSplit(db, parent, []string{"First", "Second", "Third"}, TestActor)
	if err != nil {
		t.Fatalf("RunSplit: %v", err)
	}
	first := MustGet(t, db, res.ChildShortIDs[0])
	second := MustGet(t, db, res.ChildShortIDs[1])
	third := MustGet(t, db, res.ChildShortIDs[2])
	if !(first.SortOrder < second.SortOrder && second.SortOrder < third.SortOrder) {
		t.Errorf("sort order not preserved: %d %d %d", first.SortOrder, second.SortOrder, third.SortOrder)
	}
	if first.Title != "First" || second.Title != "Second" || third.Title != "Third" {
		t.Errorf("titles mismatch: %q %q %q", first.Title, second.Title, third.Title)
	}
}

func TestRunSplit_RequiresLeaf(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, parent, "Existing child")

	_, err := RunSplit(db, parent, []string{"New child"}, TestActor)
	if err == nil {
		t.Fatal("expected error when splitting a non-leaf task")
	}
}

func TestRunSplit_NoTitles(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	_, err := RunSplit(db, parent, nil, TestActor)
	if err == nil {
		t.Fatal("expected error when called with zero titles")
	}
}

func TestRunSplit_ParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunSplit(db, "noExs", []string{"A"}, TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

func TestRunSplit_ReleasesClaimedParent(t *testing.T) {
	db := SetupTestDB(t)
	parent := MustAdd(t, db, "", "Parent")
	MustClaim(t, db, parent, "30m")

	res, err := RunSplit(db, parent, []string{"A", "B"}, TestActor)
	if err != nil {
		t.Fatalf("RunSplit: %v", err)
	}
	if res.AutoReleasedParent != parent {
		t.Errorf("AutoReleasedParent = %q, want %q", res.AutoReleasedParent, parent)
	}

	got := MustGet(t, db, parent)
	if got.Status != "available" {
		t.Errorf("parent status = %q, want available (claim auto-released)", got.Status)
	}
}

func TestRunReparent_SiblingNotInNewParent(t *testing.T) {
	db := SetupTestDB(t)
	movee := MustAdd(t, db, "", "Movee")
	dest := MustAdd(t, db, "", "Dest")
	other := MustAdd(t, db, "", "Other root sibling")

	err := RunReparent(db, movee, dest, "before", other, TestActor)
	if err == nil {
		t.Fatal("expected error when sibling is not in new parent")
	}
}

// --- Block ---

func TestRunBlock_CreatesRelationship(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")

	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	blocks, err := GetBlockers(db, blocked)
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blocks))
	}
	if blocks[0].ShortID != blocker {
		t.Errorf("blocker: got %s, want %s", blocks[0].ShortID, blocker)
	}
}

func TestRunBlock_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")

	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	blockedTask := MustGet(t, db, blocked)
	detail, err := GetLatestEventDetail(db, blockedTask.ID, "blocked")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected blocked event")
	}
}

func TestRunBlock_CircularDependency(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")

	if err := RunBlock(db, b, a, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	err := RunBlock(db, a, b, TestActor)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}
}

func TestRunBlock_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	err := RunBlock(db, id, "noExs", TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent blocker")
	}
}

// --- Unblock ---

func TestRunUnblock_RemovesRelationship(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")

	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	if err := RunUnblock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunUnblock: %v", err)
	}

	blocks, err := GetBlockers(db, blocked)
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 blockers after unblock, got %d", len(blocks))
	}
}

func TestRunUnblock_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")

	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}
	if err := RunUnblock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunUnblock: %v", err)
	}

	blockedTask := MustGet(t, db, blocked)
	detail, err := GetLatestEventDetail(db, blockedTask.ID, "unblocked")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected unblocked event")
	}
}

func TestRunUnblock_NotBlocked(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "A")
	b := MustAdd(t, db, "", "B")
	err := RunUnblock(db, a, b, TestActor)
	if err == nil {
		t.Fatal("expected error when unblocking non-blocked task")
	}
}

// --- JSON format ---

func TestFormatJSON_ListOutput(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, pid, "Child")

	nodes, err := runList(db, "", TestActor, true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}

	jsonBytes, err := FormatTaskNodesJSON(nodes)
	if err != nil {
		t.Fatalf("FormatTaskNodesJSON: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 root, got %d", len(result))
	}
	if result[0]["title"] != "Parent" {
		t.Errorf("title: got %v, want %q", result[0]["title"], "Parent")
	}
	children, ok := result[0]["children"].([]any)
	if !ok {
		t.Fatalf("children: got %T", result[0]["children"])
	}
	if len(children) != 1 {
		t.Errorf("children: got %d, want 1", len(children))
	}
}

// --- Info ---

func TestRunInfo_RootTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "My task")

	info, err := RunInfo(db, id)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	if info.Task.ShortID != id {
		t.Errorf("short_id: got %s, want %s", info.Task.ShortID, id)
	}
	if info.Task.Title != "My task" {
		t.Errorf("title: got %s, want %q", info.Task.Title, "My task")
	}
	if info.Parent != nil {
		t.Errorf("parent: got %v, want nil", info.Parent)
	}
}

func TestRunInfo_ChildTask(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")

	info, err := RunInfo(db, cid)
	if err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	if info.Parent == nil {
		t.Fatal("parent: got nil, want non-nil")
	}
	if info.Parent.ShortID != pid {
		t.Errorf("parent short_id: got %s, want %s", info.Parent.ShortID, pid)
	}
}

func TestRunInfo_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunInfo(db, "noExs")
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

// --- Duration parsing ---

func TestParseDuration_Seconds(t *testing.T) {
	got, err := ParseDuration("45s")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 45 {
		t.Errorf("got %d, want 45", got)
	}
}

func TestParseDuration_Minutes(t *testing.T) {
	got, err := ParseDuration("30m")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 1800 {
		t.Errorf("got %d, want 1800", got)
	}
}

func TestParseDuration_Hours(t *testing.T) {
	got, err := ParseDuration("4h")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 14400 {
		t.Errorf("got %d, want 14400", got)
	}
}

func TestParseDuration_Days(t *testing.T) {
	got, err := ParseDuration("2d")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 172800 {
		t.Errorf("got %d, want 172800", got)
	}
}

func TestParseDuration_Default(t *testing.T) {
	got, err := ParseDuration("")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != DefaultClaimTTLSeconds {
		t.Errorf("got %d, want %d (30m default)", got, DefaultClaimTTLSeconds)
	}
}

func TestParseDuration_DefaultIs30m(t *testing.T) {
	got, err := ParseDuration("")
	if err != nil {
		t.Fatalf("ParseDuration: %v", err)
	}
	if got != 1800 {
		t.Errorf("default TTL: got %d, want 1800", got)
	}
}

func TestParseDuration_InvalidUnit(t *testing.T) {
	_, err := ParseDuration("5x")
	if err == nil {
		t.Fatal("expected error for invalid unit")
	}
}

func TestParseDuration_NoNumber(t *testing.T) {
	_, err := ParseDuration("h")
	if err == nil {
		t.Fatal("expected error for missing number")
	}
}

// --- Claim ---

func mustRelease(t *testing.T, db *sql.DB, shortID string) {
	t.Helper()
	if err := RunRelease(db, shortID, "", TestActor); err != nil {
		t.Fatalf("release %s: %v", shortID, err)
	}
}

func TestRunClaim_Basic(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "", "", TestActor, false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want %q", task.Status, "claimed")
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != TestActor {
		t.Errorf("claimed_by: got %v, want %q", task.ClaimedBy, TestActor)
	}
	if task.ClaimExpiresAt == nil {
		t.Fatal("claim_expires_at: got nil, want non-nil")
	}
}

func TestRunClaim_WithDuration(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "4h", "", "", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want %q", task.Status, "claimed")
	}
}

func TestRunClaim_WithWho(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "", "", "Jesse", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	if task.ClaimedBy == nil || *task.ClaimedBy != "Jesse" {
		t.Errorf("claimed_by: got %v, want %q", task.ClaimedBy, "Jesse")
	}
}

func TestRunClaim_WithDurationAndWho(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "4h", "", "Jesse", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	if task.ClaimedBy == nil || *task.ClaimedBy != "Jesse" {
		t.Errorf("claimed_by: got %v, want %q", task.ClaimedBy, "Jesse")
	}
}

func TestRunClaim_AlreadyClaimed(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "")

	err := RunClaim(db, id, "", "", "", false)
	if err == nil {
		t.Fatal("expected error when claiming already-claimed task")
	}
	if !strings.Contains(err.Error(), "claimed by") {
		t.Errorf("error should mention who holds the claim: %v", err)
	}
}

func TestRunClaim_ForceOverride(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "")

	if err := RunClaim(db, id, "1h", "", "Agent-1", true); err != nil {
		t.Fatalf("RunClaim --force: %v", err)
	}

	task := MustGet(t, db, id)
	if task.ClaimedBy == nil || *task.ClaimedBy != "Agent-1" {
		t.Errorf("claimed_by: got %v, want %q", task.ClaimedBy, "Agent-1")
	}
}

func TestRunClaim_DoneTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustDone(t, db, id)

	err := RunClaim(db, id, "", "", "", false)
	if err == nil {
		t.Fatal("expected error when claiming done task")
	}
}

func TestRunClaim_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	err := RunClaim(db, "noExs", "", "", "", false)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunClaim_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	if err := RunClaim(db, id, "4h", "", "Jesse", false); err != nil {
		t.Fatalf("RunClaim: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "claimed")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected claimed event")
	}

	if detail["duration"] != "4h" {
		t.Errorf("duration: got %v, want %q", detail["duration"], "4h")
	}
	if detail["expires_at"] == nil {
		t.Error("expires_at should not be nil")
	}
}

func TestRunClaim_ExpiredClaimCanBeReclaimed(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	baseTime := time.Now()
	CurrentNowFunc = func() time.Time { return baseTime }
	MustClaim(t, db, id, "1h")

	CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	if err := RunClaim(db, id, "1h", "", "Agent-1", false); err != nil {
		t.Fatalf("RunClaim after expiry: %v", err)
	}

	task := MustGet(t, db, id)
	if task.ClaimedBy == nil || *task.ClaimedBy != "Agent-1" {
		t.Errorf("claimed_by: got %v, want %q", task.ClaimedBy, "Agent-1")
	}
}

// --- Release ---

func TestRunRelease_ClaimedTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "")

	if err := RunRelease(db, id, "", TestActor); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status: got %q, want %q", task.Status, "available")
	}
	if task.ClaimedBy != nil {
		t.Errorf("claimed_by: got %q, want nil", *task.ClaimedBy)
	}
	if task.ClaimExpiresAt != nil {
		t.Errorf("claim_expires_at: got %d, want nil", *task.ClaimExpiresAt)
	}
}

func TestRunRelease_RecordsEvent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "")

	if err := RunRelease(db, id, "", TestActor); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "released")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected released event")
	}

}

// Atomicity: when -m is supplied, the noted event and the released event
// must land in the same transaction — both or neither.
func TestRunRelease_WithNote_RecordsBothEventsAtomically(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "")

	if err := RunRelease(db, id, "wrap-up note", TestActor); err != nil {
		t.Fatalf("RunRelease: %v", err)
	}

	task := MustGet(t, db, id)
	notedDetail, err := GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail noted: %v", err)
	}
	if notedDetail["text"] != "wrap-up note" {
		t.Errorf("noted text: got %v, want %q", notedDetail["text"], "wrap-up note")
	}
	releasedDetail, err := GetLatestEventDetail(db, task.ID, "released")
	if err != nil {
		t.Fatalf("GetLatestEventDetail released: %v", err)
	}
	if releasedDetail == nil {
		t.Fatal("expected released event alongside noted event")
	}
	if task.Status != "available" {
		t.Errorf("status: got %q, want %q", task.Status, "available")
	}
}

func TestRunRelease_NotClaimed(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	err := RunRelease(db, id, "", TestActor)
	if err == nil {
		t.Fatal("expected error when releasing unclaimed task")
	}
}

func TestRunRelease_NotFound(t *testing.T) {
	db := SetupTestDB(t)
	err := RunRelease(db, "noExs", "", TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

// --- Claim expiry ---

func TestExpireStaleClaims_ExpiredClaimReset(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	baseTime := time.Now()
	CurrentNowFunc = func() time.Time { return baseTime }
	MustClaim(t, db, id, "1h")

	CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }
	if err := expireStaleClaims(db, TestActor); err != nil {
		t.Fatalf("expireStaleClaims: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Status != "available" {
		t.Errorf("status after expiry: got %q, want %q", task.Status, "available")
	}
	if task.ClaimedBy != nil {
		t.Errorf("claimed_by after expiry: got %q, want nil", *task.ClaimedBy)
	}
}

func TestExpireStaleClaims_RecordsEvent(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	baseTime := time.Now()
	CurrentNowFunc = func() time.Time { return baseTime }
	MustClaim(t, db, id, "1h")

	CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }
	if err := expireStaleClaims(db, TestActor); err != nil {
		t.Fatalf("expireStaleClaims: %v", err)
	}

	task := MustGet(t, db, id)
	detail, err := GetLatestEventDetail(db, task.ID, "claim_expired")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected claim_expired event")
	}

}

func TestExpireStaleClaims_ActiveClaimNotExpired(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "4h")

	if err := expireStaleClaims(db, TestActor); err != nil {
		t.Fatalf("expireStaleClaims: %v", err)
	}

	task := MustGet(t, db, id)
	if task.Status != "claimed" {
		t.Errorf("status: got %q, want %q", task.Status, "claimed")
	}
}

// --- Next ---

func TestRunNext_WithParent(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid1 := MustAdd(t, db, pid, "First child")
	MustAdd(t, db, pid, "Second child")

	task, err := RunNext(db, pid, TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if task.ShortID != cid1 {
		t.Errorf("got %s, want %s (lowest sort_order)", task.ShortID, cid1)
	}
}

func TestRunNext_NoParent(t *testing.T) {
	db := SetupTestDB(t)
	id1 := MustAdd(t, db, "", "First root")
	MustAdd(t, db, "", "Second root")

	task, err := RunNext(db, "", TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if task.ShortID != id1 {
		t.Errorf("got %s, want %s", task.ShortID, id1)
	}
}

func TestRunNext_NoAvailable(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")
	MustDone(t, db, cid)

	_, err := RunNext(db, pid, TestActor)
	if err == nil {
		t.Fatal("expected error when no tasks available")
	}
}

func TestRunNext_SkipsBlocked(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid1 := MustAdd(t, db, pid, "Blocked child")
	cid2 := MustAdd(t, db, pid, "Available child")
	blocker := MustAdd(t, db, "", "Blocker")
	if err := RunBlock(db, cid1, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	task, err := RunNext(db, pid, TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if task.ShortID != cid2 {
		t.Errorf("got %s, want %s (should skip blocked)", task.ShortID, cid2)
	}
}

func TestRunNext_SkipsClaimed(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid1 := MustAdd(t, db, pid, "Claimed child")
	cid2 := MustAdd(t, db, pid, "Available child")
	MustClaim(t, db, cid1, "")

	task, err := RunNext(db, pid, TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if task.ShortID != cid2 {
		t.Errorf("got %s, want %s (should skip claimed)", task.ShortID, cid2)
	}
}

func TestRunNext_SkipsDone(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid1 := MustAdd(t, db, pid, "Done child")
	cid2 := MustAdd(t, db, pid, "Available child")
	MustDone(t, db, cid1)

	task, err := RunNext(db, pid, TestActor)
	if err != nil {
		t.Fatalf("RunNext: %v", err)
	}
	if task.ShortID != cid2 {
		t.Errorf("got %s, want %s (should skip done)", task.ShortID, cid2)
	}
}

func TestRunNext_ParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunNext(db, "noExs", TestActor)
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

func TestRunNext_NoAvailableAtRoot(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Done root")
	MustDone(t, db, id)

	_, err := RunNext(db, "", TestActor)
	if err == nil {
		t.Fatal("expected error when no root tasks available")
	}
}

// --- ClaimNext ---

func TestRunClaimNext_WithParent(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Available child")

	task, err := RunClaimNext(db, pid, "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if task.ShortID != cid {
		t.Errorf("got %s, want %s", task.ShortID, cid)
	}

	updated := MustGet(t, db, cid)
	if updated.Status != "claimed" {
		t.Errorf("status: got %q, want %q", updated.Status, "claimed")
	}
}

func TestRunClaimNext_NoParent(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Root task")

	task, err := RunClaimNext(db, "", "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if task.ShortID != id {
		t.Errorf("got %s, want %s", task.ShortID, id)
	}
}

func TestRunClaimNext_WithDurationAndWho(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Available child")

	task, err := RunClaimNext(db, pid, "4h", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if task.ShortID != cid {
		t.Errorf("got %s, want %s", task.ShortID, cid)
	}

	updated := MustGet(t, db, cid)
	if updated.ClaimedBy == nil || *updated.ClaimedBy != TestActor {
		t.Errorf("claimed_by: got %v, want %q", updated.ClaimedBy, TestActor)
	}
}

func TestRunClaimNext_NoAvailable(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Done child")
	MustDone(t, db, cid)

	_, err := RunClaimNext(db, pid, "", TestActor, false)
	if err == nil {
		t.Fatal("expected error when no tasks available")
	}
}

func TestRunClaimNext_SkipsBlocked(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid1 := MustAdd(t, db, pid, "Blocked child")
	cid2 := MustAdd(t, db, pid, "Available child")
	blocker := MustAdd(t, db, "", "Blocker")
	if err := RunBlock(db, cid1, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	task, err := RunClaimNext(db, pid, "", TestActor, false)
	if err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}
	if task.ShortID != cid2 {
		t.Errorf("got %s, want %s (should skip blocked)", task.ShortID, cid2)
	}
}

func TestRunClaimNext_RecordsClaimedEvent(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Available child")

	if _, err := RunClaimNext(db, pid, "2h", TestActor, false); err != nil {
		t.Fatalf("RunClaimNext: %v", err)
	}

	task := MustGet(t, db, cid)
	detail, err := GetLatestEventDetail(db, task.ID, "claimed")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected claimed event")
	}

}

func TestRunClaimNext_ParentNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunClaimNext(db, "noExs", "", TestActor, false)
	if err == nil {
		t.Fatal("expected error for non-existent parent")
	}
}

// --- Auto-unblock on done ---

func TestRunDone_AutoUnblocks(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	MustDone(t, db, blocker)

	blockers, err := GetBlockers(db, blocked)
	if err != nil {
		t.Fatalf("GetBlockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Errorf("expected no blockers after blocker done, got %d", len(blockers))
	}
}

func TestRunDone_RecordsUnblockedEvent(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	MustDone(t, db, blocker)

	blockedTask := MustGet(t, db, blocked)
	detail, err := GetLatestEventDetail(db, blockedTask.ID, "unblocked")
	if err != nil {
		t.Fatalf("GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected unblocked event")
	}
	if detail["reason"] != "blocker_done" {
		t.Errorf("reason: got %v, want %q", detail["reason"], "blocker_done")
	}
}

// --- List excludes blocked/claimed ---

func TestRunList_ExcludesBlockedTasks(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}

	for _, node := range nodes {
		if node.Task.ShortID == blocked {
			t.Error("blocked task should not appear in default list")
		}
	}
}

func TestRunList_AllIncludesBlocked(t *testing.T) {
	db := SetupTestDB(t)
	blocker := MustAdd(t, db, "", "Blocker")
	blocked := MustAdd(t, db, "", "Blocked")
	if err := RunBlock(db, blocked, blocker, TestActor); err != nil {
		t.Fatalf("RunBlock: %v", err)
	}

	nodes, err := runList(db, "", TestActor, true)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}

	found := false
	for _, node := range nodes {
		if node.Task.ShortID == blocked {
			found = true
		}
	}
	if !found {
		t.Error("blocked task should appear in 'list all'")
	}
}

func TestRunList_ExcludesClaimedTasks(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Claimed task")
	MustClaim(t, db, id, "4h")

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}

	for _, node := range nodes {
		if node.Task.ShortID == id {
			t.Error("claimed task should not appear in default list")
		}
	}
}

// --- Log ---

func TestRunLog_SingleTask(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "My task")

	events, err := RunLog(db, id, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "created" {
		t.Errorf("event type: got %q, want %q", events[0].EventType, "created")
	}
	if events[0].ShortID != id {
		t.Errorf("short_id: got %q, want %q", events[0].ShortID, id)
	}
}

func TestRunLog_WithDescendants(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Child")

	events, err := RunLog(db, pid, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].EventType != "created" || events[0].ShortID != pid {
		t.Errorf("first event: got %q/%q, want created/%q", events[0].EventType, events[0].ShortID, pid)
	}
	if events[1].EventType != "created" || events[1].ShortID != cid {
		t.Errorf("second event: got %q/%q, want created/%q", events[1].EventType, events[1].ShortID, cid)
	}
}

func TestRunLog_VariousEventTypes(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "1h")
	mustRelease(t, db, id)

	events, err := RunLog(db, id, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	// A claim also emits focus_set when it lands outside the actor's
	// focused root (here: the first claim ever), so the lifecycle log is
	// created, claimed, focus_set, released.
	if len(events) != 4 {
		t.Fatalf("expected 4 events (created, claimed, focus_set, released), got %d", len(events))
	}
	if events[0].EventType != "created" {
		t.Errorf("event 0: got %q, want created", events[0].EventType)
	}
	if events[1].EventType != "claimed" {
		t.Errorf("event 1: got %q, want claimed", events[1].EventType)
	}
	if events[2].EventType != "focus_set" {
		t.Errorf("event 2: got %q, want focus_set", events[2].EventType)
	}
	if events[3].EventType != "released" {
		t.Errorf("event 3: got %q, want released", events[3].EventType)
	}
}

func TestRunLog_EventsOrderedByTime(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, pid, "Child A")
	MustAdd(t, db, pid, "Child B")

	events, err := RunLog(db, pid, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].CreatedAt < events[i-1].CreatedAt {
			t.Errorf("event %d (ts %d) < event %d (ts %d)", i, events[i].CreatedAt, i-1, events[i-1].CreatedAt)
		}
	}
}

func TestRunLog_DoesNotIncludeOtherBranches(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	MustAdd(t, db, pid, "Included child")
	otherID := MustAdd(t, db, "", "Other root")
	MustAdd(t, db, otherID, "Other child")

	events, err := RunLog(db, pid, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (parent + 1 child), got %d", len(events))
	}
	for _, e := range events {
		if e.ShortID == otherID {
			t.Error("should not include events from other roots")
		}
	}
}

func TestRunLog_TaskNotFound(t *testing.T) {
	db := SetupTestDB(t)
	_, err := RunLog(db, "noExs", nil)
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunLog_FormattedMarkdown(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent task")
	cid := MustAdd(t, db, pid, "Child task")
	MustDone(t, db, cid)

	events, err := RunLog(db, pid, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}

	var buf strings.Builder
	RenderEventLogMarkdown(&buf, events)
	output := buf.String()

	if !strings.Contains(output, "[") {
		t.Error("expected timestamp brackets in output")
	}
	if !strings.Contains(output, pid) {
		t.Error("expected parent short_id in output")
	}
	if !strings.Contains(output, cid) {
		t.Error("expected child short_id in output")
	}
	if !strings.Contains(output, "created") {
		t.Error("expected 'created' in output")
	}
}

func TestRunLog_FormattedJSON(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")
	MustClaim(t, db, id, "1h")

	events, err := RunLog(db, id, nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}

	jsonBytes, err := FormatEventLogJSON(events)
	if err != nil {
		t.Fatalf("FormatEventLogJSON: %v", err)
	}

	var result []map[string]any
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	// created, claimed, plus the claim's focus_set (first claim flips focus).
	if len(result) != 3 {
		t.Fatalf("expected 3 events, got %d", len(result))
	}
	if result[0]["event_type"] != "created" {
		t.Errorf("event 0 type: got %v, want created", result[0]["event_type"])
	}
	if result[1]["event_type"] != "claimed" {
		t.Errorf("event 1 type: got %v, want claimed", result[1]["event_type"])
	}
	if result[2]["event_type"] != "focus_set" {
		t.Errorf("event 2 type: got %v, want focus_set", result[2]["event_type"])
	}
	if result[0]["short_id"] != id {
		t.Errorf("event 0 short_id: got %v, want %s", result[0]["short_id"], id)
	}
}

// --- Tail ---

func TestGetEventsAfterID_ReturnsNewEvents(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	allEvents, err := GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}
	if len(allEvents) != 1 {
		t.Fatalf("expected 1 initial event, got %d", len(allEvents))
	}

	MustClaim(t, db, id, "1h")

	newEvents, err := getEventsAfterID(db, id, allEvents[0].ID)
	if err != nil {
		t.Fatalf("getEventsAfterID: %v", err)
	}
	// The claim emits claimed plus focus_set (first claim flips focus).
	if len(newEvents) != 2 {
		t.Fatalf("expected 2 new events, got %d", len(newEvents))
	}
	if newEvents[0].EventType != "claimed" {
		t.Errorf("event type: got %q, want claimed", newEvents[0].EventType)
	}
}

func TestGetEventsAfterID_NoNewEvents(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	allEvents, err := GetEventsForTaskTree(db, id)
	if err != nil {
		t.Fatalf("GetEventsForTaskTree: %v", err)
	}

	newEvents, err := getEventsAfterID(db, id, allEvents[0].ID)
	if err != nil {
		t.Fatalf("getEventsAfterID: %v", err)
	}
	if len(newEvents) != 0 {
		t.Errorf("expected 0 new events, got %d", len(newEvents))
	}
}

func TestRunTail_PicksUpNewEvents(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var mu sync.Mutex
	var collected []EventEntry
	initialDrained := make(chan struct{})
	claimSeen := make(chan struct{})
	done := make(chan struct{})

	go func() {
		sawInitial := false
		RunTail(ctx, db, id, 10*time.Millisecond, func(events []EventEntry) error {
			mu.Lock()
			collected = append(collected, events...)
			hasClaim := false
			for _, e := range events {
				if e.EventType == "claimed" {
					hasClaim = true
				}
			}
			mu.Unlock()
			if !sawInitial {
				sawInitial = true
				close(initialDrained)
			}
			if hasClaim {
				select {
				case <-claimSeen:
				default:
					close(claimSeen)
				}
				cancel()
			}
			return nil
		})
		close(done)
	}()

	// Wait for the tail to drain the pre-existing `created` event before
	// firing MustClaim — otherwise the first poll might include `created`
	// only, the callback cancels, and MustClaim's event never arrives.
	<-initialDrained
	MustClaim(t, db, id, "1h")
	<-claimSeen
	<-done

	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range collected {
		if e.EventType == "claimed" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find a claimed event")
	}
}

func TestRunTail_TaskNotFound(t *testing.T) {
	db := SetupTestDB(t)
	ctx := t.Context()

	err := RunTail(ctx, db, "noExs", 10*time.Millisecond, func(events []EventEntry) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestRunList_ClaimedByFilter_ShowsOnlyActorsClaims(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Alice task")
	b := MustAdd(t, db, "", "Bob task")
	if err := RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := RunClaim(db, b, "1h", "", "bob", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Task.ShortID != a {
		t.Errorf("got %s, want %s", nodes[0].Task.ShortID, a)
	}
}

func TestRunList_ClaimedByFilter_NoMatch(t *testing.T) {
	db := SetupTestDB(t)
	MustAdd(t, db, "", "Unclaimed")

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ClaimedByActor: "nobody"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(nodes))
	}
}

func TestRunList_ClaimedByFilter_PreservesParentContext(t *testing.T) {
	db := SetupTestDB(t)
	pid := MustAdd(t, db, "", "Parent")
	cid := MustAdd(t, db, pid, "Claimed child")
	MustAdd(t, db, pid, "Unclaimed child")
	if err := RunClaim(db, cid, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("roots: got %d, want 1", len(nodes))
	}
	if nodes[0].Task.ShortID != pid {
		t.Errorf("root: got %s, want %s (parent of claimed child)", nodes[0].Task.ShortID, pid)
	}
	if len(nodes[0].Children) != 1 {
		t.Fatalf("children: got %d, want 1 (only claimed child)", len(nodes[0].Children))
	}
	if nodes[0].Children[0].Task.ShortID != cid {
		t.Errorf("child: got %s, want %s", nodes[0].Children[0].Task.ShortID, cid)
	}
}

func TestRunList_ClaimedByFilter_WithLabelFilter(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Labeled and claimed")
	b := MustAdd(t, db, "", "Unlabeled but claimed")
	if _, err := RunLabelAdd(db, a, []string{"p0"}, TestActor); err != nil {
		t.Fatal(err)
	}
	if err := RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := RunClaim(db, b, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, Label: "p0", ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Task.ShortID != a {
		t.Errorf("expected only labeled+claimed task %s, got %+v", a, nodes)
	}
}

func TestRunList_ClaimedByFilter_ExcludesExpiredClaims(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Expiring")

	baseTime := time.Now()
	CurrentNowFunc = func() time.Time { return baseTime }
	if err := RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}

	CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }
	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes after claim expiry, got %d", len(nodes))
	}
}

func TestRunList_ClaimedByFilter_ExcludesDoneTasks(t *testing.T) {
	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Done by alice")
	if err := RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ShowAll: true, ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 nodes (done task should not match), got %d", len(nodes))
	}
}

func TestRunList_ClaimedByFilter_ComposesWithAll(t *testing.T) {
	db := SetupTestDB(t)
	a := MustAdd(t, db, "", "Claimed")
	b := MustAdd(t, db, "", "Available")
	if err := RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}

	nodes, err := RunListFiltered(db, ListFilter{Actor: TestActor, ShowAll: true, ClaimedByActor: "alice"})
	if err != nil {
		t.Fatalf("RunListFiltered: %v", err)
	}
	if len(nodes) != 1 || nodes[0].Task.ShortID != a {
		t.Errorf("expected only claimed task %s, got %+v", a, nodes)
	}
	_ = b
}

func TestRunList_ExpiredClaimShowsAsAvailable(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "Task")

	baseTime := time.Now()
	CurrentNowFunc = func() time.Time { return baseTime }
	MustClaim(t, db, id, "1h")

	CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	nodes, err := runList(db, "", TestActor, false)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after expiry, got %d", len(nodes))
	}
	if nodes[0].Task.ShortID != id {
		t.Errorf("got %s, want %s", nodes[0].Task.ShortID, id)
	}
	if nodes[0].Task.Status != "available" {
		t.Errorf("status: got %q, want %q", nodes[0].Task.Status, "available")
	}
}
