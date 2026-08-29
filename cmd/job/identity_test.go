package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/bensyverson/jobs/internal/job"
)

// initCLI drives a fresh `init` through the cobra layer in a temp dir.
// Returns the db path and captured stdout, failing the test if init errors.
func initCLI(t *testing.T, extra ...string) (dbFile, stdout string) {
	t.Helper()
	dbFile, stdout, err := tryInitCLI(t, extra...)
	if err != nil {
		t.Fatalf("init %v: %v", extra, err)
	}
	return dbFile, stdout
}

// tryInitCLI is initCLI for the cases where the error is the subject.
func tryInitCLI(t *testing.T, extra ...string) (dbFile, stdout string, err error) {
	t.Helper()
	dir := t.TempDir()
	dbFile = filepath.Join(dir, "test.db")
	resetFlags()
	t.Cleanup(resetFlags)
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	args := append([]string{"--db", dbFile, "init"}, extra...)
	root.SetArgs(args)
	err = root.Execute()
	return dbFile, outBuf.String() + errBuf.String(), err
}

// --- Default identity (P3) ---

// init no longer guesses: $USER is whoever launched the session, which for
// the dominant caller — an agent — is the wrong name, and wrong silently.
func TestInit_NoFlags_ErrorsAndCreatesNothing(t *testing.T) {
	t.Setenv("USER", "envuser")
	dbFile, _, err := tryInitCLI(t)
	if err == nil {
		t.Fatal("init with neither --as nor --strict should fail")
	}
	if err.Error() != wantInitIdentityRequired {
		t.Errorf("error:\n  got:  %q\n  want: %q", err.Error(), wantInitIdentityRequired)
	}
	if _, statErr := os.Stat(dbFile); statErr == nil {
		t.Errorf("init should create no database when it refuses: %s exists", dbFile)
	}
}

// The check runs before CreateDB, so a refused init leaves nothing behind
// to --force over — including when the flags are otherwise valid.
func TestInit_NoIdentity_DoesNotCreateDatabaseEvenWithForce(t *testing.T) {
	dbFile, _, err := tryInitCLI(t, "--force")
	if err == nil {
		t.Fatal("init --force without an identity should fail")
	}
	if _, statErr := os.Stat(dbFile); statErr == nil {
		t.Errorf("init should create no database when it refuses: %s exists", dbFile)
	}
}

func TestInit_As_SetsDefaultIdentity(t *testing.T) {
	t.Setenv("USER", "envuser")
	dbFile, stdout := initCLI(t, "--as", "claude")

	db := openTestDB(t, dbFile)
	got, err := job.GetDefaultIdentity(db)
	if err != nil {
		t.Fatalf("GetDefaultIdentity: %v", err)
	}
	if got != "claude" {
		t.Errorf("default identity = %q, want claude", got)
	}
	if !strings.Contains(stdout, "Default identity: claude\n") {
		t.Errorf("output should announce 'Default identity: claude':\n%s", stdout)
	}
	// There is one source now, so there is no source to cite.
	if strings.Contains(stdout, "(from") || strings.Contains(stdout, "$USER") {
		t.Errorf("output should not cite a source:\n%s", stdout)
	}
}

func TestInit_DefaultIdentityFlag_IsUnknownFlag(t *testing.T) {
	_, _, err := tryInitCLI(t, "--default-identity", "claude")
	if err == nil {
		t.Fatal("--default-identity should be an unknown-flag error")
	}
}

// --strict wins over --as: it is the stronger statement, and it is what the
// database records.
func TestInit_StrictWithAs_StaysStrict(t *testing.T) {
	dbFile, stdout := initCLI(t, "--as", "claude", "--strict")

	db := openTestDB(t, dbFile)
	if got, _ := job.GetDefaultIdentity(db); got != "" {
		t.Errorf("--strict should leave default unset; got %q", got)
	}
	if strict, _ := job.IsStrict(db); !strict {
		t.Errorf("--strict should enable strict mode")
	}
	if strings.Contains(stdout, "Default identity:") {
		t.Errorf("--strict should not print a default-identity note:\n%s", stdout)
	}
}

// Decision 3: the help must steer an agent to its own name without naming
// any vendor or product.
func TestInit_Help_NamesNoVendorAndPointsAtTheAlternatives(t *testing.T) {
	resetFlags()
	t.Cleanup(resetFlags)
	root := newRootCmd()
	var outBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&outBuf)
	root.SetArgs([]string{"init", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init --help: %v", err)
	}
	out := outBuf.String()
	lower := strings.ToLower(out)
	for _, vendor := range []string{"anthropic", "claude", "openai", "gpt", "gemini", "copilot", "cursor"} {
		if strings.Contains(lower, vendor) {
			t.Errorf("init help names a vendor or product (%q):\n%s", vendor, out)
		}
	}
	for _, want := range []string{"--as", "--strict", "job gitignore", "$USER"} {
		if !strings.Contains(out, want) {
			t.Errorf("init help should mention %q:\n%s", want, out)
		}
	}
}

func TestInit_Strict_LeavesDefaultUnset(t *testing.T) {
	t.Setenv("USER", "envuser")
	dbFile, stdout := initCLI(t, "--strict")

	db := openTestDB(t, dbFile)
	got, _ := job.GetDefaultIdentity(db)
	if got != "" {
		t.Errorf("--strict should leave default unset; got %q", got)
	}
	strict, _ := job.IsStrict(db)
	if !strict {
		t.Errorf("--strict should enable strict mode")
	}
	if strings.Contains(stdout, "Default identity:") {
		t.Errorf("--strict should not print a default-identity note:\n%s", stdout)
	}
}

// --- Write resolution (P3) ---

func TestWrite_NoAs_UsesDefaultIdentity(t *testing.T) {
	dbFile, _ := initCLI(t, "--as", "alice")

	// No --as on the add call.
	stdout, stderr, err := runCLI(t, dbFile, "add", "hello")
	if err != nil {
		t.Fatalf("add without --as: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "Added") && !strings.Contains(stdout, "Created") {
		// Different ack shapes — at minimum, no error and the task exists.
	}

	// Verify the task was attributed to alice via the created event.
	db := openTestDB(t, dbFile)
	events, err := job.RunLog(db, "", nil)
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	var sawAlice bool
	for _, e := range events {
		if e.Actor == "alice" && e.EventType == "created" {
			sawAlice = true
			break
		}
	}
	if !sawAlice {
		t.Errorf("expected a 'created' event attributed to alice (via default identity)")
	}
}

func TestWrite_NoAs_Strict_Errors(t *testing.T) {
	dbFile, _ := initCLI(t, "--strict")

	_, stderr, err := runCLI(t, dbFile, "add", "hello")
	if err == nil {
		t.Fatalf("expected error: write without --as under strict mode")
	}
	msg := err.Error() + stderr
	if !strings.Contains(msg, "identity required") {
		t.Errorf("error should say 'identity required', got: %s", msg)
	}
}

func TestWrite_AsFlag_OverridesDefaultIdentity(t *testing.T) {
	dbFile, _ := initCLI(t, "--as", "alice")

	_, _, err := runCLI(t, dbFile, "--as", "bob", "add", "hi")
	if err != nil {
		t.Fatalf("add --as bob: %v", err)
	}
	db := openTestDB(t, dbFile)
	events, _ := job.RunLog(db, "", nil)
	for _, e := range events {
		if e.EventType == "created" && e.Actor != "bob" {
			t.Errorf("--as bob should override default (alice); got actor=%q", e.Actor)
		}
	}
}

// --- identity verb (P3) ---

func TestIdentity_Set_RequiresAs(t *testing.T) {
	dbFile, _ := initCLI(t, "--as", "alice")

	// No --as on identity set — bootstrap discipline: still require it.
	_, stderr, err := runCLI(t, dbFile, "identity", "set", "bob")
	if err == nil {
		t.Fatalf("identity set without --as: expected error")
	}
	if !strings.Contains(err.Error()+stderr, "identity required") {
		t.Errorf("error should say 'identity required', got: %s %s", err.Error(), stderr)
	}
}

func TestIdentity_Set_UpdatesDefault(t *testing.T) {
	dbFile, _ := initCLI(t, "--as", "alice")

	_, _, err := runCLI(t, dbFile, "--as", "alice", "identity", "set", "claude")
	if err != nil {
		t.Fatalf("identity set: %v", err)
	}
	db := openTestDB(t, dbFile)
	got, _ := job.GetDefaultIdentity(db)
	if got != "claude" {
		t.Errorf("default identity = %q, want claude", got)
	}
}

func TestIdentity_Strict_On_DisablesDefault(t *testing.T) {
	dbFile, _ := initCLI(t, "--as", "alice")

	// Turn strict on.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "identity", "strict", "on")
	if err != nil {
		t.Fatalf("identity strict on: %v", err)
	}
	// Now writes without --as should error.
	_, _, err = runCLI(t, dbFile, "add", "x")
	if err == nil {
		t.Fatalf("expected error: write without --as under strict mode")
	}
	if !strings.Contains(err.Error(), "identity required") {
		t.Errorf("error should say 'identity required', got: %v", err)
	}
}

func TestIdentity_Strict_OffAfterStrictInit_DefaultRemainsUnset(t *testing.T) {
	t.Setenv("USER", "alice")
	dbFile, _ := initCLI(t, "--strict")

	// Turn strict off — but default was never set, so per the P3 rule it
	// stays unset until explicitly set.
	_, _, err := runCLI(t, dbFile, "--as", "alice", "identity", "strict", "off")
	if err != nil {
		t.Fatalf("identity strict off: %v", err)
	}
	db := openTestDB(t, dbFile)
	got, _ := job.GetDefaultIdentity(db)
	if got != "" {
		t.Errorf("default identity after strict off should remain unset; got %q", got)
	}
	// Writes without --as still error (no default).
	_, _, err = runCLI(t, dbFile, "add", "x")
	if err == nil {
		t.Fatalf("expected error: no default identity")
	}
}

// Preserved expectation: with no config at all (pre-P3 behaviour still works
// for databases that predate the migration), writes without --as error with
// the standard message. A fresh DB under strict mode is the equivalent state.
func TestWrite_Strict_EmptyDB_IdentityRequiredMessageUnchanged(t *testing.T) {
	dbFile := setupCLI(t)
	db := openTestDB(t, dbFile)
	if err := job.SetStrict(db, true); err != nil {
		t.Fatalf("SetStrict: %v", err)
	}
	db.Close()

	_, _, err := runCLI(t, dbFile, "add", "x")
	if err == nil {
		t.Fatalf("expected error under strict with no default")
	}
	if !strings.Contains(err.Error(), "identity required") {
		t.Errorf("message should match legacy wording; got %v", err)
	}
}

// Defensive: don't accidentally leak the USER env var into resolution at
// write time — P3 spec explicitly rules out env-var fallback. If default is
// empty and strict is off, writes without --as still error.
func TestWrite_NoDefault_PermissiveMode_StillErrors(t *testing.T) {
	// Note: setupCLI uses CreateDB which does NOT set a default identity.
	// Strict mode is off by default. Write without --as should still error
	// because nothing is configured.
	dbFile := setupCLI(t)
	t.Setenv("USER", "envuser") // must NOT be used.

	_, _, err := runCLI(t, dbFile, "add", "x")
	if err == nil {
		t.Fatalf("expected error: no default identity configured, $USER should NOT back-fill")
	}
	if !strings.Contains(err.Error(), "identity required") {
		t.Errorf("got %v", err)
	}

	// Verify nothing was written.
	db := openTestDB(t, dbFile)
	got, _ := job.GetDefaultIdentity(db)
	if got != "" {
		t.Errorf("default identity leaked in from $USER: %q", got)
	}
	_ = os.Getenv // silence unused if refactor drops env above
}
