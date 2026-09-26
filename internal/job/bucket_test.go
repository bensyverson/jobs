package job

import (
	"testing"
	"time"
)

// Decision 11 of project/2026-09-26-reporting.md, one case per key.
func TestBucketFor_PerKey(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		name    string
		key     RangeKey
		history time.Duration
		want    Bucket
	}{
		{"1h", RangeHour, 0, BucketMinute},
		{"1d", RangeDay, 0, BucketHour},
		{"7d", Range7D, 0, BucketSixHours},
		{"14d", Range14D, 0, BucketDay},
		{"30d", Range30D, 0, BucketDay},
		{"all, empty history", RangeAll, 0, BucketMinute},
		{"all, an hour of history", RangeAll, time.Hour, BucketMinute},
		{"all, a day of history", RangeAll, 24 * time.Hour, BucketHour},
		{"all, five days of history", RangeAll, 5 * day, BucketSixHours},
		{"all, 30 days", RangeAll, 30 * day, BucketDay},
		{"all, exactly 90 days", RangeAll, 90 * day, BucketDay},
		{"all, just past 90 days", RangeAll, 90*day + time.Second, BucketWeek},
		{"all, a year", RangeAll, 365 * day, BucketWeek},
		// History only matters for "all": a bounded key ignores it.
		{"7d, long history", Range7D, 365 * day, BucketSixHours},
		// An unrecognized key buckets like the default it would parse to.
		{"unknown key", RangeKey("bogus"), 0, BucketSixHours},
	}
	for _, c := range cases {
		if got := BucketFor(c.key, c.history); got != c.want {
			t.Errorf("%s: BucketFor(%q, %v) = %q, want %q", c.name, c.key, c.history, got, c.want)
		}
	}
}

func TestBucket_Floor(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// Friday 2026-08-28 13:47:31 in Chicago.
	fri := time.Date(2026, 8, 28, 13, 47, 31, 500, chicago)
	cases := []struct {
		name string
		b    Bucket
		in   time.Time
		want time.Time
	}{
		{"minute", BucketMinute, fri, time.Date(2026, 8, 28, 13, 47, 0, 0, chicago)},
		{"hour", BucketHour, fri, time.Date(2026, 8, 28, 13, 0, 0, 0, chicago)},
		{"6h", BucketSixHours, fri, time.Date(2026, 8, 28, 12, 0, 0, 0, chicago)},
		{"day is local midnight", BucketDay, fri, time.Date(2026, 8, 28, 0, 0, 0, 0, chicago)},
		{"week starts Monday", BucketWeek, fri, time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		{"week from a Sunday", BucketWeek, time.Date(2026, 8, 30, 23, 0, 0, 0, chicago), time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		{"week from a Monday midnight", BucketWeek, time.Date(2026, 8, 24, 0, 0, 0, 0, chicago), time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		// A half-hour offset zone: the hour is local, not a UTC hour.
		{"hour in a +05:30 zone", BucketHour, time.Date(2026, 8, 28, 13, 47, 0, 0, kolkata), time.Date(2026, 8, 28, 13, 0, 0, 0, kolkata)},
		{"day in a +05:30 zone", BucketDay, time.Date(2026, 8, 28, 2, 10, 0, 0, kolkata), time.Date(2026, 8, 28, 0, 0, 0, 0, kolkata)},
	}
	for _, c := range cases {
		if got := c.b.Floor(c.in); !got.Equal(c.want) {
			t.Errorf("%s: %q.Floor(%v) = %v, want %v", c.name, c.b, c.in, got, c.want)
		}
	}
}

func TestBucket_Next(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	start := time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)
	cases := []struct {
		name string
		b    Bucket
		in   time.Time
		want time.Time
	}{
		{"minute", BucketMinute, start, start.Add(time.Minute)},
		{"hour", BucketHour, start, start.Add(time.Hour)},
		{"6h", BucketSixHours, start, time.Date(2026, 8, 24, 6, 0, 0, 0, chicago)},
		{"day", BucketDay, start, time.Date(2026, 8, 25, 0, 0, 0, 0, chicago)},
		{"week", BucketWeek, start, time.Date(2026, 8, 31, 0, 0, 0, 0, chicago)},
		// Spring forward (2026-03-08): the local day is 23 hours long,
		// and the next bucket still starts at local midnight.
		{"day across spring forward", BucketDay, time.Date(2026, 3, 8, 0, 0, 0, 0, chicago), time.Date(2026, 3, 9, 0, 0, 0, 0, chicago)},
		// Fall back (2026-11-01): a 25-hour local day.
		{"day across fall back", BucketDay, time.Date(2026, 11, 1, 0, 0, 0, 0, chicago), time.Date(2026, 11, 2, 0, 0, 0, 0, chicago)},
	}
	for _, c := range cases {
		if got := c.b.Next(c.in); !got.Equal(c.want) {
			t.Errorf("%s: %q.Next(%v) = %v, want %v", c.name, c.b, c.in, got, c.want)
		}
	}
	if d := BucketDay.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, chicago)).Sub(time.Date(2026, 3, 8, 0, 0, 0, 0, chicago)); d != 23*time.Hour {
		t.Errorf("spring-forward day length = %v, want 23h", d)
	}
}
