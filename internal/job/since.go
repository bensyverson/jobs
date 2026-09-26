package job

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ParseSince parses the `--since` grammar of `job log`. The value may be:
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
	t, err := parseMoment(s, time.Now())
	if err != nil {
		return nil, fmt.Errorf("--since: expected RFC3339 timestamp or duration (e.g. 5m, 2h), got %q", s)
	}
	u := t.Unix()
	return &u, nil
}

// errNotAMoment is parseMoment's refusal; callers word their own
// message, since each accepts a different grammar around it.
var errNotAMoment = errors.New("not a timestamp or duration")

// parseMoment is the grammar `job log --since` and `job stats` share:
// an RFC3339 timestamp, or a relative duration measured back from now.
func parseMoment(s string, now time.Time) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts, nil
	}
	if seconds, err := ParseDuration(s); err == nil {
		return now.Add(-time.Duration(seconds) * time.Second), nil
	}
	return time.Time{}, errNotAMoment
}

// ParseWindowStart parses `job stats --since`: a range key (1h, 1d, 7d,
// 14d, 30d, all), a relative duration, or an RFC3339 timestamp. Empty
// and "all" return the zero time — no lower bound, which ReportQuery
// reads as "from the first event".
func ParseWindowStart(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	if key, ok := ParseRangeKey(raw); ok {
		d, bounded := key.Duration()
		if !bounded {
			return time.Time{}, nil
		}
		return now.Add(-d), nil
	}
	t, err := parseMoment(raw, now)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since: expected a range key (%s), a duration (e.g. 90m, 3d) or an RFC3339 timestamp, got %q", rangeKeyList(), raw)
	}
	return t, nil
}

// ParseWindowEnd parses `job stats --until`: a relative duration or an
// RFC3339 timestamp. Empty returns the zero time, which ReportQuery
// reads as now. "all" names no end, so it is refused.
func ParseWindowEnd(raw string, now time.Time) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := parseMoment(raw, now)
	if err != nil {
		return time.Time{}, fmt.Errorf("--until: expected a duration (e.g. 1d) or an RFC3339 timestamp, got %q", raw)
	}
	return t, nil
}

func rangeKeyList() string {
	var s strings.Builder
	for i, k := range rangeKeys {
		if i > 0 {
			s.WriteString(", ")
		}
		s.WriteString(string(k))
	}
	return s.String()
}
