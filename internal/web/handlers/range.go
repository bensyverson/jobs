package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
)

// The `?range=` vocabulary — keys, durations, the default and parsing —
// lives in internal/job (timerange.go) so `job stats` and the dashboard
// agree on it. What stays here is presentation: which keys a view
// offers, their tab labels, and the tab links.

// rangeOption is one key a view offers in its selector, with its tab
// label, in left-to-right order.
type rangeOption struct {
	Key   job.RangeKey
	Label string
}

// boundedViewRanges is what the Actors board and the Log offer. They
// predate the hour and day keys and keep exactly these four tabs; a
// `?range=1h` on them falls back to the default like any unknown key.
// Mirrored by BOUNDED_VIEW_RANGES in assets/js/range.mjs.
var boundedViewRanges = []rangeOption{
	{job.Range7D, "7D"},
	{job.Range14D, "14D"},
	{job.Range30D, "30D"},
	{job.RangeAll, "All"},
}

// homeRanges is what Home's chart panel offers: every key. 1H keeps the
// one-minute live histogram reachable (reporting decision 10).
var homeRanges = []rangeOption{
	{job.RangeHour, "1H"},
	{job.RangeDay, "1D"},
	{job.Range7D, "7D"},
	{job.Range14D, "14D"},
	{job.Range30D, "30D"},
	{job.RangeAll, "All"},
}

// RangeTab is one option in the range selector: a plain link, marked
// active when it names the current selection. Rendered with the
// existing `c-tabs` / `c-tab` link group.
type RangeTab struct {
	Label  string
	URL    string
	Active bool
}

// parseRange normalizes `?range=` against the keys a view offers and
// measures the window back from anchor. Unknown, unoffered, empty and
// malformed values collapse to the default rather than erroring — a
// range is a view preference, not an addressable resource, so a bad
// one should still render a page.
//
// anchor is the moment the view is pinned to: wall-clock now in live
// mode, and the scrubber cursor's event time under `?at=` (see
// rangeAnchor), so scrubbing back a month doesn't empty a 7-day view.
func parseRange(q url.Values, anchor time.Time, offered []rangeOption) job.Range {
	return job.NewRange(parseRangeKey(q.Get("range"), offered), anchor)
}

// parseRangeKey normalizes one raw `?range=` value, accepting only a
// key the view offers.
func parseRangeKey(raw string, offered []rangeOption) job.RangeKey {
	key, ok := job.ParseRangeKey(raw)
	if !ok || !slices.ContainsFunc(offered, func(o rangeOption) bool { return o.Key == key }) {
		return job.DefaultRangeKey
	}
	return key
}

// buildRangeTabs returns the selector's options for a view at base,
// preserving every other query parameter (`?at=`, label filters) so
// switching the range keeps the rest of the view. The default range is
// expressed by omitting `range=` entirely, keeping the canonical URL
// of a view clean.
func buildRangeTabs(base string, q url.Values, active job.RangeKey, offered []rangeOption) []RangeTab {
	tabs := make([]RangeTab, 0, len(offered))
	for _, opt := range offered {
		next := url.Values{}
		for k, vs := range q {
			if k == "range" {
				continue
			}
			next[k] = append([]string(nil), vs...)
		}
		if opt.Key != job.DefaultRangeKey {
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
// from. In live mode (a zero `at`) that is now; under the time-travel
// upper bound `?at=<log position>` it is that event's own timestamp, so a
// 7-day window scrubbed to last month shows the week before *then*.
// An `at` past the end of the log falls back to the newest event, and
// an empty log falls back to now.
func rangeAnchor(ctx context.Context, db *sql.DB, at eventlog.Position, now time.Time) (time.Time, error) {
	if at == (eventlog.Position{}) {
		return now, nil
	}
	expr := job.EventPositionExpr("e")
	query := `SELECT e.created_at FROM events e WHERE ` + expr + ` <= (?, ?, ?)
		ORDER BY e.ts DESC, e.rep DESC, CASE WHEN e.rep = '' THEN e.id ELSE e.seq END DESC LIMIT 1`
	var createdAt int64
	err := db.QueryRowContext(ctx, query, job.EventPositionArgs(at)...).Scan(&createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return now, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(createdAt, 0), nil
}
