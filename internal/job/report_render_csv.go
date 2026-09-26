package job

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"time"
)

// reportCSVHeader names the long-form series columns. The activity
// columns are prefixed because `done` and `blocked` already name the
// sample's state; activity counts events in the bucket, not state.
var reportCSVHeader = []string{
	"end", "scope", "done", "open", "blocked", "canceled", "plans_done",
	"activity_created", "activity_claimed", "activity_done", "activity_blocked",
}

// writeReportCSV writes one row per sample, oldest first, with that
// bucket's activity counts beside it (zeros when a bucket has none).
// Activity pairs with Series by position, as the Report promises; ends
// cannot be the key, because a last bucket shorter than a second shares
// its second with the one before. Times are RFC3339 in the report's zone.
func writeReportCSV(w io.Writer, r Report) error {
	loc := reportLocation(r.Window)
	cw := csv.NewWriter(w)
	if err := cw.Write(reportCSVHeader); err != nil {
		return err
	}
	for i, s := range r.Series {
		var a ActivityCount
		if i < len(r.Activity) {
			a = r.Activity[i]
		}
		row := []string{s.End.In(loc).Format(time.RFC3339)}
		for _, n := range []int{s.Scope, s.Done, s.Open, s.Blocked, s.Canceled, s.PlansDone, a.Created, a.Claimed, a.Done, a.Blocked} {
			row = append(row, strconv.Itoa(n))
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// writeReportJSON writes the Report as-is, indented, with empty lists
// as [] rather than null so a reader never has to tell the two apart.
func writeReportJSON(w io.Writer, r Report) error {
	r.DoneByActor = nonNil(r.DoneByActor)
	r.Series = nonNil(r.Series)
	r.Activity = nonNil(r.Activity)
	r.Imports = nonNil(r.Imports)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
