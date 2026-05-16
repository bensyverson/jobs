package main

import (
	"encoding/json"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job status --format=json` emits a machine-parsable shape mirroring the
// human preamble: identity, counts, last_activity_unix, roots, next, stale.
// Today the flag is unknown — these tests pin the JSON contract so script-
// driving agents have something stable to grep against.

func TestStatus_JSON_TopLevelShape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root task")
	job.MustAdd(t, db, root, "Child")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON:\n%s\nerr: %v", stdout, err)
	}

	for _, key := range []string{"identity", "counts", "last_activity_unix", "roots", "next", "stale"} {
		if _, ok := got[key]; !ok {
			t.Errorf("expected top-level key %q in JSON output; got keys: %v", key, mapKeys(got))
		}
	}
}

// Identity surfaces the default actor and the strict-mode flag — same two
// pieces the human form prints on its second line.
func TestStatus_JSON_IdentityFields(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Root")
	db.Close()

	// Set a default identity so the identity block has something to report.
	if _, _, err := runCLI(t, dbFile, "--as", "ben", "identity", "set", "ben"); err != nil {
		t.Fatalf("identity set: %v", err)
	}

	stdout, _, err := runCLI(t, dbFile, "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	ident, ok := got["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity should be an object; got %T", got["identity"])
	}
	if ident["default"] != "ben" {
		t.Errorf("identity.default: got %v, want %q", ident["default"], "ben")
	}
	if _, ok := ident["strict"]; !ok {
		t.Errorf("identity.strict missing; got %v", ident)
	}
}

// Counts split open / claimed / done / canceled across all non-deleted
// tasks, same buckets as the human preamble.
func TestStatus_JSON_CountsBuckets(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	open := job.MustAdd(t, db, "", "Open task")
	_ = open
	closed := job.MustAdd(t, db, "", "Done task")
	canceled := job.MustAdd(t, db, "", "Canceled task")
	claimed := job.MustAdd(t, db, "", "Claimed task")
	if err := job.RunClaim(db, claimed, "1h", "", "alice", false); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, _, err := job.RunDone(db, []string{closed}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	if _, _, _, err := job.RunCancel(db, []string{canceled}, "test reason", false, false, true, "alice"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	counts, ok := got["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts should be an object; got %T", got["counts"])
	}
	// `Open task` is open (1). The claimed-but-not-yet-done task adds to
	// claimed (1), not open. Done is 1, canceled is 1.
	if counts["open"].(float64) != 1 {
		t.Errorf("counts.open: got %v, want 1", counts["open"])
	}
	if counts["claimed"].(float64) != 1 {
		t.Errorf("counts.claimed: got %v, want 1", counts["claimed"])
	}
	if counts["done"].(float64) != 1 {
		t.Errorf("counts.done: got %v, want 1", counts["done"])
	}
	if counts["canceled"].(float64) != 1 {
		t.Errorf("counts.canceled: got %v, want 1", counts["canceled"])
	}
}

// roots: one entry per top-level task surfaced in the human rollup.
// Fully-closed roots are hidden in the human form; the JSON form follows
// suit so agents and humans see the same shape.
func TestStatus_JSON_RootsMirrorRollup(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Project A")
	job.MustAdd(t, db, a, "Child A1")
	b := job.MustAdd(t, db, "", "Project B")
	// Close project B fully so it should be filtered from roots, mirroring
	// the human rollup's done-root suppression.
	if _, _, err := job.RunDone(db, []string{b}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	roots, ok := got["roots"].([]any)
	if !ok {
		t.Fatalf("roots should be an array; got %T", got["roots"])
	}
	if len(roots) != 1 {
		t.Fatalf("expected 1 open root (closed B filtered), got %d: %v", len(roots), roots)
	}
	first := roots[0].(map[string]any)
	if first["short_id"] != a {
		t.Errorf("roots[0].short_id: got %v, want %q", first["short_id"], a)
	}
	if first["title"] != "Project A" {
		t.Errorf("roots[0].title: got %v, want %q", first["title"], "Project A")
	}
	for _, key := range []string{"open", "done"} {
		if _, ok := first[key]; !ok {
			t.Errorf("roots[0] missing %q; got %v", key, first)
		}
	}
}

// next: the same leaf the human form names. Omitted (or null) when no
// claimable leaf exists.
func TestStatus_JSON_NextNamesClaimableLeaf(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Project")
	leaf := job.MustAdd(t, db, a, "Leaf to do")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	next, ok := got["next"].(map[string]any)
	if !ok {
		t.Fatalf("next should be an object naming the next claimable leaf; got %T %v", got["next"], got["next"])
	}
	if next["short_id"] != leaf {
		t.Errorf("next.short_id: got %v, want %q", next["short_id"], leaf)
	}
	if next["title"] != "Leaf to do" {
		t.Errorf("next.title: got %v, want %q", next["title"], "Leaf to do")
	}
}

func TestStatus_JSON_NextNullWhenNoClaimableLeaf(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	// All-closed DB: no claimable leaves.
	id := job.MustAdd(t, db, "", "Root")
	if _, _, err := job.RunDone(db, []string{id}, false, "", nil, "alice", false, ""); err != nil {
		t.Fatalf("done: %v", err)
	}
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got["next"] != nil {
		t.Errorf("next should be null when no claimable leaf exists; got %v", got["next"])
	}
}

// stale: each expired-but-not-yet-swept claim surfaces with id, title,
// claimed_by, and seconds_stale. Empty array (not null) when nothing is
// stale, so agents can iterate without a nil check.
func TestStatus_JSON_StaleArrayIsAlwaysPresent(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Root")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=json")
	if err != nil {
		t.Fatalf("status --format=json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	stale, ok := got["stale"].([]any)
	if !ok {
		t.Fatalf("stale should be an array (possibly empty); got %T %v", got["stale"], got["stale"])
	}
	if len(stale) != 0 {
		t.Errorf("stale should be empty for a fresh DB; got %v", stale)
	}
}

// Subtree scope: `status <id> --format=json` swaps the preamble for a
// target+children shape, mirroring the human form which drops the
// preamble when scoped.
func TestStatus_JSON_SubtreeShape(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	a := job.MustAdd(t, db, "", "Project A")
	job.MustAdd(t, db, a, "Child A1")
	db.Close()

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "status", a, "--format=json")
	if err != nil {
		t.Fatalf("status %s --format=json: %v", a, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	// Subtree scope: target object replaces preamble; counts/identity not present.
	if _, ok := got["target"]; !ok {
		t.Errorf("subtree scope should include `target`; got keys %v", mapKeys(got))
	}
	if _, ok := got["counts"]; ok {
		t.Errorf("subtree scope should not include `counts` preamble; got keys %v", mapKeys(got))
	}
	target, _ := got["target"].(map[string]any)
	if target["short_id"] != a {
		t.Errorf("target.short_id: got %v, want %q", target["short_id"], a)
	}
}

// Unknown --format value is rejected with a directive listing the valid
// options, so a typo doesn't silently fall back to md.
func TestStatus_FormatUnknown_Rejected(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	job.MustAdd(t, db, "", "Root")
	db.Close()

	_, _, err := runCLI(t, dbFile, "--as", "alice", "status", "--format=xml")
	if err == nil {
		t.Fatal("expected unknown --format value to be rejected")
	}
	if !strings.Contains(err.Error(), "json") || !strings.Contains(err.Error(), "md") {
		t.Errorf("error should name valid formats (md, json); got: %v", err)
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
