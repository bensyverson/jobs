package job

import (
	"database/sql"
	"fmt"
)

// The default writer identity and strict mode are machine-local: they say who
// is at this keyboard, not what happened to the tasks. They live in
// .jobs/local.json beside the cache (see local.go), never in the cache — which
// is disposable — and never in the log, which every other machine reads.
//
// The `config` table remains in the schema and is no longer read or written.

// GetDefaultIdentity returns the configured default writer identity, or
// "" if unset.
func GetDefaultIdentity(db dbtx) (string, error) {
	s, err := loadLocal(db)
	if err != nil {
		return "", err
	}
	return s.Identity, nil
}

// SetDefaultIdentity sets the configured default writer identity.
func SetDefaultIdentity(db dbtx, name string) error {
	return updateLocal(db, func(s *LocalState) error {
		s.Identity = name
		return nil
	})
}

// IsStrict reports whether strict mode is enabled. Default is permissive
// (false) when nothing has set it.
func IsStrict(db dbtx) (bool, error) {
	s, err := loadLocal(db)
	if err != nil {
		return false, err
	}
	return s.Strict, nil
}

// SetStrict toggles strict mode.
func SetStrict(db dbtx, on bool) error {
	return updateLocal(db, func(s *LocalState) error {
		s.Strict = on
		return nil
	})
}

// InitIdentity records the identity choice `job init` made and clears
// everything the previous database left behind: the strict flag and every
// actor's focus, whose root short ids belong to tasks that no longer exist.
// The replica id and the clock are kept — they describe this checkout, not
// the cache that was overwritten.
func InitIdentity(db dbtx, name string, strict bool) error {
	return updateLocal(db, func(s *LocalState) error {
		s.Identity = name
		s.Strict = strict
		s.Focus = nil
		return nil
	})
}

// ResolveIdentity applies the P3 resolution chain for writes:
//  1. flagValue — the --as flag value (wins if non-empty).
//  2. the default identity from local.json, unless strict mode is on.
//  3. "" — caller must error with "identity required".
func ResolveIdentity(db dbtx, flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	s, err := loadLocal(db)
	if err != nil {
		return "", err
	}
	if s.Strict {
		return "", nil
	}
	return s.Identity, nil
}

// seedLocalStateFromConfig moves a legacy database's default_identity and
// strict rows from the config table into local.json and deletes them, so
// the seed runs at most once. A database with no such rows is untouched.
func seedLocalStateFromConfig(db *sql.DB, cachePath string) error {
	rows, err := db.Query(`SELECT key, value FROM config WHERE key IN ('default_identity', 'strict')`)
	if err != nil {
		return fmt.Errorf("read legacy config: %w", err)
	}
	legacy := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return err
		}
		legacy[k] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(legacy) == 0 {
		return nil
	}
	if err := UpdateLocalState(cachePath, func(s *LocalState) error {
		if v, ok := legacy["default_identity"]; ok && s.Identity == "" {
			s.Identity = v
		}
		if v, ok := legacy["strict"]; ok {
			s.Strict = s.Strict || v == "true"
		}
		return nil
	}); err != nil {
		return fmt.Errorf("seed local state from config: %w", err)
	}
	_, err = db.Exec(`DELETE FROM config WHERE key IN ('default_identity', 'strict')`)
	return err
}
