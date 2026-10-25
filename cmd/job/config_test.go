package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job config` reads and writes the committed repo config,
// .jobs/config.json. The claim-duration setting it holds is the default for
// every verb that claims without a duration.

func TestConfig_GetUnsetReportsTheDefault(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "config", "claim-duration")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if want := "claim-duration: 30m (default)\n"; stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestConfig_SetThenGet(t *testing.T) {
	dbFile := setupCLI(t)
	path := job.RepoConfigPath(dbFile)

	stdout, _, err := runCLI(t, dbFile, "config", "claim-duration", "2h")
	if err != nil {
		t.Fatalf("config set: %v", err)
	}
	if want := "claim-duration: 2h (set in " + path + ")\n"; stdout != want {
		t.Errorf("set: got %q, want %q", stdout, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("set should write %s: %v", path, err)
	}

	stdout, _, err = runCLI(t, dbFile, "config", "claim-duration")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if want := "claim-duration: 2h (set in " + path + ")\n"; stdout != want {
		t.Errorf("get: got %q, want %q", stdout, want)
	}
}

func TestConfig_Unset(t *testing.T) {
	dbFile := setupCLI(t)
	if _, _, err := runCLI(t, dbFile, "config", "claim-duration", "2h"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCLI(t, dbFile, "config", "claim-duration", "--unset")
	if err != nil {
		t.Fatalf("config --unset: %v", err)
	}
	if want := "claim-duration: 30m (default)\n"; stdout != want {
		t.Errorf("unset: got %q, want %q", stdout, want)
	}
}

func TestConfig_UnsetWithAValueIsAnError(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "config", "claim-duration", "2h", "--unset")
	if err == nil || !strings.Contains(err.Error(), "--unset") {
		t.Errorf("--unset with a value should fail naming --unset, got %v", err)
	}
}

func TestConfig_NoArgsListsEverySetting(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if want := "claim-duration: 30m (default)\n"; stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestConfig_RejectsAnInvalidDuration(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "config", "claim-duration", "soon")
	if err == nil || !strings.Contains(err.Error(), job.RepoConfigPath(dbFile)) {
		t.Errorf("want an error naming the file, got %v", err)
	}
}

func TestConfig_RejectsAnUnknownKey(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "config", "claim-length", "2h")
	if err == nil || !strings.Contains(err.Error(), "claim-duration") {
		t.Errorf("want an error listing the known settings, got %v", err)
	}
}

func TestConfig_WithNoStoreIsAnError(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "nothing.db")
	t.Cleanup(resetFlags)
	_, _, err := runCLI(t, dbFile, "config", "claim-duration", "2h")
	if err == nil || !strings.Contains(err.Error(), "no job database found") {
		t.Errorf("config with no store should fail rather than create one, got %v", err)
	}
	if _, err := os.Stat(job.RepoConfigPath(dbFile)); err == nil {
		t.Error("config with no store must not write a config file")
	}
}

// setClaimDurationCLI sets the repo's claim-duration through the verb.
func setClaimDurationCLI(t *testing.T, dbFile, d string) {
	t.Helper()
	if _, _, err := runCLI(t, dbFile, "config", "claim-duration", d); err != nil {
		t.Fatalf("config claim-duration %s: %v", d, err)
	}
}

func TestClaim_AckUsesTheRepoDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Slow build")
	db.Close()
	setClaimDurationCLI(t, dbFile, "2h")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if want := "Claimed: " + id + " \"Slow build\" (expires in 2h) as=alice\n"; firstLine(stdout) != want {
		t.Errorf("got %q, want %q", firstLine(stdout), want)
	}
}

func TestClaim_ExplicitDurationBeatsTheRepoDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Quick fix")
	db.Close()
	setClaimDurationCLI(t, dbFile, "2h")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", id, "10m")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if want := "Claimed: " + id + " \"Quick fix\" (expires in 10m) as=alice\n"; firstLine(stdout) != want {
		t.Errorf("got %q, want %q", firstLine(stdout), want)
	}
}

func TestClaimNext_AckUsesTheRepoDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Next up")
	db.Close()
	setClaimDurationCLI(t, dbFile, "2h")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "claim", "--next", "-q")
	if err != nil {
		t.Fatalf("claim --next: %v", err)
	}
	if want := "Claimed: " + id + " \"Next up\" (expires in 2h) as=alice\n"; firstLine(stdout) != want {
		t.Errorf("got %q, want %q", firstLine(stdout), want)
	}
}

func TestDoneClaimNext_AckUsesTheRepoDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	root := job.MustAdd(t, db, "", "Root")
	first := job.MustAdd(t, db, root, "First")
	second := job.MustAdd(t, db, root, "Second")
	db.Close()
	setClaimDurationCLI(t, dbFile, "2h")

	if _, _, err := runCLI(t, dbFile, "--as", "alice", "claim", first); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "done", first, "--claim-next", "-q")
	if err != nil {
		t.Fatalf("done --claim-next: %v", err)
	}
	if want := "Claimed: " + second + " \"Second\" (expires in 2h) as=alice"; !strings.Contains(stdout, want) {
		t.Errorf("want %q in:\n%s", want, stdout)
	}
}

func TestReopen_AutoClaimAckUsesTheRepoDefault(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	id := job.MustAdd(t, db, "", "Again")
	job.MustDone(t, db, id)
	db.Close()
	setClaimDurationCLI(t, dbFile, "2h")

	stdout, _, err := runCLI(t, dbFile, "--as", "alice", "reopen", id)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if want := "claimed by alice (expires in 2h)"; !strings.Contains(stdout, want) {
		t.Errorf("want %q in:\n%s", want, stdout)
	}
}
