package job

import (
	"strings"
	"testing"
	"time"
)

func TestRenderUsage_AllTimeForest_FullPopulation(t *testing.T) {
	u := &Usage{
		WindowKind: "all-time",
		Open:       5, Claimed: 2, Done: 378, Canceled: 17, Blocked: 3,
		EventCount:              1204,
		FirstEventAt:            time.Unix(1_700_000_000-(118*86400), 0).Unix(),
		LastEventAt:             time.Unix(1_700_000_000-(17*86400), 0).Unix(),
		DoneAllEvents:           378,
		VelocityRate:            3.2,
		VelocityDenominatorDays: 118,
		DBFileSizeBytes:         412 * 1024,
	}
	var sb strings.Builder
	RenderUsage(&sb, u)
	out := sb.String()

	if !strings.Contains(out, "Usage (all-time)") {
		t.Errorf("header missing; got:\n%s", out)
	}
	for _, mustContain := range []string{
		"open 5",
		"claimed 2",
		"done 378",
		"canceled 17",
		"blocked 3",
		"completion",
		"cancellation",
		"events 1,204",
		"velocity 3.2/day",
		"(over 118d)",
		"db 412 KB",
	} {
		if !strings.Contains(out, mustContain) {
			t.Errorf("output missing %q; got:\n%s", mustContain, out)
		}
	}
}

func TestRenderUsage_ZeroCountsOmittedFromStatusRow(t *testing.T) {
	u := &Usage{
		WindowKind:              "all-time",
		Open:                    5,
		Done:                    378,
		Canceled:                17,
		EventCount:              80,
		FirstEventAt:            time.Unix(1_700_000_000-(30*86400), 0).Unix(),
		LastEventAt:             time.Unix(1_700_000_000, 0).Unix(),
		DoneAllEvents:           378,
		VelocityRate:            12.6,
		VelocityDenominatorDays: 30,
		DBFileSizeBytes:         1024,
	}
	var sb strings.Builder
	RenderUsage(&sb, u)
	out := sb.String()

	for _, should := range []string{"open 5", "done 378", "canceled 17"} {
		if !strings.Contains(out, should) {
			t.Errorf("expected %q; got:\n%s", should, out)
		}
	}
	for _, shouldNot := range []string{"claimed 0", "blocked 0", "canceled 0"} {
		if strings.Contains(out, shouldNot) {
			t.Errorf("zero count should be omitted, found %q; got:\n%s", shouldNot, out)
		}
	}
}

func TestRenderUsage_WindowedHeaderReflectsWindow(t *testing.T) {
	u := &Usage{
		WindowKind:              "windowed",
		WindowDays:              7,
		Open:                    1,
		Done:                    10,
		EventCount:              42,
		FirstEventAt:            time.Unix(1_700_000_000, 0).Unix(),
		LastEventAt:             time.Unix(1_700_000_000, 0).Unix(),
		DoneInWindow:            2,
		VelocityRate:            2.0 / 7.0,
		VelocityDenominatorDays: 7,
		DBFileSizeBytes:         1024,
	}
	var sb strings.Builder
	RenderUsage(&sb, u)
	out := sb.String()

	if !strings.Contains(out, "Usage (last 7d)") {
		t.Errorf("windowed header missing; got:\n%s", out)
	}
}

func TestRenderUsage_SubtreeScopedNoPreamble(t *testing.T) {
	u := &Usage{
		ScopeID:      new(int64(42)),
		ScopeShortID: "PGkzI",
		WindowKind:   "all-time",
		Open:         2, Done: 4,
		EventCount:              10,
		FirstEventAt:            time.Unix(1_700_000_000, 0).Unix(),
		LastEventAt:             time.Unix(1_700_000_000, 0).Unix(),
		VelocityRate:            1.0,
		VelocityDenominatorDays: 1,
		DBFileSizeBytes:         1024,
	}
	var sb strings.Builder
	RenderUsage(&sb, u)
	out := sb.String()
	if !strings.Contains(out, "Usage (all-time)") {
		t.Errorf("header missing; got:\n%s", out)
	}
	// Identity/preamble lines belong to the DB-wide briefing, not a
	// subtree report.
	for _, shouldNot := range []string{"Identity:", "strict mode"} {
		if strings.Contains(out, shouldNot) {
			t.Errorf("subtree scope should not include %q; got:\n%s", shouldNot, out)
		}
	}
}

//go:fix inline
func idPtr(i int64) *int64 { return new(i) }
