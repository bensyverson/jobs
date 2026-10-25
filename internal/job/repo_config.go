package job

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The repo config is the repo's shared settings: .jobs/config.json, beside
// .jobs/log/ and committed with it, so every checkout and worktree of the
// repo gets the same values on checkout. It is not machine-local state —
// that is .jobs/local.json, which `job gitignore` excludes — and it is not
// in the event log: a setting is not task history. See
// project/2026-10-24-worktree-and-criteria-papercuts.md §3.

// repoConfigFileName is the file inside the store directory holding it.
const repoConfigFileName = "config.json"

// RepoConfig is the whole content of .jobs/config.json. Every field is
// optional; an absent field means the built-in default.
type RepoConfig struct {
	// ClaimDuration is the claim window a claim gets when no duration is
	// passed, and the window a heartbeat renews to. It uses the claim
	// duration grammar ("90m", "2h").
	ClaimDuration string `json:"claim_duration,omitempty"`
}

// ConfigKey names one setting as `job config` spells it.
type ConfigKey string

const (
	// ConfigClaimDuration is RepoConfig.ClaimDuration.
	ConfigClaimDuration ConfigKey = "claim-duration"
)

// configKeys is every key `job config` accepts, in the order help lists them.
var configKeys = []ConfigKey{ConfigClaimDuration}

// ConfigValue is one setting's effective value. IsDefault reports that the
// file does not set it and Value is the built-in default.
type ConfigValue struct {
	Key       ConfigKey
	Value     string
	IsDefault bool
}

// ConfigKeys returns every key `job config` accepts.
func ConfigKeys() []ConfigKey {
	return append([]ConfigKey(nil), configKeys...)
}

// ParseConfigKey resolves a key as typed on the command line.
func ParseConfigKey(s string) (ConfigKey, error) {
	for _, k := range configKeys {
		if string(k) == s {
			return k, nil
		}
	}
	names := make([]string, len(configKeys))
	for i, k := range configKeys {
		names[i] = string(k)
	}
	return "", fmt.Errorf("unknown setting %q; known settings: %s", s, strings.Join(names, ", "))
}

// RepoConfigPath returns the repo config beside the cache named by
// cachePath: the same .jobs/ store directory that holds the log, so the
// config follows the log wherever --db points.
func RepoConfigPath(cachePath string) string {
	return filepath.Join(eventlog.StoreDir(cachePath), repoConfigFileName)
}

// LoadRepoConfig reads and validates the repo config beside cachePath. A
// missing file is the zero config. A malformed file, an unknown key or an
// invalid value is an error naming the file: the file is hand-editable, and
// a typo that silently fell back to the default would look like a bug in
// the setting.
func LoadRepoConfig(cachePath string) (*RepoConfig, error) {
	path := RepoConfigPath(cachePath)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &RepoConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c RepoConfig
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// validate checks every set field.
func (c *RepoConfig) validate() error {
	if c.ClaimDuration != "" {
		if _, err := parseClaimWindow(c.ClaimDuration); err != nil {
			return fmt.Errorf("claim_duration: %w", err)
		}
	}
	return nil
}

// parseClaimWindow parses a claim duration and requires it to be positive: a
// zero or negative window would make every claim expire as it is taken.
func parseClaimWindow(s string) (int64, error) {
	seconds, err := ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("invalid duration %q: must be positive", s)
	}
	return seconds, nil
}

// save writes the config atomically beside cachePath.
func (c *RepoConfig) save(cachePath string) error {
	path := RepoConfigPath(cachePath)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeFileAtomic(path, append(b, '\n'))
}

// updateRepoConfig applies fn to the repo config and saves the result, with
// the read-modify-write under the store lock. The result is validated before
// it is written, so a rejected value never reaches the file.
func updateRepoConfig(cachePath string, fn func(*RepoConfig)) error {
	lock, err := eventlog.AcquireLock(cachePath)
	if err != nil {
		return err
	}
	defer lock.Release()

	c, err := LoadRepoConfig(cachePath)
	if err != nil {
		return err
	}
	fn(c)
	if err := c.validate(); err != nil {
		return fmt.Errorf("%s: %w", RepoConfigPath(cachePath), err)
	}
	return c.save(cachePath)
}

// SetRepoConfig sets key to value in the repo config beside cachePath.
func SetRepoConfig(cachePath string, key ConfigKey, value string) error {
	switch key {
	case ConfigClaimDuration:
		return updateRepoConfig(cachePath, func(c *RepoConfig) { c.ClaimDuration = value })
	}
	return fmt.Errorf("unknown setting %q", key)
}

// UnsetRepoConfig removes key from the repo config beside cachePath, so the
// built-in default applies again.
func UnsetRepoConfig(cachePath string, key ConfigKey) error {
	return SetRepoConfig(cachePath, key, "")
}

// GetRepoConfig returns key's effective value from the repo config beside
// cachePath.
func GetRepoConfig(cachePath string, key ConfigKey) (ConfigValue, error) {
	c, err := LoadRepoConfig(cachePath)
	if err != nil {
		return ConfigValue{}, err
	}
	switch key {
	case ConfigClaimDuration:
		if c.ClaimDuration != "" {
			return ConfigValue{Key: key, Value: c.ClaimDuration}, nil
		}
		return ConfigValue{Key: key, Value: FormatDuration(DefaultClaimTTLSeconds), IsDefault: true}, nil
	}
	return ConfigValue{}, fmt.Errorf("unknown setting %q", key)
}

// ResolveClaimDuration is the one place a claim's duration is decided: an
// explicit duration wins, then the repo's claim-duration setting, then the
// built-in 30m. Every path that claims — claim, claim --next,
// done --claim-next, reopen's auto-claim — reaches it through RunClaim, and
// callers that print the duration ask it too, so the ack and the deadline
// cannot disagree.
func ResolveClaimDuration(db dbtx, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	cachePath, err := CachePathOf(db)
	if err != nil {
		return "", err
	}
	v, err := GetRepoConfig(cachePath, ConfigClaimDuration)
	if err != nil {
		return "", err
	}
	return v.Value, nil
}

// claimWindowSeconds is the repo's default claim window in seconds: the
// window a liveness signal renews a claim to.
func claimWindowSeconds(db dbtx) (int64, error) {
	d, err := ResolveClaimDuration(db, "")
	if err != nil {
		return 0, err
	}
	return parseClaimWindow(d)
}
