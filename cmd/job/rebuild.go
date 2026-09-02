package main

import (
	"fmt"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job rebuild` — throw the cache away and replay the log into a new one.
//
// Every open already does this when a log file's size no longer matches the
// offset the cache applied, so this verb is for the case where that check
// cannot help: a cache you suspect, or one that survived a crash in a way the
// watermark did not catch. It records no event of its own and needs no --as.
func newRebuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the cache from .jobs/log",
		Long: "Drop every table in .jobs.db and replay .jobs/log/*.jsonl into it, in (ts, rep, seq) order.\n\n" +
			"The log is the record and the cache is disposable, so this is always safe and never loses anything: the cache holds nothing the log does not. Every open already rebuilds when a log file has grown, so this is the recovery verb rather than a routine one — reach for it after a crash, or when you suspect the cache.\n\n" +
			"After a rebuild that ingested another replica's events, the reconcile pass repairs the invariants a single machine keeps: a parent whose last child closed elsewhere is closed, a child of a purged task is purged, and the later of two claims made while the machines were apart is released. Each repair is an ordinary event in the log, and each is printed.\n\n" +
			"A database that predates the store is refused: its cache holds history no log line reproduces, so replaying would lose it. Adoption is what converts one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			report, err := job.RunRebuild(db)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if report.Refused {
				fmt.Fprintln(out, report.Notice)
				return nil
			}
			for _, repair := range report.Repairs {
				fmt.Fprintln(out, repair)
			}
			rep := report.Rep
			if rep == "" {
				rep = "unminted"
			}
			fmt.Fprintf(out, "Rebuilt the cache from %d log file(s), %d event(s). Replica %s.\n",
				report.Files, report.Events, rep)
			return nil
		},
	}
	return cmd
}
