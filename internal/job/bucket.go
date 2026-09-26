package job

import (
	"slices"
	"strings"
	"time"
)

// Bucket is the width of one sample in a time series over a range. It
// is a calendar unit rather than a time.Duration because the larger
// ones are local-calendar spans, not fixed lengths: a local day is 23
// or 25 hours across a DST change, and a week starts on Monday. Floor
// and Next are what make that explicit.
type Bucket string

const (
	BucketMinute   Bucket = "minute"
	BucketHour     Bucket = "hour"
	BucketSixHours Bucket = "6h"
	BucketDay      Bucket = "day"
	BucketWeek     Bucket = "week"
)

// buckets is every bucket, narrowest first.
var buckets = []Bucket{BucketMinute, BucketHour, BucketSixHours, BucketDay, BucketWeek}

// Buckets returns every bucket, narrowest first.
func Buckets() []Bucket { return slices.Clone(buckets) }

// ParseBucket normalizes a raw bucket name (trimmed, case-insensitive),
// as `job stats --by` takes it. ok is false for anything unknown,
// including empty.
func ParseBucket(raw string) (b Bucket, ok bool) {
	b = Bucket(strings.ToLower(strings.TrimSpace(raw)))
	return b, slices.Contains(buckets, b)
}

// AllRangeWeeklyAfter is the span of history past which RangeAll
// buckets by week instead of by day.
const AllRangeWeeklyAfter = 90 * 24 * time.Hour

// BucketFor returns the bucket a series over key uses: the natural
// calendar unit for the window, which lands between about a dozen and
// ninety samples. history is the span of events
// available and only matters for RangeAll, whose window is that span.
// An unrecognized key buckets like the default it would parse to.
func BucketFor(key RangeKey, history time.Duration) Bucket {
	switch key {
	case RangeHour:
		return BucketMinute
	case RangeDay:
		return BucketHour
	case Range7D:
		return BucketSixHours
	case Range14D, Range30D:
		return BucketDay
	case RangeAll:
		if history > AllRangeWeeklyAfter {
			return BucketWeek
		}
		return BucketDay
	default:
		return BucketFor(DefaultRangeKey, history)
	}
}

// Floor returns the start of the bucket holding t, aligned to the
// local calendar of t's location: minutes and hours on the local
// clock (so a +05:30 zone's hours start at :00 local), six-hour
// buckets at local 00/06/12/18, days at local midnight, and weeks at
// Monday's local midnight.
func (b Bucket) Floor(t time.Time) time.Time {
	loc := t.Location()
	y, m, d := t.Date()
	switch b {
	case BucketMinute:
		return floorOnLocalClock(t, time.Minute)
	case BucketHour:
		return floorOnLocalClock(t, time.Hour)
	case BucketSixHours:
		return time.Date(y, m, d, t.Hour()-t.Hour()%6, 0, 0, 0, loc)
	case BucketDay:
		return time.Date(y, m, d, 0, 0, 0, 0, loc)
	case BucketWeek:
		sinceMonday := (int(t.Weekday()) + 6) % 7
		return time.Date(y, m, d-sinceMonday, 0, 0, 0, 0, loc)
	default:
		return t
	}
}

// Next returns the start of the bucket after the one starting at
// start. start should come from Floor; calendar buckets step on the
// local calendar, so a day after a DST change is still midnight.
func (b Bucket) Next(start time.Time) time.Time {
	switch b {
	case BucketMinute:
		return start.Add(time.Minute)
	case BucketHour:
		return start.Add(time.Hour)
	case BucketSixHours:
		y, m, d := start.Date()
		return time.Date(y, m, d, start.Hour()+6, 0, 0, 0, start.Location())
	case BucketDay:
		return start.AddDate(0, 0, 1)
	case BucketWeek:
		return start.AddDate(0, 0, 7)
	default:
		return start
	}
}

// floorOnLocalClock truncates t to unit as read on its own zone's
// clock. time.Truncate works on absolute time, which in a zone with a
// non-whole-hour offset would floor to a UTC hour instead.
func floorOnLocalClock(t time.Time, unit time.Duration) time.Time {
	_, offset := t.Zone()
	shift := time.Duration(offset) * time.Second
	return t.Add(shift).Truncate(unit).Add(-shift).In(t.Location())
}
