package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two replicas, driven through the built binary.
//
// The library-level end-to-end suite is internal/job/replica_e2e_test.go; this
// is the happy path the design promises a human, exercised the way a human
// would: `job init` here, `git pull` there, `job ls` and `job done` on the
// other machine, and back. Nothing below reaches into the library — a bug in
// how the CLI resolves a store, mints an identity, or reports its state shows
// up here and nowhere else.

// pullLog copies every replica log file from one checkout into another, which
// is all a `git pull` of a tracked .jobs/log/ is. Files are append-only and
// written by exactly one replica each, so a whole-file copy is the merge.
func pullLog(t *testing.T, fromDir, toDir string) {
	t.Helper()
	src := filepath.Join(fromDir, ".jobs", "log")
	dst := filepath.Join(toDir, ".jobs", "log")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
}

func TestTwoReplicasThroughTheBuiltBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := buildJobBinary(t)
	dirA, dirB := t.TempDir(), t.TempDir()

	// One machine starts the project.
	initOut := runBinary(t, bin, dirA, "init", "--as", "ben")
	if !strings.Contains(initOut, "Default identity: ben") {
		t.Fatalf("init did not record the identity:\n%s", initOut)
	}
	shipID := strings.TrimSpace(runBinary(t, bin, dirA, "add", "Ship the store", "--id-only"))
	docsID := strings.TrimSpace(runBinary(t, bin, dirA, "add", "--parent", shipID, "Write the docs", "--id-only"))

	// The second machine clones: .jobs/log and nothing else — no cache, no
	// local.json, no identity.
	pullLog(t, dirA, dirB)
	if _, err := os.Stat(filepath.Join(dirB, ".jobs.db")); err == nil {
		t.Fatalf("the clone should carry no cache")
	}

	// The first command there builds the cache from the log alone.
	listB := runBinary(t, bin, dirB, "ls", "--all")
	for _, want := range []string{shipID, "Ship the store", docsID, "Write the docs"} {
		if !strings.Contains(listB, want) {
			t.Fatalf("the clone's first command did not build %q:\n%s", want, listB)
		}
	}
	if _, err := os.Stat(filepath.Join(dirB, ".jobs.db")); err != nil {
		t.Fatalf("the first command left no cache: %v", err)
	}

	// Work happens on the second machine. It has no default identity of its
	// own, because identity is per-machine and lives outside the log.
	runBinary(t, bin, dirB, "done", docsID, "--as", "sam")

	// And rides back through git.
	pullLog(t, dirB, dirA)
	afterA := runBinary(t, bin, dirA, "ls", "--all")
	if !strings.Contains(afterA, "[x]") || !strings.Contains(afterA, docsID) {
		t.Fatalf("the close made on the other machine did not arrive:\n%s", afterA)
	}
	showA := runBinary(t, bin, dirA, "show", docsID)
	if !strings.Contains(showA, "done") {
		t.Fatalf("%s is not done on the first machine:\n%s", docsID, showA)
	}
	logA := runBinary(t, bin, dirA, "log", docsID)
	if !strings.Contains(logA, "sam") || !strings.Contains(logA, "done") {
		t.Fatalf("the closing actor did not travel with the event:\n%s", logA)
	}

	// Both machines now report the same store: two replicas, two log files.
	for _, dir := range []string{dirA, dirB} {
		status := runBinary(t, bin, dir, "status")
		if !strings.Contains(status, "Store: replica ") {
			t.Fatalf("status in %s carries no store line:\n%s", dir, status)
		}
		if !strings.Contains(status, "2 log files") {
			t.Fatalf("status in %s does not see both replicas' logs:\n%s", dir, status)
		}
	}
	// A second status is the hot path: nothing to replay.
	if s := runBinary(t, bin, dirA, "status"); !strings.Contains(s, "cache in sync") {
		t.Fatalf("the second command still rebuilt:\n%s", s)
	}
}
