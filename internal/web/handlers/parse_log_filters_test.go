package handlers_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	"github.com/bensyverson/jobs/internal/web/handlers"
)

func TestParseLogFilters_TableDriven(t *testing.T) {
	rfc3339 := "2026-04-22T10:00:00Z"
	rfcTime, _ := time.Parse(time.RFC3339, rfc3339)

	cases := []struct {
		name    string
		raw     string
		wantEq  handlers.LogFilters
		isEqual func(handlers.LogFilters, handlers.LogFilters) bool
	}{
		{
			name:   "empty query → zero filters",
			raw:    "",
			wantEq: handlers.LogFilters{},
		},
		{
			name: "actor only",
			raw:  "actor=alice",
			wantEq: handlers.LogFilters{
				Actor: "alice",
			},
		},
		{
			name: "task + type + label",
			raw:  "task=abc12&type=claimed&label=web",
			wantEq: handlers.LogFilters{
				Task:  "abc12",
				Type:  "claimed",
				Label: "web",
			},
		},
		{
			name: "since as RFC3339",
			raw:  "since=" + url.QueryEscape(rfc3339),
			wantEq: handlers.LogFilters{
				Since: rfcTime,
			},
		},
		{
			name: "since as unix seconds falls back after RFC3339 fails",
			raw:  "since=1745318400",
			wantEq: handlers.LogFilters{
				Since: time.Unix(1745318400, 0),
			},
		},
		{
			name:   "since as garbage → zero (no 400, no panic)",
			raw:    "since=not-a-time",
			wantEq: handlers.LogFilters{},
		},
		{
			name: "unknown key is ignored",
			raw:  "actor=alice&wut=huh",
			wantEq: handlers.LogFilters{
				Actor: "alice",
			},
		},
		{
			name: "repeated key keeps first value",
			raw:  "actor=alice&actor=bob",
			wantEq: handlers.LogFilters{
				Actor: "alice",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.raw)
			if err != nil {
				t.Fatalf("url.ParseQuery(%q): %v", tc.raw, err)
			}
			got := handlers.ParseLogFilters(q)

			// Compare field-by-field so a Since mismatch reports clearly.
			if got.Actor != tc.wantEq.Actor {
				t.Errorf("Actor = %q, want %q", got.Actor, tc.wantEq.Actor)
			}
			if got.Task != tc.wantEq.Task {
				t.Errorf("Task = %q, want %q", got.Task, tc.wantEq.Task)
			}
			if got.Label != tc.wantEq.Label {
				t.Errorf("Label = %q, want %q", got.Label, tc.wantEq.Label)
			}
			if got.Type != tc.wantEq.Type {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantEq.Type)
			}
			if !got.Since.Equal(tc.wantEq.Since) {
				t.Errorf("Since = %v, want %v", got.Since, tc.wantEq.Since)
			}
		})
	}
}

// TestParseLogFilters_At covers the ?at upper-bound cursor.
// Unlike since/before/limit (forgiving — malformed values silently zero),
// ?at follows the time-travel scope: a present-but-unparseable value sets
// AtInvalid so the handler can return 400. Empty / absent ?at means "no
// upper bound" (live). A bare row id is no longer a valid cursor — a
// rebuild renumbers those — so it is rejected rather than silently
// resolving to a different event.
func TestParseLogFilters_At(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantAt      eventlog.Position
		wantInvalid bool
	}{
		{name: "absent → zero, valid", raw: ""},
		{name: "empty value → zero, valid", raw: "at="},
		{
			name:   "position → set, valid",
			raw:    "at=1756742400123-k7Qx2m-412",
			wantAt: eventlog.Position{TS: 1756742400123, Rep: "k7Qx2m", Seq: 412},
		},
		{
			name:   "legacy position (empty rep) → set, valid",
			raw:    "at=1756742400000--991",
			wantAt: eventlog.Position{TS: 1756742400000, Seq: 991},
		},
		{name: "non-numeric → invalid", raw: "at=foo", wantInvalid: true},
		{name: "a bare row id is not a position → invalid", raw: "at=42", wantInvalid: true},
		{name: "zero → invalid", raw: "at=0", wantInvalid: true},
		{name: "negative → invalid", raw: "at=-1", wantInvalid: true},
		{name: "seq zero → invalid", raw: "at=1756742400123-k7Qx2m-0", wantInvalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.raw)
			if err != nil {
				t.Fatalf("url.ParseQuery(%q): %v", tc.raw, err)
			}
			got := handlers.ParseLogFilters(q)
			if got.At != tc.wantAt {
				t.Errorf("At = %+v, want %+v", got.At, tc.wantAt)
			}
			if got.AtInvalid != tc.wantInvalid {
				t.Errorf("AtInvalid = %v, want %v", got.AtInvalid, tc.wantInvalid)
			}
			if got.Live() != (tc.wantAt == eventlog.Position{}) {
				t.Errorf("Live() = %v for At = %+v", got.Live(), got.At)
			}
		})
	}
}
