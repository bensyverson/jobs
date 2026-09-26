package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// runStats runs `job stats` end to end and fails the test on any error.
func runStats(t *testing.T, dbFile string, args ...string) string {
	t.Helper()
	stdout, _, err := runCLI(t, dbFile, append([]string{"stats"}, args...)...)
	if err != nil {
		t.Fatalf("stats %v: %v", args, err)
	}
	return stdout
}

func statsJSON(t *testing.T, dbFile string, args ...string) job.Report {
	t.Helper()
	out := runStats(t, dbFile, append(args, "--format", "json")...)
	var r job.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stats --format json is not a Report: %v\n%s", err, out)
	}
	return r
}

// seedStats is a root with one open and one done leaf.
func seedStats(t *testing.T) (dbFile, root string) {
	t.Helper()
	dbFile = setupCLI(t)
	db := openTestDB(t, dbFile)
	root = job.MustAdd(t, db, "", "Root")
	job.MustAdd(t, db, root, "Open leaf")
	d := job.MustAdd(t, db, root, "Done leaf")
	job.MustDone(t, db, d)
	return dbFile, root
}

func TestStats_RefusesBadFlags(t *testing.T) {
	dbFile, _ := seedStats(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--format", "yaml"}, "md, json, csv"},
		{[]string{"--by", "fortnight"}, "minute, hour, 6h, day, week"},
		{[]string{"--since", "fortnight"}, "--since"},
		{[]string{"--until", "all"}, "--until"},
		{[]string{"--timezone", "Mars/Olympus"}, "--timezone"},
	}
	for _, c := range cases {
		_, _, err := runCLI(t, dbFile, append([]string{"stats"}, c.args...)...)
		if err == nil {
			t.Errorf("stats %v: want an error", c.args)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("stats %v: error %q should name %q", c.args, err, c.want)
		}
	}
}

func TestStats_TakesAtMostOneID(t *testing.T) {
	dbFile, root := seedStats(t)
	if _, _, err := runCLI(t, dbFile, "stats", root, root); err == nil {
		t.Error("stats with two ids: want an error")
	}
}

func TestStats_TextLeadsWithHeadlineAndDrawsBurnUp(t *testing.T) {
	dbFile, _ := seedStats(t)
	out := runStats(t, dbFile)
	for _, w := range []string{"Leaves", "Plans", "Pace", "Done by", "Burn-up"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q:\n%s", w, out)
		}
	}
	if !strings.HasPrefix(out, "Stats") {
		t.Errorf("text should open with the header:\n%s", out)
	}
}

func TestStats_NoIdentityRequired(t *testing.T) {
	dbFile, _ := seedStats(t)
	runStats(t, dbFile) // setupCLI records no default identity
}

func TestStats_JSONIsTheReport(t *testing.T) {
	dbFile, _ := seedStats(t)
	r := statsJSON(t, dbFile)
	if r.Schema != job.ReportSchema {
		t.Errorf("schema = %d, want %d", r.Schema, job.ReportSchema)
	}
	if len(r.Series) == 0 {
		t.Error("series is empty")
	}
	if r.Leaves.Done != 1 {
		t.Errorf("leaves.done = %d, want 1", r.Leaves.Done)
	}
}

// Moved from TestStatus_Usage_SubtreeID: a positional id scopes the
// figures to that subtree.
func TestStats_SubtreeScope(t *testing.T) {
	dbFile, root := seedStats(t)
	db := openTestDB(t, dbFile)
	sub := job.MustAdd(t, db, root, "Subtree")
	job.MustAdd(t, db, sub, "Sub open")
	d := job.MustAdd(t, db, sub, "Sub done")
	job.MustDone(t, db, d)
	db.Close()

	r := statsJSON(t, dbFile, sub)
	if r.Window.Scope != sub {
		t.Errorf("window.scope = %q, want %q", r.Window.Scope, sub)
	}
	if r.Leaves.Done != 1 {
		t.Errorf("subtree leaves.done = %d, want 1 (the forest has 2)", r.Leaves.Done)
	}
}

// Moved from TestStatus_Usage_Since_WindowedHeader: --since narrows the
// window, and the report echoes it.
func TestStats_SinceNarrowsTheWindow(t *testing.T) {
	dbFile, _ := seedStats(t)
	r := statsJSON(t, dbFile, "--since", "7d")
	want := time.Now().Add(-7 * 24 * time.Hour)
	if d := r.Window.Since.Sub(want); d < -time.Minute || d > time.Minute {
		t.Errorf("window.since = %v, want ≈ %v", r.Window.Since, want)
	}
}

func TestStats_ByAndTimezone(t *testing.T) {
	dbFile, _ := seedStats(t)
	r := statsJSON(t, dbFile, "--by", "hour", "--timezone", "UTC")
	if r.Window.Bucket != job.BucketHour {
		t.Errorf("window.bucket = %q, want hour", r.Window.Bucket)
	}
	if r.Window.Timezone != "UTC" {
		t.Errorf("window.timezone = %q, want UTC", r.Window.Timezone)
	}
}

func TestStats_CSVOneRowPerBucket(t *testing.T) {
	dbFile, _ := seedStats(t)
	r := statsJSON(t, dbFile)
	out := runStats(t, dbFile, "--format", "csv")
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("not CSV: %v\n%s", err, out)
	}
	if len(rows) != len(r.Series)+1 {
		t.Errorf("got %d rows, want a header and %d buckets", len(rows), len(r.Series))
	}
}

func TestStatus_UsageFlagIsGone(t *testing.T) {
	dbFile, _ := seedStats(t)
	for _, args := range [][]string{{"status", "--usage"}, {"status", "--since", "7d"}} {
		_, _, err := runCLI(t, dbFile, args...)
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%v: err = %v, want an unknown-flag error", args, err)
		}
	}
}

func TestSchema_Stats(t *testing.T) {
	dbFile := setupCLI(t)
	stdout, _, err := runCLI(t, dbFile, "schema", "stats")
	if err != nil {
		t.Fatalf("schema stats: %v", err)
	}
	var buf bytes.Buffer
	if err := job.WriteSchema(&buf, job.SchemaStats); err != nil {
		t.Fatal(err)
	}
	if stdout != buf.String() {
		t.Error("`job schema stats` should print the stats schema")
	}
}

func TestSchema_UnknownKindErrors(t *testing.T) {
	dbFile := setupCLI(t)
	_, _, err := runCLI(t, dbFile, "schema", "report")
	if err == nil || !strings.Contains(err.Error(), "plan, stats") {
		t.Errorf("err = %v, want one naming plan, stats", err)
	}
}
