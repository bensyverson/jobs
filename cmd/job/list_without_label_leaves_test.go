package main

import (
	"encoding/json"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

func mustLabel(t *testing.T, dbFile, id string, names ...string) {
	t.Helper()
	args := append([]string{"--as", "alice", "label", "add", id}, names...)
	if _, _, err := runCLI(t, dbFile, args...); err != nil {
		t.Fatalf("label add %s %v: %v", id, names, err)
	}
}

// reviewFixture builds Root (open) with an open sibling and a closed
// Container holding two done leaves, one labeled `reviewed`.
func reviewFixture(t *testing.T) (dbFile, container, reviewed, unreviewed string) {
	t.Helper()
	dbFile = setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "open sibling")
	container = job.MustAdd(t, db, root, "Container")
	reviewed = job.MustAdd(t, db, container, "reviewed leaf")
	unreviewed = job.MustAdd(t, db, container, "unreviewed leaf")
	db.Close()
	mustLabel(t, dbFile, reviewed, "reviewed")
	db = openTestDB(t, dbFile)
	job.MustDone(t, db, reviewed)
	job.MustDone(t, db, unreviewed) // closes Container too
	db.Close()
	return dbFile, container, reviewed, unreviewed
}

func TestListWithoutLabel_ExcludesLabeledOpenTasks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "alpha")
	job.MustAdd(t, db, "", "beta")
	db.Close()
	mustLabel(t, dbFile, a, "p0")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--without-label", "p0")
	if err != nil {
		t.Fatalf("ls --without-label: %v", err)
	}
	if strings.Contains(stdout, "alpha") {
		t.Errorf("expected labeled task excluded, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "beta") {
		t.Errorf("expected unlabeled task kept, got:\n%s", stdout)
	}
}

func TestListWithoutLabel_ExcludesLabeledClosedTail(t *testing.T) {
	dbFile, _, reviewed, unreviewed := reviewFixture(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--status", "done", "--without-label", "reviewed")
	if err != nil {
		t.Fatalf("ls --status done --without-label: %v", err)
	}
	if strings.Contains(stdout, "`"+reviewed+"`") {
		t.Errorf("expected the reviewed leaf excluded, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "`"+unreviewed+"` unreviewed leaf") {
		t.Errorf("expected the unreviewed leaf listed, got:\n%s", stdout)
	}
}

func TestListWithoutLabel_ExcludesLabeledInJSON(t *testing.T) {
	dbFile, _, reviewed, unreviewed := reviewFixture(t)

	ids := closedTailIDs(t, dbFile, "ls", "--all", "--format", "json", "--without-label", "reviewed")
	if ids[reviewed] {
		t.Errorf("expected %s excluded from closed_tail, got %v", reviewed, ids)
	}
	if !ids[unreviewed] {
		t.Errorf("expected %s in closed_tail, got %v", unreviewed, ids)
	}
}

func TestListLeaves_DropsContainersFromTreeAndTail(t *testing.T) {
	dbFile, container, _, unreviewed := reviewFixture(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls", "--status", "done", "--leaves")
	if err != nil {
		t.Fatalf("ls --status done --leaves: %v", err)
	}
	if strings.Contains(stdout, "] `"+container+"`") { // a row, not a breadcrumb
		t.Errorf("expected the closed container dropped, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "`"+unreviewed+"`") {
		t.Errorf("expected the done leaf listed, got:\n%s", stdout)
	}
}

func TestListLeaves_DropsContainersInJSON(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	container := job.MustAdd(t, db, "", "Container")
	leaf := job.MustAdd(t, db, container, "leaf")
	job.MustAdd(t, db, "", "still open")
	job.MustDone(t, db, leaf) // closes the container root
	db.Close()

	ids := closedTailIDs(t, dbFile, "ls", "--all", "--format", "json", "--leaves")
	if ids[container] {
		t.Errorf("expected container %s dropped from closed_tail, got %v", container, ids)
	}
	if !ids[leaf] {
		t.Errorf("expected leaf %s in closed_tail, got %v", leaf, ids)
	}
}

// The review query: closed leaves in a window that lack the label.
func TestListWithoutLabelLeaves_ComposeWithSinceAndLabel(t *testing.T) {
	dbFile, container, reviewed, unreviewed := reviewFixture(t)
	mustLabel(t, dbFile, unreviewed, "audio")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "ls",
		"--status", "done", "--since", "1h", "--leaves",
		"--label", "audio", "--without-label", "reviewed")
	if err != nil {
		t.Fatalf("composed ls: %v", err)
	}
	if !strings.Contains(stdout, "`"+unreviewed+"`") {
		t.Errorf("expected the unreviewed audio leaf, got:\n%s", stdout)
	}
	for _, gone := range []string{reviewed, container} {
		if strings.Contains(stdout, "] `"+gone+"`") { // a row, not a breadcrumb
			t.Errorf("expected %s filtered out, got:\n%s", gone, stdout)
		}
	}
}

func closedTailIDs(t *testing.T, dbFile string, args ...string) map[string]bool {
	t.Helper()
	stdout, _, err := runCLI(t, dbFile, append([]string{"--as", "alice"}, args...)...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	var out struct {
		ClosedTail []struct {
			ID string `json:"id"`
		} `json:"closed_tail"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode %v: %v\n%s", args, err, stdout)
	}
	ids := map[string]bool{}
	for _, r := range out.ClosedTail {
		ids[r.ID] = true
	}
	return ids
}
