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
	BucketMinute      Bucket = "minute"
	BucketFiveMinutes Bucket = "5m"
	BucketHour        Bucket = "hour"
	BucketSixHours    Bucket = "6h"
	BucketTwelveHours Bucket = "12h"
	BucketDay         Bucket = "day"
	BucketWeek        Bucket = "week"
)

// buckets is every bucket, narrowest first.
var buckets = []Bucket{BucketMinute, BucketFiveMinutes, BucketHour, BucketSixHours, BucketTwelveHours, BucketDay, BucketWeek}

// Buckets returns every bucket, narrowest first.
func Buckets() []Bucket { return slices.Clone(buckets) }

// ParseBucket normalizes a raw bucket name (trimmed, case-insensitive),
// as `job stats --by` takes it. ok is false for anything unknown,
// including empty.
func ParseBucket(raw string) (b Bucket, ok bool) {
	b = Bucket(strings.ToLower(strings.TrimSpace(raw)))
	return b, slices.Contains(buckets, b)
}

// nominal is the bucket's usual length, for counting how many fit a span.
// Calendar buckets vary around it across DST; the count only picks a unit.
func (b Bucket) nominal() time.Duration {
	switch b {
	case BucketMinute:
		return time.Minute
	case BucketFiveMinutes:
		return 5 * time.Minute
	case BucketHour:
		return time.Hour
	case BucketSixHours:
		return 6 * time.Hour
	case BucketTwelveHours:
		return 12 * time.Hour
	case BucketDay:
		return 24 * time.Hour
	case BucketWeek:
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

// maxBars is the most buckets the automatic choice lays over a span. The
// narrowest unit within it lands every named range key on its decided bar
// count — 1h in 12 five-minute bars, 1d in 24 hours, 7d in 28 six-hour bars,
// 14d in 28 twelve-hour bars, 30d in 30 days — because the next unit down
// would draw 60, 288, 168, 56 and 60. Between the named spans the calendar
// units are too far apart to always land 25–30: past 45 days a history goes
// weekly, and past 45 weeks it draws more than 45 bars, there being no month.
const maxBars = 45

// BucketForSpan is the bucket for a window of arbitrary span: the narrowest
// unit that keeps it to maxBars. It is the single rule — BucketFor is this
// over each key's span — so a window of any span agrees with the named key
// it matches.
func BucketForSpan(span time.Duration) Bucket {
	for _, b := range buckets {
		if span <= maxBars*b.nominal() {
			return b
		}
	}
	return BucketWeek
}

// BucketFor returns the bucket a series over key uses: BucketForSpan of the
// key's window. history is the span of events available and only matters for
// RangeAll, whose window is that span. An unrecognized key buckets like the
// default it would parse to.
func BucketFor(key RangeKey, history time.Duration) Bucket {
	if key == RangeAll {
		return BucketForSpan(history)
	}
	d, bounded := key.Duration()
	if !bounded {
		d, _ = DefaultRangeKey.Duration()
	}
	return BucketForSpan(d)
}

// Floor returns the start of the bucket holding t, aligned to the
// local calendar of t's location: minutes on the local clock (so a
// +05:45 zone's five-minute buckets start at :00 local), hours
// likewise, six- and twelve-hour buckets at local 00/06/12/18 and
// 00/12, days at local midnight, and weeks at Monday's local midnight.
func (b Bucket) Floor(t time.Time) time.Time {
	loc := t.Location()
	y, m, d := t.Date()
	switch b {
	case BucketMinute:
		return floorOnLocalClock(t, time.Minute)
	case BucketFiveMinutes:
		return floorOnLocalClock(t, 5*time.Minute)
	case BucketHour:
		return floorOnLocalClock(t, time.Hour)
	case BucketSixHours:
		return time.Date(y, m, d, t.Hour()-t.Hour()%6, 0, 0, 0, loc)
	case BucketTwelveHours:
		return time.Date(y, m, d, t.Hour()-t.Hour()%12, 0, 0, 0, loc)
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
// local calendar, so a day after a DST change is still midnight and a
// twelve-hour bucket still ends at local noon.
func (b Bucket) Next(start time.Time) time.Time {
	y, m, d := start.Date()
	switch b {
	case BucketMinute:
		return start.Add(time.Minute)
	case BucketFiveMinutes:
		return start.Add(5 * time.Minute)
	case BucketHour:
		return start.Add(time.Hour)
	case BucketSixHours:
		return time.Date(y, m, d, start.Hour()+6, 0, 0, 0, start.Location())
	case BucketTwelveHours:
		return time.Date(y, m, d, start.Hour()+12, 0, 0, 0, start.Location())
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
