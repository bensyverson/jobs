package job

import (
	"testing"
	"time"
)

// Decision 2 of project/2026-09-27-chart-panel-revision.md, one case per key.
func TestBucketFor_PerKey(t *testing.T) {
	const day = 24 * time.Hour
	cases := []struct {
		name    string
		key     RangeKey
		history time.Duration
		want    Bucket
	}{
		{"1h", RangeHour, 0, BucketFiveMinutes},
		{"1d", RangeDay, 0, BucketHour},
		{"7d", Range7D, 0, BucketSixHours},
		{"14d", Range14D, 0, BucketTwelveHours},
		{"30d", Range30D, 0, BucketDay},
		{"all, empty history", RangeAll, 0, BucketMinute},
		{"all, ten minutes of history", RangeAll, 10 * time.Minute, BucketMinute},
		{"all, an hour of history", RangeAll, time.Hour, BucketFiveMinutes},
		{"all, a day of history", RangeAll, 24 * time.Hour, BucketHour},
		{"all, seven days of history", RangeAll, 7 * day, BucketSixHours},
		{"all, fourteen days of history", RangeAll, 14 * day, BucketTwelveHours},
		{"all, 30 days", RangeAll, 30 * day, BucketDay},
		{"all, 45 days", RangeAll, 45 * day, BucketDay},
		{"all, just past 45 days", RangeAll, 45*day + time.Second, BucketWeek},
		{"all, half a year", RangeAll, 182 * day, BucketWeek},
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

// Each named range key's window, laid out in its bucket up to a
// calendar-aligned Until, draws the bar count decision 2 names.
func TestBucketFor_BarCountPerKey(t *testing.T) {
	until := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		key  RangeKey
		bars int
	}{
		{RangeHour, 12},
		{RangeDay, 24},
		{Range7D, 28},
		{Range14D, 28},
		{Range30D, 30},
	}
	for _, c := range cases {
		t.Run(string(c.key), func(t *testing.T) {
			d, _ := c.key.Duration()
			w, err := resolveBuckets(until.Add(-d), until, BucketFor(c.key, 0))
			if err != nil {
				t.Fatalf("resolveBuckets: %v", err)
			}
			if got := len(w.ends); got != c.bars {
				t.Errorf("%s: %d bars of %q, want %d", c.key, got, w.bucket, c.bars)
			}
		})
	}
}

// All-history windows land about 25–30 bars wherever a calendar unit allows
// it, across short, medium and long histories.
func TestBucketFor_AllLandsAboutThirtyBars(t *testing.T) {
	const day = 24 * time.Hour
	// A Monday midnight, so week buckets align with Until.
	until := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	for _, history := range []time.Duration{
		2 * time.Hour, 26 * time.Hour, 6 * day, 13 * day, 28 * day, 26 * 7 * day, 29 * 7 * day,
	} {
		w, err := resolveBuckets(until.Add(-history), until, BucketFor(RangeAll, history))
		if err != nil {
			t.Fatalf("resolveBuckets: %v", err)
		}
		if n := len(w.ends); n < 24 || n > 30 {
			t.Errorf("all over %v: %d bars of %q, want about 25–30", history, n, w.bucket)
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
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
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
		{"5m", BucketFiveMinutes, fri, time.Date(2026, 8, 28, 13, 45, 0, 0, chicago)},
		{"5m on its boundary", BucketFiveMinutes, time.Date(2026, 8, 28, 13, 50, 0, 0, chicago), time.Date(2026, 8, 28, 13, 50, 0, 0, chicago)},
		{"hour", BucketHour, fri, time.Date(2026, 8, 28, 13, 0, 0, 0, chicago)},
		{"6h", BucketSixHours, fri, time.Date(2026, 8, 28, 12, 0, 0, 0, chicago)},
		{"12h afternoon", BucketTwelveHours, fri, time.Date(2026, 8, 28, 12, 0, 0, 0, chicago)},
		{"12h morning", BucketTwelveHours, time.Date(2026, 8, 28, 11, 59, 0, 0, chicago), time.Date(2026, 8, 28, 0, 0, 0, 0, chicago)},
		{"day is local midnight", BucketDay, fri, time.Date(2026, 8, 28, 0, 0, 0, 0, chicago)},
		{"week starts Monday", BucketWeek, fri, time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		{"week from a Sunday", BucketWeek, time.Date(2026, 8, 30, 23, 0, 0, 0, chicago), time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		{"week from a Monday midnight", BucketWeek, time.Date(2026, 8, 24, 0, 0, 0, 0, chicago), time.Date(2026, 8, 24, 0, 0, 0, 0, chicago)},
		// A half-hour offset zone: the hour is local, not a UTC hour.
		{"hour in a +05:30 zone", BucketHour, time.Date(2026, 8, 28, 13, 47, 0, 0, kolkata), time.Date(2026, 8, 28, 13, 0, 0, 0, kolkata)},
		{"day in a +05:30 zone", BucketDay, time.Date(2026, 8, 28, 2, 10, 0, 0, kolkata), time.Date(2026, 8, 28, 0, 0, 0, 0, kolkata)},
		{"12h in a +05:30 zone", BucketTwelveHours, time.Date(2026, 8, 28, 13, 47, 0, 0, kolkata), time.Date(2026, 8, 28, 12, 0, 0, 0, kolkata)},
		{"5m in a +05:45 zone", BucketFiveMinutes, time.Date(2026, 8, 28, 13, 47, 0, 0, kathmandu), time.Date(2026, 8, 28, 13, 45, 0, 0, kathmandu)},
		// Fall back (2026-11-01): 01:30 CST, the second 01:30 of the night,
		// is still in the bucket that started at local midnight.
		{"12h across fall back", BucketTwelveHours, time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC).In(chicago), time.Date(2026, 11, 1, 0, 0, 0, 0, chicago)},
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
		{"5m", BucketFiveMinutes, start, start.Add(5 * time.Minute)},
		{"hour", BucketHour, start, start.Add(time.Hour)},
		{"6h", BucketSixHours, start, time.Date(2026, 8, 24, 6, 0, 0, 0, chicago)},
		{"12h", BucketTwelveHours, start, time.Date(2026, 8, 24, 12, 0, 0, 0, chicago)},
		{"12h from noon is the next midnight", BucketTwelveHours, time.Date(2026, 8, 24, 12, 0, 0, 0, chicago), time.Date(2026, 8, 25, 0, 0, 0, 0, chicago)},
		{"day", BucketDay, start, time.Date(2026, 8, 25, 0, 0, 0, 0, chicago)},
		{"week", BucketWeek, start, time.Date(2026, 8, 31, 0, 0, 0, 0, chicago)},
		// Spring forward (2026-03-08): the local day is 23 hours long,
		// and the next bucket still starts at local midnight.
		{"day across spring forward", BucketDay, time.Date(2026, 3, 8, 0, 0, 0, 0, chicago), time.Date(2026, 3, 9, 0, 0, 0, 0, chicago)},
		// Fall back (2026-11-01): a 25-hour local day.
		{"day across fall back", BucketDay, time.Date(2026, 11, 1, 0, 0, 0, 0, chicago), time.Date(2026, 11, 2, 0, 0, 0, 0, chicago)},
		// 01:55 CST is five minutes before 03:00 CDT.
		{"5m across spring forward", BucketFiveMinutes, time.Date(2026, 3, 8, 1, 55, 0, 0, chicago), time.Date(2026, 3, 8, 3, 0, 0, 0, chicago)},
		// The spring-forward morning is 11 hours and still ends at local noon;
		// the fall-back morning is 13.
		{"12h across spring forward", BucketTwelveHours, time.Date(2026, 3, 8, 0, 0, 0, 0, chicago), time.Date(2026, 3, 8, 12, 0, 0, 0, chicago)},
		{"12h across fall back", BucketTwelveHours, time.Date(2026, 11, 1, 0, 0, 0, 0, chicago), time.Date(2026, 11, 1, 12, 0, 0, 0, chicago)},
	}
	for _, c := range cases {
		if got := c.b.Next(c.in); !got.Equal(c.want) {
			t.Errorf("%s: %q.Next(%v) = %v, want %v", c.name, c.b, c.in, got, c.want)
		}
	}
	if d := BucketDay.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, chicago)).Sub(time.Date(2026, 3, 8, 0, 0, 0, 0, chicago)); d != 23*time.Hour {
		t.Errorf("spring-forward day length = %v, want 23h", d)
	}
	if d := BucketTwelveHours.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, chicago)).Sub(time.Date(2026, 11, 1, 0, 0, 0, 0, chicago)); d != 13*time.Hour {
		t.Errorf("fall-back morning length = %v, want 13h", d)
	}
}
