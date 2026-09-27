package job

import (
	"slices"
	"testing"
)

func TestParseBucket(t *testing.T) {
	for raw, want := range map[string]Bucket{"minute": BucketMinute, "5m": BucketFiveMinutes, "12H": BucketTwelveHours, "hour": BucketHour, "6h": BucketSixHours, " Day ": BucketDay, "week": BucketWeek} {
		if got, ok := ParseBucket(raw); !ok || got != want {
			t.Errorf("ParseBucket(%q) = %q, %v; want %q, true", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "fortnight", "1d"} {
		if _, ok := ParseBucket(raw); ok {
			t.Errorf("ParseBucket(%q) should refuse", raw)
		}
	}
}

func TestBuckets_ListsEveryBucketNarrowestFirst(t *testing.T) {
	want := []Bucket{BucketMinute, BucketFiveMinutes, BucketHour, BucketSixHours, BucketTwelveHours, BucketDay, BucketWeek}
	if got := Buckets(); !slices.Equal(got, want) {
		t.Errorf("Buckets() = %v, want %v", got, want)
	}
}
