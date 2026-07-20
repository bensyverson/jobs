package job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestUsageJSON_AllStatusCountsIncludingZeros(t *testing.T) {
	u := &Usage{
		WindowKind: "all-time",
		Open:       5, Claimed: 0, Done: 378, Canceled: 17, Blocked: 0,
		EventCount:              12,
		FirstEventAt:            time.Unix(1_700_000_000, 0).Unix(),
		LastEventAt:             time.Unix(1_710_000_000, 0).Unix(),
		VelocityRate:            3.2,
		VelocityDenominatorDays: 100,
		WindowDays:              100,
		DBFileSizeBytes:         4096,
	}
	b, err := json.Marshal(usageJSON(u))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"open":5`) {
		t.Errorf("open missing; got %s", s)
	}
	if !strings.Contains(s, `"claimed":0`) {
		t.Errorf("claimed zero must be present; got %s", s)
	}
	if !strings.Contains(s, `"blocked":0`) {
		t.Errorf("blocked zero must be present; got %s", s)
	}
	if !strings.Contains(s, `"canceled":17`) {
		t.Errorf("canceled missing; got %s", s)
	}
}

func TestUsageJSON_VelocityObjectExposesRateAndWindow(t *testing.T) {
	u := &Usage{
		WindowKind:              "windowed",
		WindowDays:              30,
		VelocityRate:            1.7,
		VelocityDenominatorDays: 30,
		DoneInWindow:            5,
	}
	b, err := json.Marshal(usageJSON(u))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"velocity":{`) {
		t.Errorf("velocity object missing; got %s", s)
	}
	if !strings.Contains(s, `"rate":1.7`) {
		t.Errorf("rate missing or wrong; got %s", s)
	}
	if !strings.Contains(s, `"denominator_days":30`) {
		t.Errorf("denominator_days missing; got %s", s)
	}
	if !strings.Contains(s, `"window":"windowed"`) {
		t.Errorf("window kind missing; got %s", s)
	}
}

func TestUsageJSON_EventSpanBothUnixAndISO(t *testing.T) {
	u := &Usage{
		FirstEventAt: 1_700_000_000,
		LastEventAt:  1_710_000_000,
		EventCount:   5,
	}
	b, err := json.Marshal(usageJSON(u))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"first_event_unix":1700000000`) {
		t.Errorf("first_event_unix missing; got %s", s)
	}
	if !strings.Contains(s, `"last_event_unix":1710000000`) {
		t.Errorf("last_event_unix missing; got %s", s)
	}
	if !strings.Contains(s, `"first_event_iso":"`) {
		t.Errorf("first_event_iso missing; got %s", s)
	}
	if !strings.Contains(s, `"last_event_iso":"`) {
		t.Errorf("last_event_iso missing; got %s", s)
	}
}
