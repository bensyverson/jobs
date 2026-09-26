package job

import (
	"os"
	"testing"
	"time"
)

// BenchmarkBuildReport times a report over a real store. It opens the
// database JOBS_REPORT_BENCH_DB names — opening may rebuild the cache, so
// point it at a copy, never at a live store — and skips when unset:
//
//	JOBS_REPORT_BENCH_DB=/copy/.jobs.db go test ./internal/job -run '^$' -bench BuildReport -benchtime 20x
func BenchmarkBuildReport(b *testing.B) {
	path := os.Getenv("JOBS_REPORT_BENCH_DB")
	if path == "" {
		b.Skip("JOBS_REPORT_BENCH_DB is not set")
	}
	db, err := OpenDB(path)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	all, err := BuildReport(db, ReportQuery{})
	if err != nil {
		b.Fatal(err)
	}
	until := all.Window.Until
	cases := []struct {
		name string
		q    ReportQuery
	}{
		{"all", ReportQuery{Until: until}},
		{"7d", ReportQuery{Since: until.Add(-7 * 24 * time.Hour), Until: until}},
		{"all-by-hour", ReportQuery{Until: until, Bucket: BucketHour}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			var r Report
			for b.Loop() {
				if r, err = BuildReport(db, c.q); err != nil {
					b.Fatal(err)
				}
			}
			last := r.Series[len(r.Series)-1]
			b.ReportMetric(float64(len(r.Series)), "samples")
			b.Logf("%s: %d samples by %s; at Until scope=%d done=%d open=%d blocked=%d canceled=%d; leaves done in window=%d",
				c.name, len(r.Series), r.Window.Bucket, last.Scope, last.Done, last.Open, last.Blocked, last.Canceled, r.Leaves.Done)
		})
	}
}
