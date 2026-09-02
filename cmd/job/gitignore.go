package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job gitignore` — append the recommended entries to the .gitignore beside
// the database. It touches no database and records no event, so it needs no
// --as and works before `init` as happily as after it: the directory is
// what matters, not whether a database is in it yet.
func newGitignoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gitignore",
		Short: "Add the recommended Jobs entries to .gitignore",
		Long: "Append the recommended entries to the .gitignore beside the job database, creating the file if it does not exist.\n\n" +
			"Only missing patterns are appended, so running it twice changes nothing. The database itself is ignored by default; delete the `.jobs.db` line if you want to share it with the repository.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := filepath.Dir(job.ResolveDBPathForInit(dbPath))
			written, alreadyPresent, err := job.WriteGitignoreEntries(dir)
			if err != nil {
				return err
			}
			if len(written) > 0 {
				noun := "entries"
				if len(written) == 1 {
					noun = "entry"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d %s to .gitignore: %s\n", len(written), noun, strings.Join(written, ", "))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), ".gitignore already includes %s\n", humanJoin(alreadyPresent))
			}
			return nil
		},
	}
}
