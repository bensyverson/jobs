package job

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs git in dir and fails the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.email=t@example.com", "-c", "user.name=T",
		"-c", "commit.gpgsign=false",
	}, args...)
	cmd := gitCommand(dir, full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// realPath resolves symlinks (macOS temp dirs live behind /var -> /private/var)
// so a path can be compared with what git reports.
func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// gitRepoWithWorktree makes a repository and a linked worktree of it, and
// returns the main checkout and the worktree, both with symlinks resolved.
func gitRepoWithWorktree(t *testing.T) (main, linked string) {
	t.Helper()
	root := realPath(t, t.TempDir())
	main = filepath.Join(root, "main")
	linked = filepath.Join(root, "linked")
	gitIn(t, root, "init", "-q", main)
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	gitIn(t, main, "worktree", "add", "-q", "-b", "side", linked)
	return main, linked
}

func TestWorktreeReplica_MintingInALinkedWorktreeWarnsOnceNamingTheMainStore(t *testing.T) {
	main, linked := gitRepoWithWorktree(t)
	notices := captureNotices(t)

	db, err := CreateDB(filepath.Join(linked, ".jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	MustAdd(t, db, "", "First")
	MustAdd(t, db, "", "Second")

	got := notices.String()
	if n := strings.Count(got, "own replica"); n != 1 {
		t.Fatalf("want exactly one worktree warning, got %d:\n%s", n, got)
	}
	want := filepath.Join(main, ".jobs.db")
	if !strings.Contains(got, "--db "+want) {
		t.Errorf("warning should name %s:\n%s", want, got)
	}
	if !strings.Contains(got, "invisible") {
		t.Errorf("warning should say the writes are invisible to the main store:\n%s", got)
	}
}

func TestWorktreeReplica_LaterOpenOfTheSameReplicaStaysQuiet(t *testing.T) {
	_, linked := gitRepoWithWorktree(t)
	path := filepath.Join(linked, ".jobs.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	MustAdd(t, db, "", "First")
	db.Close()

	notices := captureNotices(t)
	again, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	MustAdd(t, again, "", "Second")

	if strings.Contains(notices.String(), "own replica") {
		t.Errorf("a replica that already exists must not warn:\n%s", notices.String())
	}
}

func TestWorktreeReplica_MainCheckoutStaysQuiet(t *testing.T) {
	main, _ := gitRepoWithWorktree(t)
	notices := captureNotices(t)

	db, err := CreateDB(filepath.Join(main, ".jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	MustAdd(t, db, "", "First")

	if strings.Contains(notices.String(), "own replica") {
		t.Errorf("the main checkout must not warn:\n%s", notices.String())
	}
}

func TestWorktreeReplica_OutsideGitStaysQuiet(t *testing.T) {
	notices := captureNotices(t)

	db, err := CreateDB(filepath.Join(t.TempDir(), ".jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	MustAdd(t, db, "", "First")

	if strings.Contains(notices.String(), "own replica") {
		t.Errorf("a store outside git must not warn:\n%s", notices.String())
	}
}

func TestMainStoreOfWorktree_ReportsTheMainCheckoutsStore(t *testing.T) {
	main, linked := gitRepoWithWorktree(t)

	if err := os.MkdirAll(filepath.Join(linked, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := mainStoreOfWorktree(filepath.Join(linked, "sub", ".jobs.db"))
	if !ok {
		t.Fatal("a store inside a linked worktree should be recognized")
	}
	if want := filepath.Join(main, "sub", ".jobs.db"); got != want {
		t.Errorf("main store = %q, want %q", got, want)
	}

	if _, ok := mainStoreOfWorktree(filepath.Join(main, ".jobs.db")); ok {
		t.Error("the main checkout is not a linked worktree")
	}
	if _, ok := mainStoreOfWorktree(filepath.Join(t.TempDir(), ".jobs.db")); ok {
		t.Error("a path outside git is not a linked worktree")
	}
	// A store that lives outside the worktree is not the worktree's to move.
	if _, ok := mainStoreOfWorktree(filepath.Join(main, "..", ".jobs.db")); ok {
		t.Error("a store outside any checkout should not be recognized")
	}
}

// A git hook exports GIT_DIR and GIT_INDEX_FILE for its own repository, and
// any job run from it (or any test run by it) inherits them. The worktree
// check must still ask about the store's directory, not the hook's repo.
func TestMainStoreOfWorktree_IgnoresAnInheritedGitDir(t *testing.T) {
	main, linked := gitRepoWithWorktree(t)
	other, _ := gitRepoWithWorktree(t)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	got, ok := mainStoreOfWorktree(filepath.Join(linked, ".jobs.db"))
	if !ok {
		t.Fatal("a store inside a linked worktree should be recognized despite an inherited GIT_DIR")
	}
	if want := filepath.Join(main, ".jobs.db"); got != want {
		t.Errorf("main store = %q, want %q", got, want)
	}
}
