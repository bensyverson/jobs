package job

import (
	"fmt"
	"time"
)

// ParseSince parses the shared `--since` grammar accepted by both
// `job log` and `job status --usage`. The value may be:
//
//   - an RFC3339 timestamp (e.g. "2026-04-28T10:00:00Z"), interpreted
//     as the absolute cutoff moment, or
//   - a relative duration (e.g. "5m", "2h", "7d"), interpreted as
//     "now − duration".
//
// Returns the unix timestamp cutoff, or (nil, nil) when s is empty.
func ParseSince(s string) (*int64, error) {
	if s == "" {
		return nil, nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		u := ts.Unix()
		return &u, nil
	}
	if seconds, err := ParseDuration(s); err == nil {
		u := time.Now().Unix() - seconds
		return &u, nil
	}
	return nil, fmt.Errorf("--since: expected RFC3339 timestamp or duration (e.g. 5m, 2h), got %q", s)
}
