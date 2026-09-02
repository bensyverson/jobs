package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitignoreCLI drives `job gitignore` with the cwd as the database's
// directory — the verb resolves the directory the same way `init` resolves
// where to create the database.
func gitignoreCLI(t *testing.T, dir string, args ...string) (stdout string, err error) {
	t.Helper()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs(append([]string{"gitignore"}, args...))
	err = root.Execute()
	return outBuf.String(), err
}

func TestGitignore_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	out, err := gitignoreCLI(t, dir)
	if err != nil {
		t.Fatalf("gitignore: %v\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, name := range []string{"\n.jobs.db*\n", ".jobs/local.json"} {
		if !strings.Contains(content, name) {
			t.Errorf(".gitignore missing %q:\n%s", name, content)
		}
	}
	if !strings.Contains(out, "Wrote 2 entries to .gitignore") {
		t.Errorf("missing success output:\n%s", out)
	}
}

// The verb is about the directory, not the database: it works before init,
// and creates no database of its own.
func TestGitignore_WorksWithNoDatabase(t *testing.T) {
	dir := t.TempDir()
	out, err := gitignoreCLI(t, dir)
	if err != nil {
		t.Fatalf("gitignore with no database: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Errorf("gitignore did not write .gitignore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".jobs.db")); err == nil {
		t.Errorf("gitignore should not create a database")
	}
}

func TestGitignore_AppendsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := "node_modules/\n.env\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	if _, err := gitignoreCLI(t, dir); err != nil {
		t.Fatalf("gitignore: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.HasPrefix(content, existing) {
		t.Errorf("original content clobbered:\n%s", content)
	}
	if !strings.Contains(content, ".jobs.db*") {
		t.Errorf("missing appended entry:\n%s", content)
	}
	if !strings.Contains(content, "# job\n") {
		t.Errorf("missing section header:\n%s", content)
	}
}

func TestGitignore_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitignoreCLI(t, dir); err != nil {
		t.Fatalf("gitignore 1: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))

	out, err := gitignoreCLI(t, dir)
	if err != nil {
		t.Fatalf("gitignore 2: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(first) != string(second) {
		t.Errorf("gitignore changed on second run:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// humanJoin's Oxford comma, shared with every other list the CLI prints.
	want := ".gitignore already includes .jobs.db* and .jobs/local.json"
	if !strings.Contains(out, want) {
		t.Errorf("second run output:\n  got:  %s\n  want to contain: %s", out, want)
	}
}

func TestGitignore_PartialPresent(t *testing.T) {
	dir := t.TempDir()
	existing := ".jobs.db*\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	out, err := gitignoreCLI(t, dir)
	if err != nil {
		t.Fatalf("gitignore: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	content := string(data)
	if !strings.Contains(content, ".jobs/local.json") {
		t.Errorf("missing local.json append:\n%s", content)
	}
	if strings.Count(content, ".jobs.db*") != 1 {
		t.Errorf("duplicated cache entry:\n%s", content)
	}
	if !strings.Contains(out, "Wrote 1 entry") {
		t.Errorf("missing wrote message:\n%s", out)
	}
}

// TestGitignore_GitActuallyIgnoresTheEntries drives real `git` on a real
// repo: the text assertions above missed, once, that a trailing inline
// comment makes git treat the whole line as one literal (never-matching)
// pattern. Skips if git isn't on PATH.
func TestGitignore_GitActuallyIgnoresTheEntries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	gitInit := exec.Command("git", "init", "-q")
	gitInit.Dir = dir
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Skipf("git init failed, skipping: %v\n%s", err, out)
	}

	if _, err := gitignoreCLI(t, dir); err != nil {
		t.Fatalf("gitignore: %v", err)
	}

	// Everything the one cache pattern is meant to cover, plus the machine's
	// own local state.
	for _, name := range []string{
		".jobs.db", ".jobs.db-shm", ".jobs.db-wal",
		".jobs.db.lock", ".jobs.db.pre-adopt", ".jobs/local.json",
	} {
		check := exec.Command("git", "check-ignore", "-q", name)
		check.Dir = dir
		if err := check.Run(); err != nil {
			t.Errorf("git check-ignore -q %s: not ignored (%v)", name, err)
		}
	}
}

// The flag was replaced by the verb; passing it is a cobra unknown-flag
// error, not a silent no-op.
func TestInit_GitignoreFlag_IsUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init", "--as", "claude", "--gitignore"})
	if err := root.Execute(); err == nil {
		t.Fatalf("init --gitignore should be an unknown-flag error:\n%s", outBuf.String())
	}
}

// --dry-run previews. `job merge` already spells it this way, so the flag and
// its -n alias are the same two on both verbs.
func TestGitignore_DryRun_WritesNothing(t *testing.T) {
	for _, flag := range []string{"--dry-run", "-n"} {
		t.Run(flag, func(t *testing.T) {
			dir := t.TempDir()
			out, err := gitignoreCLI(t, dir, flag)
			if err != nil {
				t.Fatalf("gitignore %s: %v\n%s", flag, err, out)
			}
			if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
				t.Errorf("gitignore %s created a .gitignore", flag)
			}
			if !strings.Contains(out, "Would write 2 entries to .gitignore") {
				t.Errorf("missing preview output:\n%s", out)
			}
			for _, pattern := range []string{".jobs.db*", ".jobs/local.json"} {
				if !strings.Contains(out, pattern) {
					t.Errorf("preview should name %q:\n%s", pattern, out)
				}
			}
		})
	}
}

// A preview run leaves an existing file byte-for-byte alone, and says the
// same "nothing to do" as the real verb when nothing is missing.
func TestGitignore_DryRun_LeavesAnExistingFileAlone(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitignoreCLI(t, dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".gitignore")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	out, err := gitignoreCLI(t, dir, "--dry-run")
	if err != nil {
		t.Fatalf("gitignore --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, ".gitignore already includes") {
		t.Errorf("expected the already-present report:\n%s", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf(".gitignore changed under --dry-run:\n%s", after)
	}
}

// The preview has to be the truth: what it says it would write is what the
// real run then writes.
func TestGitignore_DryRun_PreviewMatchesTheRealRun(t *testing.T) {
	dry := t.TempDir()
	preview, err := gitignoreCLI(t, dry, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	wet := t.TempDir()
	real, err := gitignoreCLI(t, wet)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimPrefix(preview, "Would write") != strings.TrimPrefix(real, "Wrote") {
		t.Errorf("preview and real run disagree:\npreview: %s\nreal:    %s", preview, real)
	}
}
