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

// The Actors board and the Log offer 7D · 14D · 30D · All. The core
// vocabulary also knows 1h and 1d, but a view that does not offer a
// key treats it as unknown and falls back to the default.
func TestParseRange_BoundedViewsNormalizeTheirOwnKeys(t *testing.T) {
	cases := []struct {
		raw  string
		want job.RangeKey
	}{
		{"", job.Range7D},
		{"7d", job.Range7D},
		{"14d", job.Range14D},
		{"30d", job.Range30D},
		{"all", job.RangeAll},
		{"  30d  ", job.Range30D},
		{"30D", job.Range30D},
		{"ALL", job.RangeAll},
		{"90d", job.Range7D},
		{"nonsense", job.Range7D},
		{"0", job.Range7D},
		{"1h", job.Range7D},
		{"1d", job.Range7D},
		{"1H", job.Range7D},
	}
	for _, c := range cases {
		q := url.Values{}
		if c.raw != "" {
			q.Set("range", c.raw)
		}
		got := parseRange(q, rangeAnchorFixture, boundedViewRanges)
		if got.Key != c.want {
			t.Errorf("parseRange(range=%q).Key = %q, want %q", c.raw, got.Key, c.want)
		}
	}
}

// A view that does offer 1h gets it, measured back from the anchor.
func TestParseRange_OfferedKeysAnchorTheWindow(t *testing.T) {
	offered := []rangeOption{{job.RangeHour, "1H"}, {job.Range7D, "7D"}}
	got := parseRange(url.Values{"range": {"1h"}}, rangeAnchorFixture, offered)
	if got.Key != job.RangeHour {
		t.Fatalf("parseRange(range=1h).Key = %q, want 1h", got.Key)
	}
	if want := rangeAnchorFixture.Add(-time.Hour).Unix(); got.Cutoff != want {
		t.Errorf("parseRange(range=1h).Cutoff = %d, want %d", got.Cutoff, want)
	}
	if def := parseRange(url.Values{}, rangeAnchorFixture, offered); def.Key != job.DefaultRangeKey {
		t.Errorf("parseRange(no range).Key = %q, want the default", def.Key)
	}
}

// The bounded views' list is exactly the four tabs they have always
// shown, and every offered key is a real core key.
func TestBoundedViewRanges_AreTheFourTabs(t *testing.T) {
	want := []job.RangeKey{job.Range7D, job.Range14D, job.Range30D, job.RangeAll}
	if len(boundedViewRanges) != len(want) {
		t.Fatalf("boundedViewRanges has %d options, want %d", len(boundedViewRanges), len(want))
	}
	for i, opt := range boundedViewRanges {
		if opt.Key != want[i] {
			t.Errorf("option %d = %q, want %q", i, opt.Key, want[i])
		}
		if _, ok := job.ParseRangeKey(string(opt.Key)); !ok {
			t.Errorf("option %q is not a core range key", opt.Key)
		}
	}
}

func TestBuildRangeTabs_LabelsActiveAndURLs(t *testing.T) {
	tabs := buildRangeTabs("/actors", url.Values{}, job.Range7D, boundedViewRanges)
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
	tabs := buildRangeTabs("/actors", url.Values{"range": {"all"}}, job.RangeAll, boundedViewRanges)
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
	tabs := buildRangeTabs("/log", q, job.Range30D, boundedViewRanges)
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
