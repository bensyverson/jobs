package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// XtgBr / 2gD3H — `-F, --file <path>` is the file form of every verb's
// free-text body flag: an alias for `-m @<path>` (`--desc @<path>` on
// add/edit), with `-F -` reading stdin. Combining it with the inline flag or
// with a positional body is an error, as git does.

// writeBody writes contents to a temp file and returns its path.
func writeBody(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	return path
}

// assertConflictError checks that err is the shared "pass the body one way"
// conflict message and not, say, a parse error that would let these tests pass
// before -F exists at all.
func assertConflictError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a conflict error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "-F") || !strings.Contains(msg, "mutually exclusive") {
		t.Errorf("error should name -F and say the forms are mutually exclusive, got: %v", err)
	}
}

// notedText returns the text of the most recent `noted` event on a task.
func notedText(t *testing.T, dbFile, shortID string) string {
	t.Helper()
	db := openTestDB(t, dbFile)
	task := job.MustGet(t, db, shortID)
	detail, err := job.GetLatestEventDetail(db, task.ID, "noted")
	if err != nil {
		t.Fatalf("GetLatestEventDetail(noted): %v", err)
	}
	if detail == nil {
		t.Fatalf("no noted event on %s", shortID)
	}
	s, _ := detail["text"].(string)
	return s
}

// --- note ---------------------------------------------------------------

// voB: `job note <id> -F body.md` records the file's contents with the
// trailing newline trimmed, as git's commit-message cleanup does.
func TestNote_DashF_ReadsFileTrimmingTrailingNewline(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	want := "multi-line\nevidence with ```backticks```"
	path := writeBody(t, want+"\n\n")

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "note", id, "-F", path); err != nil {
		t.Fatalf("note -F: %v", err)
	}
	if got := notedText(t, dbFile, id); got != want {
		t.Errorf("noted text: got %q, want %q", got, want)
	}
}

// -F is exactly the `-m @path` spelling: both produce the same stored body.
func TestNote_DashF_MatchesDashMAtPath(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	viaF := job.MustAdd(t, db, "", "viaF")
	viaAt := job.MustAdd(t, db, "", "viaAt")
	db.Close()

	contents := "same body\ntrailing newline kept\n"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "note", viaF, "-F", path); err != nil {
		t.Fatalf("note -F: %v", err)
	}
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "note", viaAt, "-m", "@"+path); err != nil {
		t.Fatalf("note -m @path: %v", err)
	}
	f, at := notedText(t, dbFile, viaF), notedText(t, dbFile, viaAt)
	if f != at {
		t.Errorf("-F and -m @path disagree:\n -F: %q\n -m: %q", f, at)
	}
}

// D4e: `-F -` reads stdin, trimming trailing newlines exactly as `-m -` does.
func TestNote_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLIWithStdin(t, dbFile, "piped body\nline two\n", "--as", "alice", "note", id, "-F", "-"); err != nil {
		t.Fatalf("note -F -: %v", err)
	}
	if got, want := notedText(t, dbFile, id), "piped body\nline two"; got != want {
		t.Errorf("noted text: got %q, want %q", got, want)
	}
}

// F0P: -F with -m is an error.
func TestNote_DashF_WithDashM_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "note", id, "-m", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with -m")
	}
	assertConflictError(t, err)
}

// F0P: -F with a positional body is an error.
func TestNote_DashF_WithPositional_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "note", id, "positional body", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with a positional body")
	}
	assertConflictError(t, err)
}

// A missing -F path fails with a message naming the flag and the path.
func TestNote_DashF_MissingFile_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "note", id, "-F", "/nonexistent/body.md")
	if err == nil {
		t.Fatal("expected an error for a missing -F file")
	}
	if !strings.Contains(err.Error(), "-F") || !strings.Contains(err.Error(), "/nonexistent/body.md") {
		t.Errorf("error should name -F and the path: %v", err)
	}
}

// --- done ---------------------------------------------------------------

func TestDone_DashF_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	contents := "closing notes\nfrom a file"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-F", path); err != nil {
		t.Fatalf("done -F: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil || *task.CompletionNote != contents {
		t.Errorf("completion note: got %v, want %q", task.CompletionNote, contents)
	}
}

func TestDone_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLIWithStdin(t, dbFile, "piped close\n", "--as", "alice", "done", id, "-F", "-"); err != nil {
		t.Fatalf("done -F -: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.CompletionNote == nil || *task.CompletionNote != "piped close" {
		t.Errorf("completion note: got %v, want %q", task.CompletionNote, "piped close")
	}
}

// Multi-id done applies the one -F body to every id, as -m does.
func TestDone_DashF_AppliesToEveryID(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "A")
	b := job.MustAdd(t, db, "", "B")
	db.Close()

	contents := "batch close body"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "done", a, b, "-F", path); err != nil {
		t.Fatalf("done multi -F: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	for _, id := range []string{a, b} {
		task := job.MustGet(t, db2, id)
		if task.CompletionNote == nil || *task.CompletionNote != contents {
			t.Errorf("%s completion note: got %v, want %q", id, task.CompletionNote, contents)
		}
	}
}

func TestDone_DashF_WithDashM_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "done", id, "-m", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with -m")
	}
	assertConflictError(t, err)
}

// --- claim --------------------------------------------------------------

func TestClaim_DashF_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	contents := "starting context\nfrom a file"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "-F", path); err != nil {
		t.Fatalf("claim -F: %v", err)
	}
	if got := notedText(t, dbFile, id); got != contents {
		t.Errorf("noted text: got %q, want %q", got, contents)
	}
}

func TestClaim_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLIWithStdin(t, dbFile, "piped start\n", "--as", "alice", "claim", id, "-F", "-"); err != nil {
		t.Fatalf("claim -F -: %v", err)
	}
	if got := notedText(t, dbFile, id); got != "piped start" {
		t.Errorf("noted text: got %q, want %q", got, "piped start")
	}
}

func TestClaim_DashF_WithDashM_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "-m", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with -m")
	}
	assertConflictError(t, err)
}

// --- release ------------------------------------------------------------

func TestRelease_DashF_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id); err != nil {
		t.Fatalf("claim: %v", err)
	}

	contents := "handing off\ncontext in the latest note"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "release", id, "-F", path); err != nil {
		t.Fatalf("release -F: %v", err)
	}
	if got := notedText(t, dbFile, id); got != contents {
		t.Errorf("noted text: got %q, want %q", got, contents)
	}
}

func TestRelease_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := runCLIWithStdin(t, dbFile, "piped handoff\n", "--as", "alice", "release", id, "-F", "-"); err != nil {
		t.Fatalf("release -F -: %v", err)
	}
	if got := notedText(t, dbFile, id); got != "piped handoff" {
		t.Errorf("noted text: got %q, want %q", got, "piped handoff")
	}
}

func TestRelease_DashF_WithDashM_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id); err != nil {
		t.Fatalf("claim: %v", err)
	}
	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "release", id, "-m", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with -m")
	}
	assertConflictError(t, err)
}

// --- cancel -------------------------------------------------------------

// canceledReason returns the reason recorded on a task's `canceled` event.
func canceledReason(t *testing.T, dbFile, shortID string) string {
	t.Helper()
	db := openTestDB(t, dbFile)
	task := job.MustGet(t, db, shortID)
	detail, err := job.GetLatestEventDetail(db, task.ID, "canceled")
	if err != nil {
		t.Fatalf("GetLatestEventDetail(canceled): %v", err)
	}
	if detail == nil {
		t.Fatalf("no canceled event on %s", shortID)
	}
	s, _ := detail["reason"].(string)
	return s
}

func TestCancel_DashF_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	contents := "out of scope\nsee the linked thread"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "-F", path); err != nil {
		t.Fatalf("cancel -F: %v", err)
	}
	if got := canceledReason(t, dbFile, id); got != contents {
		t.Errorf("cancel reason: got %q, want %q", got, contents)
	}
}

func TestCancel_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	if _, _, err := runCLIWithStdin(t, dbFile, "piped reason\n", "--as", "alice", "cancel", id, "-F", "-"); err != nil {
		t.Fatalf("cancel -F -: %v", err)
	}
	if got := canceledReason(t, dbFile, id); got != "piped reason" {
		t.Errorf("cancel reason: got %q, want %q", got, "piped reason")
	}
}

// cancel's --reason/-m now routes through the same resolver, so `@path` works
// there as it does on the other four verbs.
func TestCancel_DashM_AtPath_ReadsFile(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	contents := "reason from a file"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "-m", "@"+path); err != nil {
		t.Fatalf("cancel -m @path: %v", err)
	}
	if got := canceledReason(t, dbFile, id); got != contents {
		t.Errorf("cancel reason: got %q, want %q", got, contents)
	}
}

func TestCancel_DashF_WithDashM_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "cancel", id, "-m", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with -m")
	}
	assertConflictError(t, err)
}

// --- add ----------------------------------------------------------------

// JZi: `job add <parent> <title> -F desc.md` stores the file as the description.
func TestAdd_DashF_StoresFileAsDescription(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	parent := job.MustAdd(t, db, "", "Parent")
	db.Close()

	contents := "a long description\nwith several lines"
	path := writeBody(t, contents)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", parent, "Child", "-F", path)
	if err != nil {
		t.Fatalf("add -F: %v", err)
	}
	id := strings.TrimSpace(strings.Split(strings.TrimSpace(stdout), "\n")[0])

	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.Description != contents {
		t.Errorf("description: got %q, want %q", task.Description, contents)
	}
}

func TestAdd_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLIWithStdin(t, dbFile, "piped description\n", "--as", "alice", "add", "Root", "-F", "-")
	if err != nil {
		t.Fatalf("add -F -: %v", err)
	}
	id := strings.TrimSpace(strings.Split(strings.TrimSpace(stdout), "\n")[0])

	db := openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Description != "piped description" {
		t.Errorf("description: got %q, want %q", task.Description, "piped description")
	}
}

// e3g: -F with --desc exits non-zero.
func TestAdd_DashF_WithDesc_Errors(t *testing.T) {
	dbFile := setupCLI(t)

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "add", "Root", "-d", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with --desc")
	}
	assertConflictError(t, err)
}

// --- edit ---------------------------------------------------------------

// esE: `job edit <id> -F desc.md` replaces the description.
func TestEdit_DashF_ReplacesDescription(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Task", "old description")
	db.Close()

	contents := "new description\nfrom a file"
	path := writeBody(t, contents)

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "-F", path); err != nil {
		t.Fatalf("edit -F: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.Description != contents {
		t.Errorf("description: got %q, want %q", task.Description, contents)
	}
}

// esE: `--desc ""` still clears the description.
func TestEdit_EmptyDesc_StillClears(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Task", "old description")
	db.Close()

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "--desc", ""); err != nil {
		t.Fatalf("edit --desc \"\": %v", err)
	}
	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.Description != "" {
		t.Errorf("description should be cleared, got %q", task.Description)
	}
}

func TestEdit_DashF_DashReadsStdin(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAddDesc(t, db, "", "Task", "old")
	db.Close()

	if _, _, err := runCLIWithStdin(t, dbFile, "piped description\n", "--as", "alice", "edit", id, "-F", "-"); err != nil {
		t.Fatalf("edit -F -: %v", err)
	}
	db2 := openTestDB(t, dbFile)
	task := job.MustGet(t, db2, id)
	if task.Description != "piped description" {
		t.Errorf("description: got %q, want %q", task.Description, "piped description")
	}
}

// e3g: -F with --desc exits non-zero on edit too.
func TestEdit_DashF_WithDesc_Errors(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "from file")
	_, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "-d", "inline", "-F", path)
	if err == nil {
		t.Fatal("expected an error for -F together with --desc")
	}
	assertConflictError(t, err)
}

// `edit -F` alone satisfies the "at least one of --title/--desc/..." gate.
func TestEdit_DashF_AloneIsEnough(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Task")
	db.Close()

	path := writeBody(t, "just a description")
	if _, _, err := runCLI(t, dbFile, "--as", "alice", "edit", id, "-F", path); err != nil {
		t.Fatalf("edit -F alone should be accepted: %v", err)
	}
}

// add/edit keep --desc strictly literal: it never gains the @path or `-`
// forms, so a description that legitimately starts with @ survives.
func TestAdd_Desc_StaysLiteral(t *testing.T) {
	dbFile := setupCLI(t)

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "add", "Root", "-d", "@mentions are literal here")
	if err != nil {
		t.Fatalf("add -d: %v", err)
	}
	id := strings.TrimSpace(strings.Split(strings.TrimSpace(stdout), "\n")[0])

	db := openTestDB(t, dbFile)
	task := job.MustGet(t, db, id)
	if task.Description != "@mentions are literal here" {
		t.Errorf("description: got %q, want the literal string", task.Description)
	}
}

// --- help strings -------------------------------------------------------

// OgR / 5XI: every body-taking verb advertises -F in its own help.
func TestBodyVerbs_AdvertiseDashF(t *testing.T) {
	for _, name := range []string{"note", "done", "claim", "release", "unclaim", "cancel", "add", "edit"} {
		resetFlags()
		root := newRootCmd()
		var found *string
		for _, c := range root.Commands() {
			if c.Name() == name {
				f := c.Flags().Lookup("file")
				if f == nil {
					t.Errorf("%s: no --file flag registered", name)
					break
				}
				if f.Shorthand != "F" {
					t.Errorf("%s: --file shorthand is %q, want \"F\"", name, f.Shorthand)
				}
				u := f.Usage
				found = &u
			}
		}
		if found == nil {
			t.Errorf("%s: command not found or flag missing", name)
		}
	}
	resetFlags()
}
