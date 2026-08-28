package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
)

// RangeKey is the normalized `?range=` value — how far back a view
// looks. Shared by every bounded view (the Actors board, the Log)
// so one selector reads the same everywhere.
type RangeKey string

const (
	Range7D  RangeKey = "7d"
	Range14D RangeKey = "14d"
	Range30D RangeKey = "30d"
	RangeAll RangeKey = "all"
)

// DefaultRangeKey is what an absent or unrecognized `?range=` falls
// back to. A week is the span a human can hold in their head, and it
// keeps a long-lived store from rendering hundreds of stale columns.
const DefaultRangeKey = Range7D

// rangeDurations is the window each key names. RangeAll is absent —
// it has no duration, which is what makes it unbounded.
var rangeDurations = map[RangeKey]time.Duration{
	Range7D:  7 * 24 * time.Hour,
	Range14D: 14 * 24 * time.Hour,
	Range30D: 30 * 24 * time.Hour,
}

// rangeOrder is the selector's left-to-right order and its labels.
var rangeOrder = []struct {
	Key   RangeKey
	Label string
}{
	{Range7D, "7D"},
	{Range14D, "14D"},
	{Range30D, "30D"},
	{RangeAll, "All"},
}

// Range is a parsed `?range=` selection anchored at a moment in time.
// Cutoff is the unix second at (and after) which events are in the
// window; zero means "no lower bound" — the RangeAll case.
type Range struct {
	Key      RangeKey
	Duration time.Duration
	Cutoff   int64
}

// Bounded reports whether the range excludes anything at all.
func (rg Range) Bounded() bool { return rg.Cutoff > 0 }

// Includes reports whether a unix-second timestamp falls inside the
// window. The cutoff second itself is inside.
func (rg Range) Includes(sec int64) bool { return !rg.Bounded() || sec >= rg.Cutoff }

// RangeTab is one option in the range selector: a plain link, marked
// active when it names the current selection. Rendered with the
// existing `c-tabs` / `c-tab` link group.
type RangeTab struct {
	Label  string
	URL    string
	Active bool
}

// parseRange normalizes `?range=` and measures the window back from
// anchor. Unknown, empty and malformed values collapse to the default
// rather than erroring — a range is a view preference, not an
// addressable resource, so a bad one should still render a page.
//
// anchor is the moment the view is pinned to: wall-clock now in live
// mode, and the scrubber cursor's event time under `?at=` (see
// rangeAnchor), so scrubbing back a month doesn't empty a 7-day view.
func parseRange(q url.Values, anchor time.Time) Range {
	key := parseRangeKey(q.Get("range"))
	d, bounded := rangeDurations[key]
	rg := Range{Key: key, Duration: d}
	if bounded {
		rg.Cutoff = anchor.Add(-d).Unix()
	}
	return rg
}

// parseRangeKey normalizes one raw `?range=` value.
func parseRangeKey(raw string) RangeKey {
	switch RangeKey(strings.ToLower(strings.TrimSpace(raw))) {
	case Range7D:
		return Range7D
	case Range14D:
		return Range14D
	case Range30D:
		return Range30D
	case RangeAll:
		return RangeAll
	default:
		return DefaultRangeKey
	}
}

// buildRangeTabs returns the selector's four options for a view at
// base, preserving every other query parameter (`?at=`, label
// filters) so switching the range keeps the rest of the view. The
// default range is expressed by omitting `range=` entirely, keeping
// the canonical URL of a view clean.
func buildRangeTabs(base string, q url.Values, active RangeKey) []RangeTab {
	tabs := make([]RangeTab, 0, len(rangeOrder))
	for _, opt := range rangeOrder {
		next := url.Values{}
		for k, vs := range q {
			if k == "range" {
				continue
			}
			next[k] = append([]string(nil), vs...)
		}
		if opt.Key != DefaultRangeKey {
			next.Set("range", string(opt.Key))
		}
		u := base
		if encoded := next.Encode(); encoded != "" {
			u += "?" + encoded
		}
		tabs = append(tabs, RangeTab{Label: opt.Label, URL: u, Active: opt.Key == active})
	}
	return tabs
}

// rangeAnchor returns the moment a range should be measured back
// from. In live mode (at <= 0) that is now; under the time-travel
// upper bound `?at=<event id>` it is that event's own timestamp, so a
// 7-day window scrubbed to last month shows the week before *then*.
// An `at` past the end of the log falls back to the newest event, and
// an empty log falls back to now.
func rangeAnchor(ctx context.Context, db *sql.DB, at int64, now time.Time) (time.Time, error) {
	if at <= 0 {
		return now, nil
	}
	var createdAt int64
	err := db.QueryRowContext(ctx,
		`SELECT created_at FROM events WHERE id <= ? ORDER BY id DESC LIMIT 1`, at).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return now, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(createdAt, 0), nil
}
