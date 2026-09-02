package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// The store's promise, checked against the real binary: a clone that carries
// only .jobs/log — no cache, no local.json — works on the first command.
//
// This drives the built binary rather than the library because the path it
// exercises is the CLI's own: resolving a database that does not exist yet
// from a store that does.
func TestFreshCloneWorksAgainstTheBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildJobBinary(t)

	// A store written by the library, standing in for the repo somebody
	// committed.
	src := t.TempDir()
	db, err := job.CreateDB(filepath.Join(src, ".jobs.db"))
	if err != nil {
		t.Fatalf("create source store: %v", err)
	}
	added, err := job.RunAdd(db, "", "carried through git", "", "", nil, "ben")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	db.Close()

	// The clone: .jobs/log and nothing else.
	clone := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clone, ".jobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(clone, ".jobs", "log"), os.DirFS(filepath.Join(src, ".jobs", "log"))); err != nil {
		t.Fatalf("copy log: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, ".jobs.db")); err == nil {
		t.Fatalf("the clone should carry no cache")
	}

	out := runBinary(t, bin, clone, "ls", "--all")
	if !strings.Contains(out, added.ShortID) || !strings.Contains(out, "carried through git") {
		t.Fatalf("the first command did not build a working cache:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(clone, ".jobs.db")); err != nil {
		t.Fatalf("the first command left no cache: %v", err)
	}

	status := runBinary(t, bin, clone, "status")
	if !strings.Contains(status, "Store: replica ") {
		t.Fatalf("status carries no store line:\n%s", status)
	}

	// And `job rebuild` on the same clone reports what it replayed.
	rebuilt := runBinary(t, bin, clone, "rebuild")
	if !strings.Contains(rebuilt, "Rebuilt the cache from 1 log file") {
		t.Fatalf("rebuild did not report:\n%s", rebuilt)
	}
	after := runBinary(t, bin, clone, "ls", "--all")
	if !strings.Contains(after, added.ShortID) {
		t.Fatalf("the task did not survive the rebuild:\n%s", after)
	}
}

func buildJobBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "job")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}
	return bin
}

func runBinary(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
	return string(out)
}
