package job

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// pItH3 — machine-local state lives in .jobs/local.json beside the cache,
// never in the cache and never in the shared log. These tests pin the file
// itself; the identity and focus tests exercise the callers.

func TestLoadLocalState_MissingFileIsTheZeroState(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	s, err := LoadLocalState(cache)
	if err != nil {
		t.Fatalf("LoadLocalState on a missing file: %v", err)
	}
	if s == nil {
		t.Fatal("LoadLocalState returned nil state")
	}
	if s.Rep != "" || s.LastSeen != 0 || s.Identity != "" || s.Strict || len(s.Focus) != 0 {
		t.Errorf("missing file yielded %+v, want the zero state", s)
	}
}

func TestLocalState_SaveThenLoadRoundTripsEveryField(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	want := &LocalState{
		Rep:      "k7Qx2m",
		LastSeen: 1756742400123,
		Identity: "ben",
		Strict:   true,
		Focus: map[string]FocusSlots{
			"ben":   {KindTask: "VBF5u", KindIssue: "AbC12"},
			"alice": {KindTask: "QnB2g"},
		},
	}
	if err := want.Save(cache); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadLocalState(cache)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if got.Rep != want.Rep || got.LastSeen != want.LastSeen {
		t.Errorf("reserved fields: got rep=%q last_seen=%d, want %q/%d", got.Rep, got.LastSeen, want.Rep, want.LastSeen)
	}
	if got.Identity != want.Identity || got.Strict != want.Strict {
		t.Errorf("identity: got %q strict=%v, want %q/%v", got.Identity, got.Strict, want.Identity, want.Strict)
	}
	for actor, slots := range want.Focus {
		for kind, root := range slots {
			if got.FocusRoot(actor, kind) != root {
				t.Errorf("focus[%s][%s] = %q, want %q", actor, kind, got.FocusRoot(actor, kind), root)
			}
		}
	}
}

// The JSON keys are the wire format the store leaf's replica id and clock
// will share, so they are pinned here rather than left to the struct tags.
func TestLocalState_JSONKeys(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	s := &LocalState{Rep: "k7Qx2m", LastSeen: 42, Identity: "ben", Strict: true}
	s.SetFocusRoot("ben", KindIssue, "AbC12")
	if err := s.Save(cache); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b, err := os.ReadFile(LocalStatePath(cache))
	if err != nil {
		t.Fatalf("read local.json: %v", err)
	}
	for _, key := range []string{`"rep"`, `"last_seen"`, `"identity"`, `"strict"`, `"focus"`, `"issue"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("local.json is missing key %s: %s", key, b)
		}
	}
}

func TestLocalStatePath_IsInsideTheStoreDirBesideTheCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, ".jobs.db")
	want := filepath.Join(dir, ".jobs", "local.json")
	if got := LocalStatePath(cache); got != want {
		t.Errorf("LocalStatePath = %q, want %q", got, want)
	}
}

func TestLoadLocalState_MalformedFileErrorsNamingThePath(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	path := LocalStatePath(cache)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadLocalState(cache)
	if err == nil {
		t.Fatal("LoadLocalState on a malformed file returned no error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name %s", err, path)
	}
}

// Two agents setting focus at the same time must both persist: the
// read-modify-write happens under the store lock.
func TestUpdateLocalState_ConcurrentActorsBothPersist(t *testing.T) {
	cache := filepath.Join(t.TempDir(), ".jobs.db")
	actors := []string{"agent-a", "agent-b", "agent-c", "agent-d"}
	roots := map[string]string{"agent-a": "AAAAA", "agent-b": "BBBBB", "agent-c": "CCCCC", "agent-d": "DDDDD"}

	var wg sync.WaitGroup
	errs := make(chan error, len(actors))
	for _, actor := range actors {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			for range 10 {
				err := UpdateLocalState(cache, func(s *LocalState) error {
					s.SetFocusRoot(actor, KindTask, roots[actor])
					return nil
				})
				if err != nil {
					errs <- err
					return
				}
			}
		}(actor)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("UpdateLocalState: %v", err)
	}

	s, err := LoadLocalState(cache)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	for _, actor := range actors {
		if got := s.FocusRoot(actor, KindTask); got != roots[actor] {
			t.Errorf("focus for %s = %q, want %q (a concurrent write clobbered it)", actor, got, roots[actor])
		}
	}
}

// The cache path reaches the identity and focus code through the open
// database: SQLite knows which file it was opened from.
func TestCachePathOf_NamesTheOpenedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	got, err := CachePathOf(db)
	if err != nil {
		t.Fatalf("CachePathOf: %v", err)
	}
	// SQLite reports the fully resolved path, so compare through symlinks
	// (macOS temp dirs live under /private/var but are named /var).
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("CachePathOf = %q, want %q", got, want)
	}
}

// bH0 — deleting the cache must not lose the default identity, the strict
// flag or the focus. They live beside it, not inside it.
func TestDeletingTheCacheKeepsIdentityStrictAndFocus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	db, err := CreateDB(path)
	if err != nil {
		t.Fatal(err)
	}
	root := MustAdd(t, db, "", "Root")
	if err := SetDefaultIdentity(db, "ben"); err != nil {
		t.Fatalf("SetDefaultIdentity: %v", err)
	}
	if err := SetStrict(db, true); err != nil {
		t.Fatalf("SetStrict: %v", err)
	}
	if _, err := SetFocus(db, root, "ben"); err != nil {
		t.Fatalf("SetFocus: %v", err)
	}
	db.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove cache: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}

	fresh, err := CreateDB(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer fresh.Close()

	id, err := GetDefaultIdentity(fresh)
	if err != nil {
		t.Fatalf("GetDefaultIdentity: %v", err)
	}
	if id != "ben" {
		t.Errorf("default identity after deleting the cache: got %q, want %q", id, "ben")
	}
	strict, err := IsStrict(fresh)
	if err != nil {
		t.Fatalf("IsStrict: %v", err)
	}
	if !strict {
		t.Error("strict flag did not survive deleting the cache")
	}
	// The focused root itself lived in the cache, so it is gone; what must
	// survive is the recorded pointer.
	s, err := LoadLocalState(path)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	if got := s.FocusRoot("ben", KindTask); got != root {
		t.Errorf("focus after deleting the cache: got %q, want %q", got, root)
	}
}

// D2h — no focus event is recorded anywhere any more.
func TestNoFocusEventIsRecorded(t *testing.T) {
	db := SetupTestDB(t)
	root := MustAdd(t, db, "", "Root")
	leaf := MustAdd(t, db, root, "Leaf")
	other := MustAdd(t, db, "", "Other root")
	otherLeaf := MustAdd(t, db, other, "Other leaf")

	steps := []struct {
		name string
		run  func() error
	}{
		{"focus", func() error { _, err := SetFocus(db, root, TestActor); return err }},
		{"claim", func() error { return RunClaim(db, leaf, "1h", "", TestActor, false) }},
		{"done", func() error {
			_, _, err := RunDone(db, []string{leaf}, false, "", nil, TestActor, false, "")
			return err
		}},
		{"release focus", func() error { return ReleaseFocus(db, TestActor) }},
		{"claim other", func() error { return RunClaim(db, otherLeaf, "1h", "", TestActor, false) }},
		{"cancel", func() error {
			_, _, _, err := RunCancel(db, []string{other}, "abandoned", true, false, true, TestActor)
			return err
		}},
	}
	for _, step := range steps {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		var n int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM events WHERE event_type IN ('focus_set','focus_released')",
		).Scan(&n); err != nil {
			t.Fatalf("count focus events after %s: %v", step.name, err)
		}
		if n != 0 {
			t.Fatalf("after %s: %d focus events recorded, want 0", step.name, n)
		}
	}
}

// localFocusSlot reads an actor's focus slot straight out of local.json.
// Tests that used to count focus_set / focus_released events assert on this
// instead: the slot is now the whole record of a focus.
func localFocusSlot(t *testing.T, db *sql.DB, actor string, kind TreeKind) string {
	t.Helper()
	path, err := CachePathOf(db)
	if err != nil {
		t.Fatalf("CachePathOf: %v", err)
	}
	s, err := LoadLocalState(path)
	if err != nil {
		t.Fatalf("LoadLocalState: %v", err)
	}
	return s.FocusRoot(actor, kind)
}

// focusEventCount counts focus rows in the events table. Every caller wants
// zero: focus events are history, never written again.
func focusEventCount(t *testing.T, db dbtx, actor string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM events WHERE event_type IN ('focus_set','focus_released') AND actor = ?", actor,
	).Scan(&n); err != nil {
		t.Fatalf("count focus events for %s: %v", actor, err)
	}
	return n
}
