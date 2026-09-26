package job

import (
	"testing"
	"time"
)

// Day buckets follow the local calendar across a DST change: the spring-
// forward day is 23 hours, and an event half an hour after local midnight
// lands in the new day, not the one before.
func TestReport_DayBucketsAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	f := newReportFixture(t)
	local := func(d, h, m int) time.Time { return time.Date(2026, 3, d, h, m, 0, 0, ny) }
	f.at(local(7, 9, 0))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	f.add(root, "B")
	f.at(local(9, 0, 30)).done(a) // Monday, after the Sunday spring-forward

	r := f.report(ReportQuery{Since: local(7, 0, 0), Until: local(10, 0, 0), Bucket: BucketDay, Location: ny})
	wantEnds := []time.Time{local(8, 0, 0), local(9, 0, 0), local(10, 0, 0)}
	if len(r.Series) != len(wantEnds) {
		t.Fatalf("series ends = %v, want %v", seriesEnds(r), wantEnds)
	}
	for i, want := range wantEnds {
		if !r.Series[i].End.Equal(want) {
			t.Errorf("sample %d ends %s, want %s", i, r.Series[i].End, want)
		}
	}
	if got := r.Activity[1].End.Sub(r.Activity[1].Start); got != 23*time.Hour {
		t.Errorf("spring-forward bucket is %s long, want 23h", got)
	}
	if r.Activity[1].Done != 0 || r.Activity[2].Done != 1 {
		t.Errorf("done landed in %+v, want the Monday bucket", r.Activity)
	}
	wantSample(t, "Sunday night", r.Series[1], Sample{Scope: 2, Open: 2})
	if r.Window.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q", r.Window.Timezone)
	}
}

// Week buckets start on Monday at local midnight; a window starting mid-week
// clips the first bucket to Since.
func TestReport_WeekBucketsStartMonday(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	f := newReportFixture(t)
	wed := time.Date(2026, 3, 4, 12, 0, 0, 0, ny)
	mon := time.Date(2026, 3, 9, 0, 0, 0, 0, ny)
	nextMon := time.Date(2026, 3, 16, 0, 0, 0, 0, ny)
	r := f.report(ReportQuery{Since: wed, Until: nextMon, Bucket: BucketWeek, Location: ny})
	if len(r.Series) != 2 || !r.Series[0].End.Equal(mon) || !r.Series[1].End.Equal(nextMon) {
		t.Fatalf("series ends = %v, want [%s %s]", seriesEnds(r), mon, nextMon)
	}
	if !r.Activity[0].Start.Equal(wed) || !r.Activity[1].Start.Equal(mon) {
		t.Errorf("activity starts = %s, %s; want %s (clipped) and %s", r.Activity[0].Start, r.Activity[1].Start, wed, mon)
	}
}

// The last sample ends at Until even when Until is mid-bucket.
func TestReport_LastSampleEndsAtUntil(t *testing.T) {
	f := newReportFixture(t)
	until := day(2, 12*time.Hour)
	r := f.report(ReportQuery{Since: day(0), Until: until, Bucket: BucketDay})
	want := []time.Time{day(1), day(2), until}
	if len(r.Series) != 3 || !r.Series[2].End.Equal(until) || !r.Series[1].End.Equal(want[1]) {
		t.Errorf("series ends = %v, want %v", seriesEnds(r), want)
	}
	if !r.Window.Until.Equal(until) {
		t.Errorf("Window.Until = %s, want %s", r.Window.Until, until)
	}
}

// With no Since the window starts at the first event in scope, and with no
// Until it ends now.
func TestReport_DefaultsSinceToFirstEventAndUntilToNow(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, 5*time.Hour))
	f.add("", "Plan")
	f.at(day(3, time.Hour))
	r := f.report(ReportQuery{})
	// The log's clock ticks a millisecond per event within one write.
	if !r.Window.Since.Truncate(time.Second).Equal(day(0, 5*time.Hour)) {
		t.Errorf("Window.Since = %s, want the first event %s", r.Window.Since, day(0, 5*time.Hour))
	}
	if !r.Window.Until.Equal(day(3, time.Hour)) {
		t.Errorf("Window.Until = %s, want now %s", r.Window.Until, day(3, time.Hour))
	}
	if r.Window.Bucket != BucketDay {
		t.Errorf("Window.Bucket = %q, want day — all history under 90 days", r.Window.Bucket)
	}
}

// With no Bucket the choice agrees with BucketFor for each named range key: a
// zero Since is RangeAll, and a bounded window buckets by its span.
func TestReport_AutomaticBucketAgreesWithBucketFor(t *testing.T) {
	f := newReportFixture(t)
	until := day(200)
	for _, key := range []RangeKey{RangeHour, RangeDay, Range7D, Range14D, Range30D} {
		d, _ := key.Duration()
		r := f.report(ReportQuery{Since: until.Add(-d), Until: until})
		if want := BucketFor(key, 0); r.Window.Bucket != want {
			t.Errorf("%s: bucket = %q, want %q", key, r.Window.Bucket, want)
		}
	}
	// All history: day up to 90 days, week past it.
	f.at(day(0)).add("", "first")
	if r := f.report(ReportQuery{Until: day(89)}); r.Window.Bucket != BucketDay {
		t.Errorf("all over 89 days: bucket = %q, want day", r.Window.Bucket)
	}
	if r := f.report(ReportQuery{Until: day(91)}); r.Window.Bucket != BucketWeek {
		t.Errorf("all over 91 days: bucket = %q, want week", r.Window.Bucket)
	}
	// A short all-history window still buckets by day, as BucketFor says.
	if r := f.report(ReportQuery{Until: day(3)}); r.Window.Bucket != BucketDay {
		t.Errorf("all over 3 days: bucket = %q, want day", r.Window.Bucket)
	}
}

// Arbitrary spans pick the unit of the nearest named window.
func TestReport_AutomaticBucketForArbitrarySpans(t *testing.T) {
	f := newReportFixture(t)
	until := day(400)
	cases := []struct {
		span time.Duration
		want Bucket
	}{
		{90 * time.Minute, BucketMinute},
		{6 * time.Hour, BucketHour},
		{3 * 24 * time.Hour, BucketSixHours},
		{60 * 24 * time.Hour, BucketDay},
		{120 * 24 * time.Hour, BucketWeek},
	}
	for _, c := range cases {
		r := f.report(ReportQuery{Since: until.Add(-c.span), Until: until})
		if r.Window.Bucket != c.want {
			t.Errorf("span %s: bucket = %q, want %q", c.span, r.Window.Bucket, c.want)
		}
	}
}

// The local zone is reported by its IANA name, not Go's "Local", so a
// reader of the JSON on another machine knows which calendar the buckets
// follow.
func TestReport_LocalTimezoneIsNamed(t *testing.T) {
	t.Setenv("TZ", "Europe/Paris")
	// time.Local reads TZ once per process, so the test stands one in: Go
	// names the zone it loads from /etc/localtime "Local".
	if got := locationName(time.FixedZone("Local", 3600)); got != "Europe/Paris" {
		t.Errorf("locationName(Local) = %q, want the TZ name", got)
	}
	if got := locationName(time.UTC); got != "UTC" {
		t.Errorf("locationName(UTC) = %q", got)
	}
}

func TestReport_RejectsBadWindows(t *testing.T) {
	f := newReportFixture(t)
	if _, err := BuildReport(f.db, ReportQuery{Since: day(2), Until: day(1)}); err == nil {
		t.Error("Since after Until: want an error")
	}
	if _, err := BuildReport(f.db, ReportQuery{Since: day(0), Until: day(1), Bucket: "fortnight"}); err == nil {
		t.Error("unknown bucket: want an error")
	}
}
