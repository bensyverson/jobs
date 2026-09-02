package job

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Machine-local state: the default identity, strict mode and focus, plus the
// replica id and clock watermark the store leaf fills in.
//
// None of it belongs in the cache — .jobs.db is disposable and rebuilt from
// the log, so a default identity stored there would vanish with `rm .jobs.db`
// — and none of it belongs in the log either: focus is per-session workflow
// state and the identity is per-machine, so replicating them would push one
// machine's cursor onto another. It lives in .jobs/local.json beside the
// cache, which `job gitignore` excludes. See decision 2 in
// project/2026-09-01-git-native-event-log.md.

// localStateFileName is the file inside the store directory holding it.
const localStateFileName = "local.json"

// FocusSlots is one actor's focus, one root short id per tree kind. Focus is
// per actor and per kind so triaging a bug never loses your place in the plan.
type FocusSlots map[TreeKind]string

// LocalState is the whole content of .jobs/local.json.
type LocalState struct {
	// Rep is this checkout's replica id. Reserved for the store leaf;
	// nothing reads it yet.
	Rep string `json:"rep,omitempty"`
	// LastSeen is the hybrid logical clock's high-water mark in
	// milliseconds. Reserved for the store leaf; nothing reads it yet.
	LastSeen int64 `json:"last_seen,omitempty"`
	// Identity is the default writer identity used when --as is omitted.
	Identity string `json:"identity,omitempty"`
	// Strict disables the default identity: every write must pass --as.
	Strict bool `json:"strict,omitempty"`
	// Focus maps actor to that actor's focused root per tree kind.
	Focus map[string]FocusSlots `json:"focus,omitempty"`
}

// LocalStatePath returns the local-state file beside the cache named by
// cachePath. --db names the cache; the store is the .jobs/ directory next to
// it, and local.json sits directly inside.
func LocalStatePath(cachePath string) string {
	return filepath.Join(eventlog.StoreDir(cachePath), localStateFileName)
}

// LoadLocalState reads the local state beside cachePath. A missing file is
// the zero state, not an error — a fresh checkout has no local state until
// something writes one. A malformed file is an error naming the path, since
// silently starting over would drop the operator's identity without saying so.
func LoadLocalState(cachePath string) (*LocalState, error) {
	path := LocalStatePath(cachePath)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &LocalState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var s LocalState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the state atomically to the store beside cachePath: a temp file
// in the same directory, fsynced, then renamed over the target, so a crash
// mid-write cannot leave a half-written local.json.
func (s *LocalState) Save(cachePath string) error {
	path := LocalStatePath(cachePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), localStateFileName+".*")
	if err != nil {
		return fmt.Errorf("create temp beside %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename onto %s: %w", path, err)
	}
	return nil
}

// UpdateLocalState applies fn to the local state and saves the result, with
// the whole read-modify-write under the store lock. Parallel agents setting
// focus in one checkout serialize there instead of clobbering each other.
func UpdateLocalState(cachePath string, fn func(*LocalState) error) error {
	lock, err := eventlog.AcquireLock(cachePath)
	if err != nil {
		return err
	}
	defer lock.Release()

	s, err := LoadLocalState(cachePath)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return s.Save(cachePath)
}

// FocusRoot returns the root short id in an actor's slot, or "" when the slot
// is empty.
func (s *LocalState) FocusRoot(actor string, kind TreeKind) string {
	if s == nil {
		return ""
	}
	return s.Focus[actor][kind]
}

// SetFocusRoot points an actor's slot at a root short id.
func (s *LocalState) SetFocusRoot(actor string, kind TreeKind, root string) {
	if s.Focus == nil {
		s.Focus = map[string]FocusSlots{}
	}
	if s.Focus[actor] == nil {
		s.Focus[actor] = FocusSlots{}
	}
	s.Focus[actor][kind] = root
}

// ClearFocusRoot empties an actor's slot, dropping the actor's entry once no
// slot is left so an abandoned identity does not linger in the file.
func (s *LocalState) ClearFocusRoot(actor string, kind TreeKind) {
	slots, ok := s.Focus[actor]
	if !ok {
		return
	}
	delete(slots, kind)
	if len(slots) == 0 {
		delete(s.Focus, actor)
	}
	if len(s.Focus) == 0 {
		s.Focus = nil
	}
}

// CachePathOf asks the open database which file it was opened from. That path
// is what --db named, so it is also where the store and local.json live: the
// identity and focus code needs no separate plumbing to find them, and the two
// can never disagree.
//
// PRAGMA database_list names main's file; an in-memory database reports "",
// which is an error here because local state has nowhere to live.
func CachePathOf(db dbtx) (string, error) {
	var seq int
	var name, file string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file); err != nil {
		return "", fmt.Errorf("resolve cache path: %w", err)
	}
	if file == "" {
		return "", errors.New("this database has no file on disk, so it has no local state (.jobs/local.json)")
	}
	return file, nil
}

// loadLocal reads the local state beside db's file.
func loadLocal(db dbtx) (*LocalState, error) {
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}
	return LoadLocalState(path)
}

// updateLocal applies fn to the local state beside db's file, under the lock.
func updateLocal(db dbtx, fn func(*LocalState) error) error {
	path, err := CachePathOf(db)
	if err != nil {
		return err
	}
	return UpdateLocalState(path, fn)
}

// Compile-time assurance that *sql.DB and *sql.Tx both satisfy the reader
// these helpers take.
var _ dbtx = (*sql.DB)(nil)
