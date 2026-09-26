package job

import (
	"slices"
	"testing"
	"time"
)

// rangeAnchorFixture is a fixed wall-clock moment so cutoff arithmetic is exact.
var rangeAnchorFixture = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

func TestParseRangeKey_NormalizesKnownKeys(t *testing.T) {
	cases := []struct {
		raw  string
		want RangeKey
	}{
		{"1h", RangeHour},
		{"1d", RangeDay},
		{"7d", Range7D},
		{"14d", Range14D},
		{"30d", Range30D},
		{"all", RangeAll},
		{"  30d  ", Range30D},
		{"30D", Range30D},
		{"1H", RangeHour},
		{"ALL", RangeAll},
	}
	for _, c := range cases {
		got, ok := ParseRangeKey(c.raw)
		if !ok || got != c.want {
			t.Errorf("ParseRangeKey(%q) = %q, %v; want %q, true", c.raw, got, ok, c.want)
		}
	}
}

// Unknown keys report !ok and still hand back the default, so a view
// can fall back without a second lookup and a CLI can refuse.
func TestParseRangeKey_UnknownFallsBackToDefault(t *testing.T) {
	for _, raw := range []string{"", "90d", "nonsense", "0", "2h", "1w"} {
		got, ok := ParseRangeKey(raw)
		if ok {
			t.Errorf("ParseRangeKey(%q) ok = true, want false", raw)
		}
		if got != DefaultRangeKey {
			t.Errorf("ParseRangeKey(%q) = %q, want the default %q", raw, got, DefaultRangeKey)
		}
	}
	if DefaultRangeKey != Range7D {
		t.Errorf("DefaultRangeKey = %q, want 7d", DefaultRangeKey)
	}
}

func TestRangeKeys_ShortestFirstAndAllLast(t *testing.T) {
	want := []RangeKey{RangeHour, RangeDay, Range7D, Range14D, Range30D, RangeAll}
	if got := RangeKeys(); !slices.Equal(got, want) {
		t.Errorf("RangeKeys() = %v, want %v", got, want)
	}
	// The slice is the caller's to keep; mutating it must not leak.
	RangeKeys()[0] = RangeAll
	if RangeKeys()[0] != RangeHour {
		t.Errorf("RangeKeys() returned shared backing storage")
	}
}

func TestRangeKey_Duration(t *testing.T) {
	cases := []struct {
		key     RangeKey
		want    time.Duration
		bounded bool
	}{
		{RangeHour, time.Hour, true},
		{RangeDay, 24 * time.Hour, true},
		{Range7D, 7 * 24 * time.Hour, true},
		{Range14D, 14 * 24 * time.Hour, true},
		{Range30D, 30 * 24 * time.Hour, true},
		{RangeAll, 0, false},
		{RangeKey("bogus"), 0, false},
	}
	for _, c := range cases {
		got, bounded := c.key.Duration()
		if got != c.want || bounded != c.bounded {
			t.Errorf("%q.Duration() = %v, %v; want %v, %v", c.key, got, bounded, c.want, c.bounded)
		}
	}
}

// Moved from internal/web/handlers/range_test.go
// (TestParseRange_CutoffIsAnchorMinusDuration), with 1h and 1d added.
func TestNewRange_CutoffIsAnchorMinusDuration(t *testing.T) {
	cases := []struct {
		key      RangeKey
		wantDur  time.Duration
		wantCut  int64
		hasBound bool
	}{
		{RangeHour, time.Hour, rangeAnchorFixture.Add(-time.Hour).Unix(), true},
		{RangeDay, 24 * time.Hour, rangeAnchorFixture.Add(-24 * time.Hour).Unix(), true},
		{Range7D, 7 * 24 * time.Hour, rangeAnchorFixture.Add(-7 * 24 * time.Hour).Unix(), true},
		{Range14D, 14 * 24 * time.Hour, rangeAnchorFixture.Add(-14 * 24 * time.Hour).Unix(), true},
		{Range30D, 30 * 24 * time.Hour, rangeAnchorFixture.Add(-30 * 24 * time.Hour).Unix(), true},
		{RangeAll, 0, 0, false},
	}
	for _, c := range cases {
		got := NewRange(c.key, rangeAnchorFixture)
		if got.Key != c.key {
			t.Errorf("NewRange(%q).Key = %q", c.key, got.Key)
		}
		if got.Duration != c.wantDur {
			t.Errorf("NewRange(%q).Duration = %v, want %v", c.key, got.Duration, c.wantDur)
		}
		if got.Cutoff != c.wantCut {
			t.Errorf("NewRange(%q).Cutoff = %d, want %d", c.key, got.Cutoff, c.wantCut)
		}
		if got.Bounded() != c.hasBound {
			t.Errorf("NewRange(%q).Bounded() = %v, want %v", c.key, got.Bounded(), c.hasBound)
		}
	}
}

// Moved from internal/web/handlers/range_test.go
// (TestRange_IncludesRespectsCutoff).
func TestRange_IncludesRespectsCutoff(t *testing.T) {
	rg := NewRange(Range7D, rangeAnchorFixture)
	inside := rangeAnchorFixture.Add(-6 * 24 * time.Hour).Unix()
	outside := rangeAnchorFixture.Add(-8 * 24 * time.Hour).Unix()
	if !rg.Includes(inside) {
		t.Errorf("Includes(%d) = false, want true (6 days back in a 7d window)", inside)
	}
	if rg.Includes(outside) {
		t.Errorf("Includes(%d) = true, want false (8 days back in a 7d window)", outside)
	}
	if !rg.Includes(rg.Cutoff) {
		t.Errorf("Includes(cutoff) = false, want true — the cutoff second is inside the window")
	}

	all := NewRange(RangeAll, rangeAnchorFixture)
	if !all.Includes(0) {
		t.Errorf("all.Includes(0) = false, want true — 'all' has no lower bound")
	}
}
