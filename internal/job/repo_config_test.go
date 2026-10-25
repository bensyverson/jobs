package job

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The repo config is .jobs/config.json: committed, shared by every checkout
// and worktree, and kept out of the event log. These tests cover the file,
// the one resolver every claim path asks for its default, and each path
// that consults it.

// cachePathFor is the --db path of a test database, so a test can put a
// config.json beside it.
func cachePathFor(t *testing.T, db dbtx) string {
	t.Helper()
	p, err := CachePathOf(db)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// writeRepoConfigFile hand-writes config.json, the way an operator editing
// it would, bypassing the validating writer.
func writeRepoConfigFile(t *testing.T, cachePath, body string) string {
	t.Helper()
	path := RepoConfigPath(cachePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRepoConfigPath_SitsBesideTheLog(t *testing.T) {
	cache := filepath.Join("/repo", ".jobs.db")
	if got, want := RepoConfigPath(cache), filepath.Join("/repo", ".jobs", "config.json"); got != want {
		t.Errorf("RepoConfigPath: got %q, want %q", got, want)
	}
}

func TestLoadRepoConfig_MissingFileIsTheZeroConfig(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	c, err := LoadRepoConfig(cache)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.ClaimDuration != "" {
		t.Errorf("ClaimDuration: got %q, want empty", c.ClaimDuration)
	}
}

func TestSetRepoConfig_WritesTheFileAndReadsBack(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	if err := SetRepoConfig(cache, ConfigClaimDuration, "2h"); err != nil {
		t.Fatalf("set: %v", err)
	}
	b, err := os.ReadFile(RepoConfigPath(cache))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("file is not JSON: %v\n%s", err, b)
	}
	if raw["claim_duration"] != "2h" {
		t.Errorf("file content: got %s, want claim_duration 2h", b)
	}
	got, err := GetRepoConfig(cache, ConfigClaimDuration)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != "2h" || got.IsDefault {
		t.Errorf("get: got %+v, want 2h, not default", got)
	}
}

func TestGetRepoConfig_UnsetReportsTheDefault(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	got, err := GetRepoConfig(cache, ConfigClaimDuration)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Value != "30m" || !got.IsDefault {
		t.Errorf("get: got %+v, want 30m, default", got)
	}
}

func TestUnsetRepoConfig_RestoresTheDefault(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	if err := SetRepoConfig(cache, ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	if err := UnsetRepoConfig(cache, ConfigClaimDuration); err != nil {
		t.Fatalf("unset: %v", err)
	}
	got, err := GetRepoConfig(cache, ConfigClaimDuration)
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "30m" || !got.IsDefault {
		t.Errorf("after unset: got %+v, want 30m, default", got)
	}
}

func TestSetRepoConfig_RejectsAnInvalidDuration(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	for _, bad := range []string{"soon", "2x", "0m", "-5m", "h"} {
		err := SetRepoConfig(cache, ConfigClaimDuration, bad)
		if err == nil {
			t.Errorf("set %q: want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), RepoConfigPath(cache)) {
			t.Errorf("set %q: error should name the file: %v", bad, err)
		}
	}
	if _, err := os.Stat(RepoConfigPath(cache)); err == nil {
		t.Error("a rejected write must not create the file")
	}
}

func TestLoadRepoConfig_RejectsAnInvalidHandEditedDuration(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	path := writeRepoConfigFile(t, cache, `{"claim_duration": "two hours"}`)
	_, err := LoadRepoConfig(cache)
	if err == nil {
		t.Fatal("want an error for an invalid duration in the file")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "claim_duration") {
		t.Errorf("error should name the file and the key: %v", err)
	}
}

func TestLoadRepoConfig_RejectsMalformedJSON(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	path := writeRepoConfigFile(t, cache, `{"claim_duration": `)
	_, err := LoadRepoConfig(cache)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("want an error naming %s, got %v", path, err)
	}
}

// A misspelled key in a hand-edited file would otherwise be ignored without
// a word, and the operator would wonder why their setting did nothing.
func TestLoadRepoConfig_RejectsAnUnknownKey(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	path := writeRepoConfigFile(t, cache, `{"claim_duraton": "2h"}`)
	_, err := LoadRepoConfig(cache)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "claim_duraton") {
		t.Errorf("want an error naming %s and the key, got %v", path, err)
	}
}

func TestParseConfigKey(t *testing.T) {
	k, err := ParseConfigKey("claim-duration")
	if err != nil || k != ConfigClaimDuration {
		t.Errorf("claim-duration: got %q, %v", k, err)
	}
	_, err = ParseConfigKey("claim-length")
	if err == nil || !strings.Contains(err.Error(), "claim-duration") {
		t.Errorf("unknown key: want an error listing the known keys, got %v", err)
	}
}

func TestResolveClaimDuration(t *testing.T) {
	db := SetupTestDB(t)
	cache := cachePathFor(t, db)

	got, err := ResolveClaimDuration(db, "")
	if err != nil || got != "30m" {
		t.Errorf("no setting: got %q, %v; want 30m", got, err)
	}

	if err := SetRepoConfig(cache, ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	got, err = ResolveClaimDuration(db, "")
	if err != nil || got != "2h" {
		t.Errorf("with setting: got %q, %v; want 2h", got, err)
	}
	got, err = ResolveClaimDuration(db, "45m")
	if err != nil || got != "45m" {
		t.Errorf("explicit: got %q, %v; want 45m", got, err)
	}
}

func TestRunClaim_UsesTheRepoDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	if err := SetRepoConfig(cachePathFor(t, db), ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "")

	task := MustGet(t, db, id)
	if want := base.Add(2 * time.Hour).Unix(); task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 2h)", task.ClaimExpiresAt, want)
	}
	if got := lastClaimedDuration(t, db, id); got != "2h" {
		t.Errorf("claimed event duration: got %q, want 2h", got)
	}
}

func TestRunClaim_ExplicitDurationBeatsTheRepoDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	if err := SetRepoConfig(cachePathFor(t, db), ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "10m")

	task := MustGet(t, db, id)
	if want := base.Add(10 * time.Minute).Unix(); task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 10m)", task.ClaimExpiresAt, want)
	}
}

// The event records the duration the claim actually got, so a later change
// to the setting cannot make history misreport what an old claim was for.
func TestRunClaim_NoSettingRecordsThe30mDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "")

	task := MustGet(t, db, id)
	if want := base.Unix() + DefaultClaimTTLSeconds; task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 30m)", task.ClaimExpiresAt, want)
	}
	if got := lastClaimedDuration(t, db, id); got != "30m" {
		t.Errorf("claimed event duration: got %q, want 30m", got)
	}
}

func TestRunClaim_InvalidRepoConfigFailsNamingTheFile(t *testing.T) {
	db := SetupTestDB(t)
	path := writeRepoConfigFile(t, cachePathFor(t, db), `{"claim_duration": "forever"}`)
	id := MustAdd(t, db, "", "T")
	err := RunClaim(db, id, "", "", TestActor, false)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("claim: want an error naming %s, got %v", path, err)
	}
}

func TestRunClaimNext_UsesTheRepoDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	if err := SetRepoConfig(cachePathFor(t, db), ConfigClaimDuration, "90m"); err != nil {
		t.Fatal(err)
	}
	MustAdd(t, db, "", "T")
	task, err := RunClaimNext(db, "", "", TestActor, false)
	if err != nil {
		t.Fatalf("claim next: %v", err)
	}
	got := MustGet(t, db, task.ShortID)
	if want := base.Add(90 * time.Minute).Unix(); got.ClaimExpiresAt == nil || *got.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 90m)", got.ClaimExpiresAt, want)
	}
}

// A liveness signal renews a claim to the repo's default window, not to a
// hard-coded 30m: in a repo whose builds outlast 30m, a heartbeat that only
// bought 30m would lapse mid-build exactly as the setting exists to prevent.
func TestHeartbeat_ExtendsToTheRepoDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	if err := SetRepoConfig(cachePathFor(t, db), ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "10m")

	CurrentNowFunc = func() time.Time { return base.Add(5 * time.Minute) }
	if _, err := RunHeartbeat(db, []string{id}, TestActor); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	task := MustGet(t, db, id)
	if want := base.Add(5*time.Minute + 2*time.Hour).Unix(); task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 2h)", task.ClaimExpiresAt, want)
	}
}

func TestAutoExtendClaim_ExtendsToTheRepoDefault(t *testing.T) {
	origNow := CurrentNowFunc
	defer func() { CurrentNowFunc = origNow }()
	base := time.Now()
	CurrentNowFunc = func() time.Time { return base }

	db := SetupTestDB(t)
	if err := SetRepoConfig(cachePathFor(t, db), ConfigClaimDuration, "2h"); err != nil {
		t.Fatal(err)
	}
	id := MustAdd(t, db, "", "T")
	MustClaim(t, db, id, "10m")

	CurrentNowFunc = func() time.Time { return base.Add(5 * time.Minute) }
	if err := RunNote(db, id, "still working", nil, TestActor); err != nil {
		t.Fatalf("note: %v", err)
	}
	task := MustGet(t, db, id)
	if want := base.Add(5*time.Minute + 2*time.Hour).Unix(); task.ClaimExpiresAt == nil || *task.ClaimExpiresAt != want {
		t.Errorf("claim_expires_at: got %v, want %d (now + 2h)", task.ClaimExpiresAt, want)
	}
}

// lastClaimedDuration reads the duration string off id's latest claimed
// event.
func lastClaimedDuration(t *testing.T, db dbtx, id string) string {
	t.Helper()
	var detail string
	err := db.QueryRow(`
		SELECT e.detail FROM events e JOIN tasks t ON t.id = e.task_id
		WHERE t.short_id = ? AND e.event_type = 'claimed'
		ORDER BY e.id DESC LIMIT 1`, id).Scan(&detail)
	if err != nil {
		t.Fatalf("read claimed event: %v", err)
	}
	var p ClaimedPayload
	if err := json.Unmarshal([]byte(detail), &p); err != nil {
		t.Fatalf("decode claimed payload %q: %v", detail, err)
	}
	return p.Duration
}
