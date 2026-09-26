package main

import (
	"fmt"
	"strings"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newStatsCmd() *cobra.Command {
	var since, until, by, timezone, format string
	cmd := &cobra.Command{
		Use:   "stats [id]",
		Short: "Show how the work has gone: headline figures and a burn-up",
		Long:  "Reports progress over a window: leaves created, done, canceled, open and blocked; plans imported, closed and open with the median import→close time; pace (done per week, median created→done and claimed→done); done by identity; and a text burn-up of scope against done that fits the terminal. Counts are in leaves, and each burn-up sample is the store's state as of that instant. With an id, the report covers that subtree. No --as required.\n\n--since accepts a range key (1h, 1d, 7d, 14d, 30d, all), a relative duration (90m, 3d) or an RFC3339 timestamp; the default is all time. --until accepts a duration or a timestamp; the default is now. --by overrides the bucket the window would choose. --timezone sets the calendar buckets align to (default: local). --format=json emits the report with a `schema` version (`job schema stats` prints its JSON Schema); --format=csv emits the series, one row per bucket.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, ok := job.ParseReportFormat(format)
			if !ok {
				return fmt.Errorf("stats: --format must be one of %s (got %q)", job.ReportFormatList(), format)
			}
			q := job.ReportQuery{}
			if len(args) == 1 {
				q.Scope = args[0]
			}
			if by != "" {
				if q.Bucket, ok = job.ParseBucket(by); !ok {
					return fmt.Errorf("stats: --by must be one of %s (got %q)", bucketList(), by)
				}
			}
			if timezone != "" {
				loc, err := time.LoadLocation(timezone)
				if err != nil {
					return fmt.Errorf("stats: --timezone: %w", err)
				}
				q.Location = loc
			}
			now := time.Now()
			var err error
			if q.Since, err = job.ParseWindowStart(since, now); err != nil {
				return fmt.Errorf("stats: %w", err)
			}
			if q.Until, err = job.ParseWindowEnd(until, now); err != nil {
				return fmt.Errorf("stats: %w", err)
			}
			// A relative --since is measured from now, so the window
			// ends at that same now rather than a few ms later.
			if q.Until.IsZero() && !q.Since.IsZero() {
				q.Until = now
			}

			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			report, err := job.BuildReport(db, q)
			if err != nil {
				return fmt.Errorf("stats: %w", err)
			}
			out := cmd.OutOrStdout()
			return job.WriteReport(out, report, f, termWidth(out))
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "window start: range key (1h, 1d, 7d, 14d, 30d, all), duration (e.g. 3d) or RFC3339 timestamp; default all")
	cmd.Flags().StringVar(&until, "until", "", "window end: duration (e.g. 1d) or RFC3339 timestamp; default now")
	cmd.Flags().StringVar(&by, "by", "", "bucket width: "+bucketList()+"; default chosen by the window")
	cmd.Flags().StringVar(&timezone, "timezone", "", "IANA zone buckets align to (e.g. America/Chicago); default local")
	cmd.Flags().StringVar(&format, "format", string(job.ReportFormatText), "output format ("+strings.ReplaceAll(job.ReportFormatList(), ", ", "|")+")")
	return cmd
}

func bucketList() string {
	names := []string{}
	for _, b := range job.Buckets() {
		names = append(names, string(b))
	}
	return strings.Join(names, ", ")
}
