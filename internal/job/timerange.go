package job

import (
	"slices"
	"strings"
	"time"
)

// RangeKey names how far back a report or a bounded view looks: the
// `?range=` value on the dashboard and the window vocabulary of
// `job stats`. One vocabulary, so the CLI and the dashboard agree on
// what "7d" means and how it is bucketed.
type RangeKey string

const (
	RangeHour RangeKey = "1h"
	RangeDay  RangeKey = "1d"
	Range7D   RangeKey = "7d"
	Range14D  RangeKey = "14d"
	Range30D  RangeKey = "30d"
	RangeAll  RangeKey = "all"
)

// DefaultRangeKey is what an absent or unrecognized key falls back
// to. A week is the span a human can hold in their head, and it keeps
// a long-lived store from rendering hundreds of stale columns.
const DefaultRangeKey = Range7D

// rangeKeys is every key, shortest window first; RangeAll is last.
var rangeKeys = []RangeKey{RangeHour, RangeDay, Range7D, Range14D, Range30D, RangeAll}

// rangeDurations is the window each key names. RangeAll is absent —
// it has no duration, which is what makes it unbounded.
var rangeDurations = map[RangeKey]time.Duration{
	RangeHour: time.Hour,
	RangeDay:  24 * time.Hour,
	Range7D:   7 * 24 * time.Hour,
	Range14D:  14 * 24 * time.Hour,
	Range30D:  30 * 24 * time.Hour,
}

// RangeKeys returns every known key, shortest window first, with
// RangeAll last. Which of them a given view offers is that view's
// decision.
func RangeKeys() []RangeKey { return append([]RangeKey(nil), rangeKeys...) }

// ParseRangeKey normalizes a raw key (trimmed, case-insensitive). An
// unknown or empty value returns DefaultRangeKey with ok false, so a
// view can fall back quietly and a CLI flag can refuse it.
func ParseRangeKey(raw string) (key RangeKey, ok bool) {
	k := RangeKey(strings.ToLower(strings.TrimSpace(raw)))
	if slices.Contains(rangeKeys, k) {
		return k, true
	}
	return DefaultRangeKey, false
}

// Duration is the window the key names; bounded is false for RangeAll
// and for any unrecognized key.
func (k RangeKey) Duration() (d time.Duration, bounded bool) {
	d, bounded = rangeDurations[k]
	return d, bounded
}

// Range is a range key anchored at a moment in time. Cutoff is the
// unix second at (and after) which events are in the window; zero
// means "no lower bound" — the RangeAll case.
type Range struct {
	Key      RangeKey
	Duration time.Duration
	Cutoff   int64
}

// NewRange measures key's window back from anchor. The anchor is the
// moment the report is pinned to — wall-clock now when live, the
// scrubber cursor's event time when parked in history.
func NewRange(key RangeKey, anchor time.Time) Range {
	d, bounded := key.Duration()
	rg := Range{Key: key, Duration: d}
	if bounded {
		rg.Cutoff = anchor.Add(-d).Unix()
	}
	return rg
}

// Bounded reports whether the range excludes anything at all.
func (rg Range) Bounded() bool { return rg.Cutoff > 0 }

// Includes reports whether a unix-second timestamp falls inside the
// window. The cutoff second itself is inside.
func (rg Range) Includes(sec int64) bool { return !rg.Bounded() || sec >= rg.Cutoff }
