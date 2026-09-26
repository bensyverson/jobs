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
// bucket's activity counts joined on the bucket end (zeros when a
// bucket has none). Times are RFC3339 in the report's zone.
func writeReportCSV(w io.Writer, r Report) error {
	loc := reportLocation(r.Window)
	activity := make(map[int64]ActivityCount, len(r.Activity))
	for _, a := range r.Activity {
		activity[a.End.Unix()] = a
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(reportCSVHeader); err != nil {
		return err
	}
	for _, s := range r.Series {
		a := activity[s.End.Unix()]
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
