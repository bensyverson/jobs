package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	t.Cleanup(resetFlags)
	return path
}

func resetFlags() {
	dbPath = ""
	asFlag = ""
}

func runCLI(t *testing.T, dbFile string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetFlags()
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	full := append([]string{"--db", dbFile}, args...)
	root.SetArgs(full)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func runCLIWithStdin(t *testing.T, dbFile, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	resetFlags()
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetIn(bytes.NewBufferString(stdin))
	full := append([]string{"--db", dbFile}, args...)
	root.SetArgs(full)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := job.OpenDB(path)
	if err != nil {
		t.Fatalf("job.OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

const wantIdentityRequired = "identity required. Pass --as <name> before the verb."

func TestWriteRequiresAs(t *testing.T) {
	dbFile := setupCLI(t)

	// Seed: create a task using direct API, so we have a target id
	db := openTestDB(t, dbFile)
	idRes, err := job.RunAdd(db, "", "Seed", "", "", nil, "seeder")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := idRes.ShortID
	otherRes, err := job.RunAdd(db, "", "Other", "", "", nil, "seeder")
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	other := otherRes.ShortID
	db.Close()

	cases := []struct {
		name string
		args []string
	}{
		{"add", []string{"add", "New"}},
		{"done", []string{"done", id}},
		{"reopen", []string{"reopen", id}},
		{"edit", []string{"edit", id, "--title", "New title"}},
		{"note", []string{"note", id, "-m", "hello"}},
		{"cancel", []string{"cancel", id, "--reason", "x"}},
		{"move", []string{"move", id, "before", other}},
		{"block", []string{"block", id, "by", other}},
		{"unblock", []string{"unblock", id, "from", other}},
		{"claim", []string{"claim", id}},
		{"release", []string{"release", id}},
		{"claim --next", []string{"claim", "--next"}},
		{"heartbeat", []string{"heartbeat", id}},
		{"label add", []string{"label", "add", id, "foo"}},
		{"label remove", []string{"label", "remove", id, "foo"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := runCLI(t, dbFile, tc.args...)
			if err == nil {
				t.Fatalf("expected error for %s without --as", tc.name)
			}
			if err.Error() != wantIdentityRequired {
				t.Errorf("error:\n  got:  %q\n  want: %q", err.Error(), wantIdentityRequired)
			}
		})
	}
}

func TestReadDoesNotRequireAs(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	res, err := job.RunAdd(db, "", "Seed", "", "", nil, "seeder")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := res.ShortID
	db.Close()

	cases := [][]string{
		{"list"},
		{"list", "all"},
		{"info", id},
		{"log", id},
		{"next"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, _, err := runCLI(t, dbFile, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClaimNextRequiresAsEvenWhenReadPartSucceeds(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	if _, err := job.RunAdd(db, "", "Seed", "", "", nil, "seeder"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "claim", "--next")
	if err == nil {
		t.Fatal("expected claim --next without --as to error")
	}
	if err.Error() != wantIdentityRequired {
		t.Errorf("got %q, want %q", err.Error(), wantIdentityRequired)
	}
}

func TestWriteCreatesUserLazily(t *testing.T) {
	dbFile := setupCLI(t)

	_, _, err := runCLI(t, dbFile, "--as", "newname", "add", "x")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	db := openTestDB(t, dbFile)
	user, err := job.GetUserByName(db, "newname")
	if err != nil {
		t.Fatalf("job.GetUserByName: %v", err)
	}
	if user == nil {
		t.Fatal("user should have been created lazily")
	}
}

func TestWriteAttributesEvent(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "x")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	id := strings.TrimSpace(stdout)

	db := openTestDB(t, dbFile)
	task, err := job.GetTaskByShortID(db, id)
	if err != nil || task == nil {
		t.Fatalf("get task: %v", err)
	}
	detail, err := job.GetLatestEventDetail(db, task.ID, "created")
	if err != nil {
		t.Fatalf("job.GetLatestEventDetail: %v", err)
	}
	if detail == nil {
		t.Fatal("expected created event")
	}

	var actor string
	if err := db.QueryRow("SELECT actor FROM events WHERE task_id = ? AND event_type = 'created'", task.ID).Scan(&actor); err != nil {
		t.Fatalf("select actor: %v", err)
	}
	if actor != "alice" {
		t.Errorf("actor: got %q, want %q", actor, "alice")
	}
}

func TestClaimConflict(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}

	_, _, err := job.RunDone(db, []string{id}, false, "", nil, "bob", false, "")
	if err == nil {
		t.Fatal("expected bob done to error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "claimed by alice") {
		t.Errorf("error should name alice: %v", err)
	}
	if !strings.Contains(msg, "Wait for expiry, or ask alice to release") {
		t.Errorf("error should guide user: %v", err)
	}
	if !strings.Contains(msg, "expires in") {
		t.Errorf("error should mention expiry: %v", err)
	}
}

func TestStolenClaim(t *testing.T) {
	origNow := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = origNow }()

	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	baseTime := time.Now()
	job.CurrentNowFunc = func() time.Time { return baseTime }
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}

	// Advance past TTL so alice's claim expires; bob claims.
	job.CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }
	if err := job.RunClaim(db, id, "1h", "", "bob", false); err != nil {
		t.Fatalf("bob claim: %v", err)
	}

	// Move time forward a bit so "claimed X ago" reads non-zero.
	job.CurrentNowFunc = func() time.Time { return baseTime.Add(2*time.Hour + 5*time.Minute) }

	_, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, "")
	if err == nil {
		t.Fatal("expected alice done to error (stolen claim)")
	}
	msg := err.Error()
	if !strings.Contains(msg, "your claim on "+id+" expired") {
		t.Errorf("error should mention expired claim: %v", err)
	}
	if !strings.Contains(msg, "now held by bob") {
		t.Errorf("error should name bob: %v", err)
	}
	if !strings.Contains(msg, "Run 'claim "+id+"' to take it back") {
		t.Errorf("error should suggest reclaim: %v", err)
	}
}

func TestReleaseWrongHolder(t *testing.T) {
	db := job.SetupTestDB(t)
	id := job.MustAdd(t, db, "", "job.Task")

	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	err := job.RunRelease(db, id, "", "bob")
	if err == nil {
		t.Fatal("expected bob release to error")
	}
	msg := err.Error()
	want := "task " + id + " is claimed by alice, not you. 'release' operates only on your own claims."
	if msg != want {
		t.Errorf("error:\n  got:  %q\n  want: %q", msg, want)
	}
}

func TestNoEnvFallback(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	res, err := job.RunAdd(db, "", "Seed", "", "", nil, "seeder")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := res.ShortID
	db.Close()

	t.Setenv("JOBS_USER", "bob")
	t.Setenv("JOBS_KEY", "somekey")

	_, _, err = runCLI(t, dbFile, "done", id)
	if err == nil {
		t.Fatal("expected error; env vars should not satisfy identity")
	}
	if err.Error() != wantIdentityRequired {
		t.Errorf("got %q, want %q", err.Error(), wantIdentityRequired)
	}
}

func TestLoginVerbRemoved(t *testing.T) {
	dbFile := setupCLI(t)
	for _, verb := range []string{"login", "logout"} {
		t.Run(verb, func(t *testing.T) {
			_, _, err := runCLI(t, dbFile, verb)
			if err == nil {
				t.Fatalf("expected %q to be unknown", verb)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("error: got %v, want unknown command", err)
			}
		})
	}
}

func TestImport_RequiresAs(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "```yaml\ntasks:\n  - title: x\n```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	_, _, err := runCLI(t, dbFile, "import", planPath)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != wantIdentityRequired {
		t.Errorf("error:\n  got:  %q\n  want: %q", err.Error(), wantIdentityRequired)
	}
}

func TestImport_CLI_HappyPath(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "# Plan\n\n```yaml\n" +
		"tasks:\n" +
		"  - title: Root A\n" +
		"  - title: Root B\n" +
		"```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), stdout)
	}
	if !strings.HasSuffix(lines[0], "Root A") {
		t.Errorf("line 0 title: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "Root B") {
		t.Errorf("line 1 title: %q", lines[1])
	}

	db := openTestDB(t, dbFile)
	fields := strings.Fields(lines[0])
	ta, _ := job.GetTaskByShortID(db, fields[0])
	if ta == nil || ta.Title != "Root A" {
		t.Errorf("Root A not in db")
	}
}

func TestImport_CLI_DryRunJSON(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "```yaml\ntasks:\n  - title: One\n  - title: Two\n```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath, "--dry-run", "--format=json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if got["dry_run"] != true {
		t.Errorf("dry_run flag: %v", got["dry_run"])
	}
	tasks, ok := got["tasks"].([]any)
	if !ok || len(tasks) != 2 {
		t.Fatalf("tasks: %v", got["tasks"])
	}
	for _, tt := range tasks {
		m := tt.(map[string]any)
		id, _ := m["id"].(string)
		if !strings.HasPrefix(id, "<new-") {
			t.Errorf("dry-run id should be a placeholder, got %q", id)
		}
	}

	// No rows written.
	db := openTestDB(t, dbFile)
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("dry-run wrote %d tasks", n)
	}
}

func TestImport_DryRun_ShowsBlockedByEdges_MD(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "```yaml\ntasks:\n  - title: A\n  - title: B\n    blockedBy: [A]\n  - title: C\n    blockedBy: [B]\n```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, "blocked on <new-1>") {
		t.Errorf("expected B row to mention 'blocked on <new-1>':\n%s", stdout)
	}
	if !strings.Contains(stdout, "blocked on <new-2>") {
		t.Errorf("expected C row to mention 'blocked on <new-2>':\n%s", stdout)
	}
}

func TestImport_DryRun_RendersIndentedTree_MD(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "```yaml\ntasks:\n" +
		"  - title: Root\n" +
		"    children:\n" +
		"      - title: Child A\n" +
		"      - title: Child B\n" +
		"        blockedBy: [Child A]\n" +
		"        children:\n" +
		"          - title: Grandchild\n" +
		"```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), stdout)
	}
	// Root: no leading indent, checkbox shape.
	if !strings.HasPrefix(lines[0], "- [ ] ") {
		t.Errorf("root line should start with `- [ ] `: %q", lines[0])
	}
	if !strings.Contains(lines[0], "Root") {
		t.Errorf("root line missing title: %q", lines[0])
	}
	// Children: two-space indent.
	for _, i := range []int{1, 2} {
		if !strings.HasPrefix(lines[i], "  - [ ] ") {
			t.Errorf("child line %d should start with two-space indented checkbox: %q", i, lines[i])
		}
	}
	// Child B carries the blocker on Child A — must remain on the indented line.
	if !strings.Contains(lines[2], "Child B") {
		t.Fatalf("line 2 should be Child B: %q", lines[2])
	}
	if !strings.Contains(lines[2], "blocked on <new-2>") {
		t.Errorf("Child B should still mention 'blocked on <new-2>': %q", lines[2])
	}
	// Grandchild: four-space indent.
	if !strings.HasPrefix(lines[3], "    - [ ] ") {
		t.Errorf("grandchild line should start with four-space indented checkbox: %q", lines[3])
	}
	if !strings.Contains(lines[3], "Grandchild") {
		t.Errorf("line 3 missing Grandchild: %q", lines[3])
	}
}

func TestImport_DryRun_ExistingDBTask_BlockedByRealID(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	existingID := job.MustAdd(t, db, "", "Existing")
	db.Close()

	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := fmt.Sprintf("```yaml\ntasks:\n  - title: New task\n    blockedBy: [%s]\n```\n", existingID)
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(stdout, "blocked on "+existingID) {
		t.Errorf("expected real ID %q in output:\n%s", existingID, stdout)
	}
}

func TestImport_DryRun_BlockedByEdges_JSON(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	body := "```yaml\ntasks:\n  - title: A\n  - title: B\n    blockedBy: [A]\n```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath, "--dry-run", "--format=json")
	if err != nil {
		t.Fatalf("dry-run json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	tasks, _ := got["tasks"].([]any)
	if len(tasks) != 2 {
		t.Fatalf("tasks len: %d", len(tasks))
	}
	bTask := tasks[1].(map[string]any)
	blockedBy, _ := bTask["blocked_by"].([]any)
	if len(blockedBy) != 1 || blockedBy[0].(string) != "<new-1>" {
		t.Errorf("B.blocked_by: got %v, want [<new-1>]", blockedBy)
	}
}

func TestList_Grep_SubstringMatch(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Fix the login bug")
	job.MustAdd(t, db, "", "Add user settings page")
	job.MustAdd(t, db, "", "login redirect issue")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--grep", "login")
	if err != nil {
		t.Fatalf("list --grep: %v", err)
	}
	if !strings.Contains(stdout, "Fix the login bug") {
		t.Errorf("expected 'Fix the login bug' in output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "login redirect issue") {
		t.Errorf("expected 'login redirect issue' in output:\n%s", stdout)
	}
	if strings.Contains(stdout, "Add user settings page") {
		t.Errorf("'Add user settings page' should not appear:\n%s", stdout)
	}
}

func TestList_Grep_CaseInsensitive(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Fix the LOGIN bug")
	job.MustAdd(t, db, "", "Unrelated task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--grep", "login")
	if err != nil {
		t.Fatalf("list --grep: %v", err)
	}
	if !strings.Contains(stdout, "Fix the LOGIN bug") {
		t.Errorf("expected case-insensitive match:\n%s", stdout)
	}
	if strings.Contains(stdout, "Unrelated task") {
		t.Errorf("'Unrelated task' should not appear:\n%s", stdout)
	}
}

func TestList_Grep_NoMatch_PrintsEmptyState(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Some task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--grep", "zzznomatch")
	if err != nil {
		t.Fatalf("list --grep: %v", err)
	}
	if !strings.Contains(stdout, "No tasks match") {
		t.Errorf("expected empty-state message, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "zzznomatch") {
		t.Errorf("expected pattern echoed in message, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--all") {
		t.Errorf("expected --all suggestion, got:\n%s", stdout)
	}
}

func TestList_Grep_ComposesWithLabel(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "login bug fix")
	b := job.MustAdd(t, db, "", "login unrelated")
	job.MustAdd(t, db, "", "other bug")
	if _, err := job.RunLabelAdd(db, a, []string{"bug"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	if _, err := job.RunLabelAdd(db, b, []string{"unrelated"}, job.TestActor); err != nil {
		t.Fatal(err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--grep", "login", "--label", "bug")
	if err != nil {
		t.Fatalf("list --grep --label: %v", err)
	}
	if !strings.Contains(stdout, "login bug fix") {
		t.Errorf("expected 'login bug fix' in output:\n%s", stdout)
	}
	if strings.Contains(stdout, "login unrelated") {
		t.Errorf("'login unrelated' should not appear:\n%s", stdout)
	}
}

func TestList_Grep_JSON(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Find me task")
	job.MustAdd(t, db, "", "Other task")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--grep", "find me", "--format=json")
	if err != nil {
		t.Fatalf("list --grep --format=json: %v", err)
	}
	var rows []any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stdout)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 result, got %d:\n%s", len(rows), stdout)
	}
}

func TestImport_YAMLParseError_Surfaced(t *testing.T) {
	dbFile := setupCLI(t)
	planPath := filepath.Join(filepath.Dir(dbFile), "plan.md")
	// Unquoted colon-in-value causes yaml.v3 "mapping values not allowed" error.
	body := "```yaml\ntasks:\n  - title: Surface Next: in status output\n```\n"
	if err := os.WriteFile(planPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	_, _, err := runCLI(t, dbFile, "--as", "alice", "import", planPath)
	if err == nil {
		t.Fatal("expected YAML parse error")
	}
	if !strings.Contains(err.Error(), "mapping values are not allowed") {
		t.Errorf("error should name the YAML parse failure, got: %v", err)
	}
}

func TestSchema_CLI_NoIdentityRequired(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "schema")
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("CLI output not valid JSON: %v\n%s", err, stdout)
	}

	var buf bytes.Buffer
	if err := job.RunSchema(&buf); err != nil {
		t.Fatal(err)
	}
	if stdout != buf.String() {
		t.Errorf("CLI and job.RunSchema differ:\n CLI:  %q\n func: %q", stdout, buf.String())
	}
}

func TestInit_StillUsesCwd_EvenUnderAncestor(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(a, "b")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing ancestor db.
	ancestor := filepath.Join(a, ".jobs.db")
	if _, err := job.CreateDB(ancestor); err != nil {
		t.Fatalf("job.CreateDB ancestor: %v", err)
	}

	t.Chdir(b)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root2 := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root2.SetOut(&outBuf)
	root2.SetErr(&errBuf)
	root2.SetArgs([]string{"init"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("init: %v (stderr=%s)", err, errBuf.String())
	}

	cwdDB := filepath.Join(b, ".jobs.db")
	if _, err := os.Stat(cwdDB); err != nil {
		t.Errorf("init did not create %s: %v", cwdDB, err)
	}
}

func TestDone_EnrichedAck_MidPhase(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Phase 1")
	c1 := job.MustAdd(t, db, parent, "Child 1")
	c2 := job.MustAdd(t, db, parent, "Child 2")
	_ = job.MustAdd(t, db, parent, "Child 3")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	wantHead := `Done: ` + c1 + ` "Child 1"`
	if !strings.Contains(stdout, wantHead) {
		t.Errorf("missing headline:\n%s", stdout)
	}
	wantNext := `  Next: ` + c2 + ` "Child 2"`
	if !strings.Contains(stdout, wantNext) {
		t.Errorf("missing Next line:\n%s", stdout)
	}
	wantParent := "  Parent " + parent + ": 1 of 3 complete"
	if !strings.Contains(stdout, wantParent) {
		t.Errorf("missing Parent line:\n%s", stdout)
	}
}

func TestDone_EnrichedAck_SkipBlocked(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Phase")
	c1 := job.MustAdd(t, db, parent, "C1")
	c2 := job.MustAdd(t, db, parent, "C2")
	c3 := job.MustAdd(t, db, parent, "C3")
	// Block C2 by some external blocker.
	blocker := job.MustAdd(t, db, "", "Blocker")
	if err := job.RunBlock(db, c2, blocker, job.TestActor); err != nil {
		t.Fatalf("block: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	want := "  Next sibling " + c2 + " is blocked on " + blocker + ". Skipping to " + c3 + "."
	if !strings.Contains(stdout, want) {
		t.Errorf("missing skip-blocked line:\n%s", stdout)
	}
}

func TestDone_EnrichedAck_LastChild_WithParentSibling(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	// Root -> Phase1, Phase2 each with child
	p1 := job.MustAdd(t, db, "", "Phase 1")
	p2 := job.MustAdd(t, db, "", "Phase 2")
	c1 := job.MustAdd(t, db, p1, "C1 only child")
	_ = p2
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	wantAuto := `  Auto-closed: ` + p1 + ` "Phase 1"`
	if !strings.Contains(stdout, wantAuto) {
		t.Errorf("missing auto-closed line:\n%s", stdout)
	}
	wantNext := `  Next: ` + p2 + ` "Phase 2"`
	if !strings.Contains(stdout, wantNext) {
		t.Errorf("missing Next line:\n%s", stdout)
	}
}

func TestDone_EnrichedAck_WholeTreeDone(t *testing.T) {
	// Under leaf-frontier semantics, closing the last open child auto-closes
	// every ancestor up to the root. The Auto-closed line for the root
	// already conveys "the whole tree just closed", so the "All tasks in X
	// complete" line is suppressed when the whole-tree root equals the
	// highest auto-closed ancestor (P7 improvement 1).
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	c1 := job.MustAdd(t, db, root, "C1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", c1)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	wantAuto := "  Auto-closed: " + root + " \"Root\""
	if !strings.Contains(stdout, wantAuto) {
		t.Errorf("missing Auto-closed line for root:\n%s", stdout)
	}
	if strings.Contains(stdout, "All tasks in "+root+" complete") {
		t.Errorf("whole-tree line should be suppressed (duplicates Auto-closed):\n%s", stdout)
	}
}

func TestDone_AlreadyDone_Unchanged(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "job.Task")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", id)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	want := "Already done: " + id + "\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestDone_Cascade_IncludesContext(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	_ = job.MustAdd(t, db, root, "C1")
	_ = job.MustAdd(t, db, root, "C2")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", root, "--cascade")
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(stdout, "and 2 subtasks") {
		t.Errorf("missing cascade count:\n%s", stdout)
	}
	if !strings.Contains(stdout, "All tasks in "+root+" complete") {
		t.Errorf("missing whole-tree context on cascade:\n%s", stdout)
	}
}

func TestList_EmptyState_HasDoneTasks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "X")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	want := "Nothing actionable. 1 tasks done. Run 'list all' to see the full tree.\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

// When scoped to a subtree, the empty-state count reflects only the
// subtree's descendants — not the global task count.
func TestList_EmptyState_Scoped_CountsSubtreeOnly(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Child")
	job.MustDone(t, db, child)
	// Extra global done tasks that must NOT appear in the scoped count.
	for range 5 {
		extra := job.MustAdd(t, db, "", "Extra")
		job.MustDone(t, db, extra)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list", parent)
	if err != nil {
		t.Fatalf("list <id>: %v", err)
	}
	want := "Nothing actionable. 1 tasks done. Run 'list all' to see the full tree.\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestList_EmptyState_FreshDB(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(stdout, "No tasks.") {
		t.Errorf("want fresh-db message:\n%s", stdout)
	}
	if !strings.Contains(stdout, "job import plan.md") {
		t.Errorf("want import hint:\n%s", stdout)
	}
}

func TestNext_Empty_WordingUpdated(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "next")
	if err == nil {
		t.Fatal("expected error when no available tasks")
	}
	want := "No available tasks. Run 'list all' to see blocked or claimed work."
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestInit_DefaultOutput_IncludesGitignoreHint(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	out := outBuf.String()
	if !strings.Contains(out, "Recommended .gitignore entries") {
		t.Errorf("expected hint:\n%s", out)
	}
	if !strings.Contains(out, ".jobs.db-shm") {
		t.Errorf("expected shm entry:\n%s", out)
	}
}

func TestInit_GitignoreFlag_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init", "--gitignore"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".jobs.db-shm") {
		t.Errorf(".gitignore missing -shm entry:\n%s", content)
	}
	if !strings.Contains(content, ".jobs.db-wal") {
		t.Errorf(".gitignore missing -wal entry:\n%s", content)
	}
	// .jobs.db itself is a recommended entry too: every project using Jobs
	// ends up ignoring it, and the agent-worktree workflow assumes it isn't
	// checked in.
	if !strings.Contains(content, "\n.jobs.db\n") {
		t.Errorf(".gitignore should include .jobs.db itself:\n%s", content)
	}
	out := outBuf.String()
	if !strings.Contains(out, "Wrote 3 entries to .gitignore") {
		t.Errorf("missing success output:\n%s", out)
	}
}

func TestInit_GitignoreFlag_AppendsExisting(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	existing := "node_modules/\n.env\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init", "--gitignore"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.HasPrefix(content, existing) {
		t.Errorf("original content clobbered:\n%s", content)
	}
	if !strings.Contains(content, ".jobs.db-shm") {
		t.Errorf("missing appended entry:\n%s", content)
	}
	if !strings.Contains(content, "# job\n") {
		t.Errorf("missing section header:\n%s", content)
	}
}

func TestInit_GitignoreFlag_Idempotent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	// First run
	root1 := newRootCmd()
	root1.SetOut(&bytes.Buffer{})
	root1.SetErr(&bytes.Buffer{})
	root1.SetArgs([]string{"init", "--gitignore"})
	if err := root1.Execute(); err != nil {
		t.Fatalf("init 1: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))

	// Second run: overwrite db, gitignore should be unchanged.
	resetFlags()
	root2 := newRootCmd()
	var outBuf bytes.Buffer
	root2.SetOut(&outBuf)
	root2.SetErr(&outBuf)
	root2.SetArgs([]string{"init", "--force", "--gitignore"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("init 2: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(first) != string(second) {
		t.Errorf("gitignore changed on second run:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if !strings.Contains(outBuf.String(), "already includes") {
		t.Errorf("expected 'already includes':\n%s", outBuf.String())
	}
}

func TestInit_GitignoreFlag_PartialPresent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	existing := ".jobs.db-shm\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init", "--gitignore"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, ".jobs.db-wal") {
		t.Errorf("missing -wal append:\n%s", content)
	}
	if !strings.Contains(content, "\n.jobs.db\n") {
		t.Errorf("missing .jobs.db append:\n%s", content)
	}
	// Only one -shm entry (not duplicated).
	if strings.Count(content, ".jobs.db-shm") != 1 {
		t.Errorf("duplicated -shm entry:\n%s", content)
	}
	out := outBuf.String()
	if !strings.Contains(out, "Wrote 2 entries") {
		t.Errorf("missing wrote message:\n%s", out)
	}
}

// TestInit_GitignoreFlag_GitActuallyIgnoresTheEntries drives real `git` on a
// real repo: the earlier tests only inspect the .gitignore text, which
// missed that a trailing inline comment makes git treat the whole line as
// one literal (never-matching) pattern. Skips if git isn't on PATH.
func TestInit_GitignoreFlag_GitActuallyIgnoresTheEntries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	gitInit := exec.Command("git", "init", "-q")
	gitInit.Dir = dir
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Skipf("git init failed, skipping: %v\n%s", err, out)
	}

	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init", "--gitignore"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, name := range []string{".jobs.db", ".jobs.db-shm", ".jobs.db-wal"} {
		check := exec.Command("git", "check-ignore", "-q", name)
		check.Dir = dir
		if err := check.Run(); err != nil {
			t.Errorf("git check-ignore -q %s: not ignored (%v)", name, err)
		}
	}
}

func TestHelp_MentionsCurrentVerbs(t *testing.T) {
	dbFile := setupCLI(t)
	_ = dbFile
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := outBuf.String()

	// Verbs that are registered AND should appear in help.
	// `remove` was retired in Phase 5 in favor of `cancel`.
	// `unblock` is a hidden deprecated alias for `block remove`; it still
	// works but is intentionally absent from help output.
	wantVerbs := []string{
		"init", "schema", "add", "import", "edit", "block",
		"move", "claim", "release", "note", "done", "reopen",
		"cancel", "ls", "show", "log", "status", "next", "tail",
		"heartbeat", "label",
	}
	for _, v := range wantVerbs {
		if !strings.Contains(out, v) {
			t.Errorf("help missing verb %q", v)
		}
	}
}

func TestHelp_PhaseGatedVerbsAnnotated(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := outBuf.String()
	for _, phrase := range []string{
		"next all",
		"tail --until-close",
	} {
		if !strings.Contains(out, phrase) {
			t.Errorf("help missing orchestration phrase %q", phrase)
		}
	}
	if strings.Contains(out, "(in next release)") {
		t.Errorf("help should not contain '(in next release)' annotations:\n%s", out)
	}
}

func TestHelp_Snapshot(t *testing.T) {
	// Snapshot test: lock the top-level help output.
	// When verbs are added/removed in future phases, update the golden
	// string deliberately.
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := outBuf.String()
	for _, mustHave := range []string{
		"QUICKSTART",
		"IDENTITY",
		"VERBS (grouped by role)",
		"OUTPUT",
		"ORCHESTRATION",
		"job import plan.md",
		"claim --next",
		"--format=json",
	} {
		if !strings.Contains(out, mustHave) {
			t.Errorf("help missing anchor phrase %q", mustHave)
		}
	}
}

func TestReadSideEffectsUseEmptyActor(t *testing.T) {
	origNow := job.CurrentNowFunc
	defer func() { job.CurrentNowFunc = origNow }()

	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	res, err := job.RunAdd(db, "", "Seed", "", "", nil, "seeder")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id := res.ShortID
	baseTime := time.Now()
	job.CurrentNowFunc = func() time.Time { return baseTime }
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	db.Close()

	// Advance past TTL. List (a read) should expire alice's claim with actor="".
	job.CurrentNowFunc = func() time.Time { return baseTime.Add(2 * time.Hour) }

	if _, _, err := runCLI(t, dbFile, "list"); err != nil {
		t.Fatalf("list: %v", err)
	}

	db = openTestDB(t, dbFile)
	task, err := job.GetTaskByShortID(db, id)
	if err != nil || task == nil {
		t.Fatalf("get task: %v", err)
	}

	var actor string
	if err := db.QueryRow(
		"SELECT actor FROM events WHERE task_id = ? AND event_type = 'claim_expired' ORDER BY id DESC LIMIT 1",
		task.ID,
	).Scan(&actor); err != nil {
		t.Fatalf("select actor: %v", err)
	}
	if actor != "" {
		t.Errorf("actor: got %q, want empty", actor)
	}
}

func TestList_Mine_ShowsClaimedTasks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alice task")
	b := job.MustAdd(t, db, "", "Bob task")
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := job.RunClaim(db, b, "1h", "", "bob", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--mine")
	if err != nil {
		t.Fatalf("list --mine: %v", err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("expected to see alice's task %s:\n%s", a, stdout)
	}
	if strings.Contains(stdout, b) {
		t.Errorf("should not show bob's task %s:\n%s", b, stdout)
	}
}

func TestList_Mine_NoAs_NoStrict_NoDefault_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	if err := job.RunClaim(db, id, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "list", "--mine")
	if err == nil {
		t.Fatal("expected error when --mine has no identity")
	}
	if !strings.Contains(err.Error(), "no identity to scope to") {
		t.Errorf("got %q, want identity error", err.Error())
	}
}

func TestList_ClaimedBy_ShowsClaimedTasks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alice task")
	b := job.MustAdd(t, db, "", "Bob task")
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := job.RunClaim(db, b, "1h", "", "bob", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list", "--claimed-by", "bob")
	if err != nil {
		t.Fatalf("list --claimed-by bob: %v", err)
	}
	if !strings.Contains(stdout, b) {
		t.Errorf("expected to see bob's task %s:\n%s", b, stdout)
	}
	if strings.Contains(stdout, a) {
		t.Errorf("should not show alice's task %s:\n%s", a, stdout)
	}
}

func TestList_ClaimedBy_ComposesWithAll(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Alice task")
	_ = job.MustAdd(t, db, "", "Other")
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list", "--claimed-by", "alice", "all")
	if err != nil {
		t.Fatalf("list --claimed-by alice all: %v", err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("expected to see alice's task %s:\n%s", a, stdout)
	}
}

func TestList_ClaimedBy_NoClaims_EmptyMessage(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Available")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list", "--claimed-by", "nobody")
	if err != nil {
		t.Fatalf("list --claimed-by nobody: %v", err)
	}
	if !strings.Contains(stdout, "No tasks claimed by nobody") {
		t.Errorf("expected friendly empty message:\n%s", stdout)
	}
}

func TestList_ClaimedBy_ComposesWithLabel(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Labeled claim")
	b := job.MustAdd(t, db, "", "Unlabeled claim")
	if _, err := job.RunLabelAdd(db, a, []string{"p0"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := job.RunClaim(db, b, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "list", "--claimed-by", "alice", "--label", "p0")
	if err != nil {
		t.Fatalf("list --claimed-by alice --label p0: %v", err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("expected to see labeled task %s:\n%s", a, stdout)
	}
	if strings.Contains(stdout, b) {
		t.Errorf("should not show unlabeled task %s:\n%s", b, stdout)
	}
}

func TestList_Mine_ComposesWithLabel(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Labeled claim")
	b := job.MustAdd(t, db, "", "Unlabeled claim")
	if _, err := job.RunLabelAdd(db, a, []string{"p0"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := job.RunClaim(db, a, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim a: %v", err)
	}
	if err := job.RunClaim(db, b, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim b: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--mine", "--label", "p0")
	if err != nil {
		t.Fatalf("list --mine --label p0: %v", err)
	}
	if !strings.Contains(stdout, a) {
		t.Errorf("expected to see labeled task %s:\n%s", a, stdout)
	}
	if strings.Contains(stdout, b) {
		t.Errorf("should not show unlabeled task %s:\n%s", b, stdout)
	}
}

func TestList_AllFlag_ShowsDone(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Finished task")
	job.MustClaim(t, db, id, "1h")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "--all")
	if err != nil {
		t.Fatalf("list --all: %v", err)
	}
	if !strings.Contains(stdout, "Finished task") {
		t.Errorf("expected done task in --all output:\n%s", stdout)
	}
}

func TestList_AllFlag_ComposesWithParent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	child := job.MustAdd(t, db, parent, "Done child")
	job.MustClaim(t, db, child, "1h")
	job.MustDone(t, db, child)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", parent, "--all")
	if err != nil {
		t.Fatalf("list <parent> --all: %v", err)
	}
	if !strings.Contains(stdout, "Done child") {
		t.Errorf("expected done child in scoped --all output:\n%s", stdout)
	}
}

func TestList_AllPositional_StillWorks(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Done task")
	job.MustClaim(t, db, id, "1h")
	job.MustDone(t, db, id)
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "list", "all")
	if err != nil {
		t.Fatalf("list all (positional): %v", err)
	}
	if !strings.Contains(stdout, "Done task") {
		t.Errorf("positional 'all' should still work:\n%s", stdout)
	}
}
