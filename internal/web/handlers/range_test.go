package handlers

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	job "github.com/bensyverson/jobs/internal/job"
)

// anchor is a fixed wall-clock moment so cutoff arithmetic is exact.
var rangeAnchorFixture = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

func TestParseRange_NormalizesKey(t *testing.T) {
	cases := []struct {
		raw  string
		want RangeKey
	}{
		{"", Range7D},
		{"7d", Range7D},
		{"14d", Range14D},
		{"30d", Range30D},
		{"all", RangeAll},
		{"  30d  ", Range30D},
		{"30D", Range30D},
		{"ALL", RangeAll},
		{"90d", Range7D},
		{"nonsense", Range7D},
		{"0", Range7D},
	}
	for _, c := range cases {
		q := url.Values{}
		if c.raw != "" {
			q.Set("range", c.raw)
		}
		got := parseRange(q, rangeAnchorFixture)
		if got.Key != c.want {
			t.Errorf("parseRange(range=%q).Key = %q, want %q", c.raw, got.Key, c.want)
		}
	}
}

func TestParseRange_CutoffIsAnchorMinusDuration(t *testing.T) {
	cases := []struct {
		raw      string
		wantDur  time.Duration
		wantCut  int64
		hasBound bool
	}{
		{"", 7 * 24 * time.Hour, rangeAnchorFixture.Add(-7 * 24 * time.Hour).Unix(), true},
		{"14d", 14 * 24 * time.Hour, rangeAnchorFixture.Add(-14 * 24 * time.Hour).Unix(), true},
		{"30d", 30 * 24 * time.Hour, rangeAnchorFixture.Add(-30 * 24 * time.Hour).Unix(), true},
		{"all", 0, 0, false},
	}
	for _, c := range cases {
		q := url.Values{}
		if c.raw != "" {
			q.Set("range", c.raw)
		}
		got := parseRange(q, rangeAnchorFixture)
		if got.Duration != c.wantDur {
			t.Errorf("parseRange(range=%q).Duration = %v, want %v", c.raw, got.Duration, c.wantDur)
		}
		if got.Cutoff != c.wantCut {
			t.Errorf("parseRange(range=%q).Cutoff = %d, want %d", c.raw, got.Cutoff, c.wantCut)
		}
		if got.Bounded() != c.hasBound {
			t.Errorf("parseRange(range=%q).Bounded() = %v, want %v", c.raw, got.Bounded(), c.hasBound)
		}
	}
}

func TestRange_IncludesRespectsCutoff(t *testing.T) {
	rg := parseRange(url.Values{"range": {"7d"}}, rangeAnchorFixture)
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

	all := parseRange(url.Values{"range": {"all"}}, rangeAnchorFixture)
	if !all.Includes(0) {
		t.Errorf("all.Includes(0) = false, want true — 'all' has no lower bound")
	}
}

func TestBuildRangeTabs_LabelsActiveAndURLs(t *testing.T) {
	tabs := buildRangeTabs("/actors", url.Values{}, Range7D)
	if len(tabs) != 4 {
		t.Fatalf("buildRangeTabs: got %d tabs, want 4", len(tabs))
	}
	wantLabels := []string{"7D", "14D", "30D", "All"}
	wantURLs := []string{"/actors", "/actors?range=14d", "/actors?range=30d", "/actors?range=all"}
	for i, tab := range tabs {
		if tab.Label != wantLabels[i] {
			t.Errorf("tab %d label = %q, want %q", i, tab.Label, wantLabels[i])
		}
		if tab.URL != wantURLs[i] {
			t.Errorf("tab %d URL = %q, want %q", i, tab.URL, wantURLs[i])
		}
	}
	if !tabs[0].Active {
		t.Errorf("7D tab should be active when the range is 7d")
	}
	for _, tab := range tabs[1:] {
		if tab.Active {
			t.Errorf("tab %q should not be active when the range is 7d", tab.Label)
		}
	}
}

func TestBuildRangeTabs_MarksTheSelectedRange(t *testing.T) {
	tabs := buildRangeTabs("/actors", url.Values{"range": {"all"}}, RangeAll)
	active := ""
	for _, tab := range tabs {
		if tab.Active {
			if active != "" {
				t.Fatalf("more than one active tab: %q and %q", active, tab.Label)
			}
			active = tab.Label
		}
	}
	if active != "All" {
		t.Errorf("active tab = %q, want %q", active, "All")
	}
}

func TestBuildRangeTabs_PreservesOtherQueryParams(t *testing.T) {
	q := url.Values{"at": {"42"}, "range": {"30d"}}
	tabs := buildRangeTabs("/log", q, Range30D)
	for _, tab := range tabs {
		u, err := url.Parse(tab.URL)
		if err != nil {
			t.Fatalf("parse %q: %v", tab.URL, err)
		}
		if u.Path != "/log" {
			t.Errorf("tab %q path = %q, want /log", tab.Label, u.Path)
		}
		if got := u.Query().Get("at"); got != "42" {
			t.Errorf("tab %q dropped ?at (got %q)", tab.Label, got)
		}
	}
	// The default range is expressed by omitting the parameter, so a
	// bookmark of the default view stays clean.
	u, _ := url.Parse(tabs[0].URL)
	if _, ok := u.Query()["range"]; ok {
		t.Errorf("7D tab should omit ?range=, got %q", tabs[0].URL)
	}
}

// rangeAnchor pins the window to the moment the scrubber is parked at,
// so ?at=<event id> measures the range back from that event rather
// than from wall-clock now.
func TestRangeAnchor_UsesTheCursorEventTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchor.db")
	db, err := job.CreateDB(path)
	if err != nil {
		t.Fatalf("CreateDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := job.RunAdd(db, "", "anchored", "", "", nil, "alice"); err != nil {
		t.Fatalf("RunAdd: %v", err)
	}
	events, err := job.GetEventsForTaskTree(db, "")
	if err != nil || len(events) == 0 {
		t.Fatalf("seed events: %v / %d", err, len(events))
	}
	at := events[0].Position()
	want := rangeAnchorFixture.Add(-3 * 24 * time.Hour).Unix()
	if _, err := db.Exec(`UPDATE events SET created_at = ? WHERE id = ?`, want, events[0].ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	now := rangeAnchorFixture
	got, err := rangeAnchor(context.Background(), db, at, now)
	if err != nil {
		t.Fatalf("rangeAnchor: %v", err)
	}
	if got.Unix() != want {
		t.Errorf("rangeAnchor(at=%s) = %d, want %d", at, got.Unix(), want)
	}

	live, err := rangeAnchor(context.Background(), db, eventlog.Position{}, now)
	if err != nil {
		t.Fatalf("rangeAnchor(live): %v", err)
	}
	if !live.Equal(now) {
		t.Errorf("rangeAnchor(live) = %v, want now (%v)", live, now)
	}
}
