package main

import (
	"fmt"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job merge` — fold a second, diverged copy of the database into this one.
// It copies history rather than making it, so it records no event of its own
// and needs no --as; and it only ever writes the database --db names.
func newMergeCmd() *cobra.Command {
	var dryRun bool
	var format string

	cmd := &cobra.Command{
		Use:   "merge <other.jobs.db>",
		Short: "Merge a diverged copy of the database into this one",
		Long: "Fold a second copy of this database — one that was copied and then written on both machines — into the local one.\n\n" +
			"The two files must share an event prefix; without one they are unrelated databases and the merge is refused. Tasks that exist on one side only are copied over whole, with their labels, blocks, criteria, provenance and events. Tasks both sides hold merge per table: the task row from the side with the later edit, labels and blocks as a union, criteria matched by short id with the later edit winning, and notes and events as a deduplicated union. A live claim on either side survives unless the other side closed the task, in which case the close wins and the claim is dropped.\n\n" +
			"The report names every task that existed on one side only, every task both sides touched and which side won, and every claim it dropped. --dry-run prints it and writes nothing. The other file is never written; merging the same pair twice changes nothing. No --as required: merge records no events.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "md" && format != "json" {
				return fmt.Errorf("merge: --format must be one of md, json (got %q)", format)
			}

			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			report, err := job.RunMerge(db, args[0], dryRun)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if format == "json" {
				data, err := report.JSON()
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}
			fmt.Fprint(out, report.Markdown())
			return nil
		},
	}

	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "print the report and write nothing")
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
