package job

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// knownBuckets is every Bucket that Floor and Next understand. An unknown one
// would never advance, so it is refused rather than looped on.
var knownBuckets = []Bucket{BucketMinute, BucketHour, BucketSixHours, BucketDay, BucketWeek}

// autoBucket chooses the sample width for a window from its span. An
// unbounded window (no Since) is RangeAll, whose span is the history's.
func autoBucket(span time.Duration) Bucket { return BucketForSpan(span) }

// reportWindow is a resolved window: its bounds and its buckets. Bucket i
// covers [starts[i], ends[i]); the first start is Since, clipped from the
// calendar floor, and the last end is Until.
type reportWindow struct {
	since, until     time.Time
	sinceMS, untilMS int64
	bucket           Bucket
	starts, ends     []time.Time
	endsMS           []int64
}

// resolveBuckets lays buckets over [since, until] on the local calendar of
// since's location. since == until yields one empty bucket, so a series
// always has a last sample at Until.
func resolveBuckets(since, until time.Time, bucket Bucket) (reportWindow, error) {
	if !slices.Contains(knownBuckets, bucket) {
		return reportWindow{}, fmt.Errorf("report: unknown bucket %q", bucket)
	}
	if since.After(until) {
		return reportWindow{}, fmt.Errorf("report: since %s is after until %s", since, until)
	}
	w := reportWindow{
		since: since, until: until, bucket: bucket,
		sinceMS: since.UnixMilli(), untilMS: until.UnixMilli(),
	}
	start := since
	for next := bucket.Next(bucket.Floor(since)); next.Before(until); next = bucket.Next(next) {
		w.starts = append(w.starts, start)
		w.ends = append(w.ends, next)
		start = next
	}
	w.starts = append(w.starts, start)
	w.ends = append(w.ends, until)
	w.endsMS = make([]int64, len(w.ends))
	for i, e := range w.ends {
		w.endsMS[i] = e.UnixMilli()
	}
	return w, nil
}

// inWindow reports whether a transition at ts (milliseconds) falls inside
// [Since, Until].
func (w reportWindow) inWindow(ts int64) bool { return ts >= w.sinceMS && ts <= w.untilMS }

// weeks is the window's span in weeks, for DonePerWeek.
func (w reportWindow) weeks() float64 {
	return w.until.Sub(w.since).Hours() / (7 * 24)
}

// locationName is the IANA name of loc. Go names the zone it loads from
// /etc/localtime "Local", which tells a reader on another machine nothing,
// so that one is resolved the way Go found it: TZ, else the zoneinfo file
// /etc/localtime links to. "Local" survives only when neither names a zone.
func locationName(loc *time.Location) string {
	name := loc.String()
	if name != "Local" {
		return name
	}
	if tz := os.Getenv("TZ"); tz != "" && !strings.HasPrefix(tz, ":") && !strings.HasPrefix(tz, "/") {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(target, "zoneinfo/"); ok && zone != "" {
			return zone
		}
	}
	return name
}
