package job

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
