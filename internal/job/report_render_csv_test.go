package job

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func renderFormat(t *testing.T, r Report, f ReportFormat) string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteReport(&buf, r, f, 80); err != nil {
		t.Fatalf("WriteReport(%s): %v", f, err)
	}
	return buf.String()
}

func readCSV(t *testing.T, out string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("not CSV: %v\n%s", err, out)
	}
	if len(rows) == 0 {
		t.Fatal("no CSV rows, not even a header")
	}
	return rows
}

func TestReportCSV_Header(t *testing.T) {
	rows := readCSV(t, renderFormat(t, sampleReport(), ReportFormatCSV))
	want := []string{"end", "scope", "done", "open", "blocked", "canceled", "plans_done",
		"activity_created", "activity_claimed", "activity_done", "activity_blocked"}
	if !reflect.DeepEqual(rows[0], want) {
		t.Errorf("header = %v\nwant     %v", rows[0], want)
	}
}

func TestReportCSV_OneRowPerBucket(t *testing.T) {
	r := sampleReport()
	rows := readCSV(t, renderFormat(t, r, ReportFormatCSV))
	if len(rows) != len(r.Series)+1 {
		t.Fatalf("got %d rows, want header + %d", len(rows), len(r.Series))
	}
	want := []string{"2026-09-14T00:00:00Z", "640", "550", "90", "4", "3", "5", "240", "201", "250", "2"}
	if !reflect.DeepEqual(rows[2], want) {
		t.Errorf("second bucket = %v\nwant            %v", rows[2], want)
	}
}

func TestReportCSV_TimesInTheReportZone(t *testing.T) {
	r := sampleReport()
	r.Window.Timezone = "America/Chicago"
	rows := readCSV(t, renderFormat(t, r, ReportFormatCSV))
	if got := rows[1][0]; got != "2026-09-06T19:00:00-05:00" {
		t.Errorf("end = %q, want it in the report's zone", got)
	}
}

func TestReportCSV_MissingActivityLeavesZeros(t *testing.T) {
	r := sampleReport()
	r.Activity = r.Activity[:1]
	rows := readCSV(t, renderFormat(t, r, ReportFormatCSV))
	if got := rows[4][7:]; !reflect.DeepEqual(got, []string{"0", "0", "0", "0"}) {
		t.Errorf("a bucket without activity should count zero: %v", got)
	}
}

func TestReportCSV_EmptySeriesIsHeaderOnly(t *testing.T) {
	rows := readCSV(t, renderFormat(t, Report{Schema: ReportSchema}, ReportFormatCSV))
	if len(rows) != 1 {
		t.Errorf("got %d rows, want the header alone", len(rows))
	}
}

func TestReportJSON_RoundTrips(t *testing.T) {
	r := sampleReport()
	out := renderFormat(t, r, ReportFormatJSON)
	var got Report
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Schema != ReportSchema || got.Leaves != r.Leaves || len(got.Series) != len(r.Series) || *got.Plans.MedianImportToCloseSeconds != *r.Plans.MedianImportToCloseSeconds {
		t.Errorf("round trip lost data:\n%s", out)
	}
}

func TestReportJSON_EmptyListsAreArraysNotNull(t *testing.T) {
	out := renderFormat(t, Report{Schema: ReportSchema}, ReportFormatJSON)
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, k := range []string{"done_by_actor", "series", "activity", "imports"} {
		if _, ok := raw[k].([]any); !ok {
			t.Errorf("%s = %v, want []", k, raw[k])
		}
	}
	if raw["schema"] != float64(ReportSchema) {
		t.Errorf("schema = %v, want %d", raw["schema"], ReportSchema)
	}
}
