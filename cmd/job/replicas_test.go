package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// l5SEtr — the CLI half of replica naming: `job init --replica-name` parks
// the name, the first write mints the replica event carrying it, `job
// replicas` lists what the store knows, and `job replica rename` changes it.

// replicaCLI runs one `job` invocation with dir as the working directory.
func replicaCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	t.Setenv("JOBS_DB", "")
	resetFlags()
	t.Cleanup(resetFlags)

	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestInitReplicaName_IsTheLabelOnTheFirstEvent(t *testing.T) {
	dir := t.TempDir()
	if out, err := replicaCLI(t, dir, "init", "--as", "ben", "--replica-name", "ben-mbp"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := replicaCLI(t, dir, "add", "First task", "--as", "ben"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	out, err := replicaCLI(t, dir, "replicas", "--format", "json")
	if err != nil {
		t.Fatalf("replicas: %v\n%s", err, out)
	}
	var got []struct {
		Rep     string `json:"rep"`
		Label   string `json:"label"`
		Host    string `json:"host"`
		User    string `json:"user"`
		Path    string `json:"path"`
		Events  int    `json:"events"`
		IsLocal bool   `json:"is_local"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("parse %q: %v", out, err)
	}
	if len(got) != 1 {
		t.Fatalf("job replicas listed %d replicas, want 1:\n%s", len(got), out)
	}
	if got[0].Label != "ben-mbp" {
		t.Errorf("label = %q, want the name given at init", got[0].Label)
	}
	if got[0].Host == "" || got[0].User == "" || got[0].Path == "" {
		t.Errorf("the listing is missing host/user/path: %+v", got[0])
	}
	if !got[0].IsLocal {
		t.Error("the only replica is not marked as this checkout")
	}

	// `job status` names it beside the id.
	status, err := replicaCLI(t, dir, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, status)
	}
	if !strings.Contains(status, `"ben-mbp"`) {
		t.Errorf("status does not name the replica:\n%s", status)
	}
}

func TestReplicaRename_ReportsTheOldAndNewName(t *testing.T) {
	dir := t.TempDir()
	if out, err := replicaCLI(t, dir, "init", "--as", "ben", "--replica-name", "before"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := replicaCLI(t, dir, "add", "First task", "--as", "ben"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	out, err := replicaCLI(t, dir, "replica", "rename", "after", "--as", "ben")
	if err != nil {
		t.Fatalf("rename: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"before"`) || !strings.Contains(out, `"after"`) {
		t.Errorf("rename output should name both the old and the new label:\n%s", out)
	}

	listing, err := replicaCLI(t, dir, "replicas")
	if err != nil {
		t.Fatalf("replicas: %v\n%s", err, listing)
	}
	if !strings.Contains(listing, `"after"`) {
		t.Errorf("listing does not show the new label:\n%s", listing)
	}
	if !strings.Contains(listing, "this checkout") {
		t.Errorf("listing does not mark this checkout:\n%s", listing)
	}
}
