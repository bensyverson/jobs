package job

import (
	"encoding/json"
	"time"
)

// usageJSONOutput is the machine-readable shape for `job status --usage
// --format json`. Mirrors Usage fields verbatim, plus derived fields
// (iso timestamps) the human renderer formats on the fly.
type usageJSONOutput struct {
	Scope      *int64 `json:"scope_task_id,omitempty"`
	ScopeShort string `json:"scope_short_id,omitempty"`

	Counts struct {
		Open     int `json:"open"`
		Claimed  int `json:"claimed"`
		Done     int `json:"done"`
		Canceled int `json:"canceled"`
		Blocked  int `json:"blocked"`
	} `json:"counts"`

	Rates struct {
		Completion   *int `json:"completion_pct,omitempty"`
		Cancellation *int `json:"cancellation_pct,omitempty"`
	} `json:"rates"`

	Activity struct {
		Events int64 `json:"event_count"`

		FirstEventUnix int64  `json:"first_event_unix"`
		FirstEventISO  string `json:"first_event_iso"`
		LastEventUnix  int64  `json:"last_event_unix"`
		LastEventISO   string `json:"last_event_iso"`

		DoneAllEvents int `json:"done_events_all_time"`
		DoneInWindow  int `json:"done_events_in_window"`
	} `json:"activity"`

	Velocity struct {
		Rate            float64 `json:"rate"`
		DenominatorDays float64 `json:"denominator_days"`
		Window          string  `json:"window"`
		WindowDays      float64 `json:"window_days,omitempty"`
	} `json:"velocity"`

	DBFileSizeBytes int64 `json:"db_file_size_bytes"`
}

// usageJSON projects a Usage into the JSON shape. Tests target this
// directly; cmd/job/status.go calls MarshalUsageJSON for the CLI.
func usageJSON(u *Usage) *usageJSONOutput {
	out := &usageJSONOutput{
		Scope:      u.ScopeID,
		ScopeShort: u.ScopeShortID,
	}
	out.Counts.Open = u.Open
	out.Counts.Claimed = u.Claimed
	out.Counts.Done = u.Done
	out.Counts.Canceled = u.Canceled
	out.Counts.Blocked = u.Blocked

	totalClosed := u.Done + u.Canceled
	if totalClosed > 0 {
		c := percent(u.Done, totalClosed)
		out.Rates.Completion = &c
		if u.Canceled > 0 {
			cn := percent(u.Canceled, totalClosed)
			out.Rates.Cancellation = &cn
		}
	}

	out.Activity.Events = u.EventCount
	out.Activity.FirstEventUnix = u.FirstEventAt
	out.Activity.FirstEventISO = unixToISO(u.FirstEventAt)
	out.Activity.LastEventUnix = u.LastEventAt
	out.Activity.LastEventISO = unixToISO(u.LastEventAt)
	out.Activity.DoneAllEvents = u.DoneAllEvents
	out.Activity.DoneInWindow = u.DoneInWindow

	out.Velocity.Rate = u.VelocityRate
	out.Velocity.DenominatorDays = u.VelocityDenominatorDays
	out.Velocity.Window = u.WindowKind
	out.Velocity.WindowDays = u.WindowDays

	out.DBFileSizeBytes = u.DBFileSizeBytes
	return out
}

// MarshalUsageJSON returns indented JSON for the CLI's --format json.
func MarshalUsageJSON(u *Usage) ([]byte, error) {
	return json.MarshalIndent(usageJSON(u), "", "  ")
}

func unixToISO(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}
