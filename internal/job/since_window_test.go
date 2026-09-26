package job

import (
	"strings"
	"testing"
	"time"
)

var windowNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func TestParseWindowStart(t *testing.T) {
	cases := map[string]time.Time{
		"":                     {},
		"all":                  {},
		"ALL":                  {},
		"7d":                   windowNow.Add(-7 * 24 * time.Hour),
		"14D":                  windowNow.Add(-14 * 24 * time.Hour),
		"1h":                   windowNow.Add(-time.Hour),
		"90m":                  windowNow.Add(-90 * time.Minute),
		"3d":                   windowNow.Add(-3 * 24 * time.Hour),
		"2026-09-01T00:00:00Z": time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	for raw, want := range cases {
		got, err := ParseWindowStart(raw, windowNow)
		if err != nil {
			t.Errorf("ParseWindowStart(%q): %v", raw, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("ParseWindowStart(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseWindowStart_RefusesUnknown(t *testing.T) {
	for _, raw := range []string{"fortnight", "7x", "yesterday"} {
		_, err := ParseWindowStart(raw, windowNow)
		if err == nil {
			t.Errorf("ParseWindowStart(%q) should refuse", raw)
			continue
		}
		if !strings.Contains(err.Error(), "--since") || !strings.Contains(err.Error(), "all") {
			t.Errorf("error should name the flag and the range keys: %v", err)
		}
	}
}

func TestParseWindowEnd(t *testing.T) {
	cases := map[string]time.Time{
		"":                     {},
		"1d":                   windowNow.Add(-24 * time.Hour),
		"2026-09-20T00:00:00Z": time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC),
	}
	for raw, want := range cases {
		got, err := ParseWindowEnd(raw, windowNow)
		if err != nil {
			t.Errorf("ParseWindowEnd(%q): %v", raw, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("ParseWindowEnd(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseWindowEnd_RefusesAllAndUnknown(t *testing.T) {
	for _, raw := range []string{"all", "fortnight"} {
		_, err := ParseWindowEnd(raw, windowNow)
		if err == nil {
			t.Errorf("ParseWindowEnd(%q) should refuse", raw)
			continue
		}
		if !strings.Contains(err.Error(), "--until") {
			t.Errorf("error should name the flag: %v", err)
		}
	}
}
