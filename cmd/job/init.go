package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

func newInitCmd() *cobra.Command {
	var force bool
	var strict bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new job database",
		Long: "Initialize a new .jobs.db in the current directory. Errors if one already exists unless --force is used.\n\n" +
			"--as <name> records <name> as this database's default identity: every later write that omits --as is attributed to it. " +
			"Use the name of whoever is running the command — a person's handle, or, for an automated assistant, the assistant's own name rather than the account it runs under. " +
			"$USER is the human who launched the session, which is usually not who is doing the work.\n\n" +
			"Pass --strict instead to record no default at all; every write then has to carry its own --as.\n\n" +
			"The database is local to your checkout by default: run `job gitignore` to add the recommended entries to .gitignore.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before CreateDB, so a call that names nobody leaves nothing
			// behind to --force over.
			if !strict && asFlag == "" {
				return fmt.Errorf("%s", initIdentityRequiredMsg)
			}

			path := job.ResolveDBPathForInit(dbPath)
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists. Use --force to overwrite", path)
			}
			if force {
				os.Remove(path)
			}
			db, err := job.CreateDB(path)
			if err != nil {
				return err
			}
			defer db.Close()

			if force {
				fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s (overwrote existing database)\n", path)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Initialized %s\n", path)
			}

			// --strict is the stronger statement, so it wins over --as:
			// under strict mode there is no default identity to record.
			if strict {
				if err := job.InitIdentity(db, "", true); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Strict mode: writes require --as <name> (no default identity).")
			} else {
				if err := job.InitIdentity(db, asFlag, false); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Default identity: %s\n", asFlag)
			}

			// Only advice that can be acted on: inside a repository, and
			// only while something is still unignored.
			dir := filepath.Dir(path)
			if isGitRepo(dir) {
				missing, err := job.MissingGitignoreEntries(dir)
				if err != nil {
					return err
				}
				if len(missing) > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
					fmt.Fprintln(cmd.OutOrStdout(), "Add to .gitignore (or run: job gitignore):")
					fmt.Fprintln(cmd.OutOrStdout())
					fmt.Fprintln(cmd.OutOrStdout(), job.GitignoreHint())
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing database")
	cmd.Flags().BoolVar(&strict, "strict", false, "require --as on every write; do not set a default identity")
	return cmd
}

// isGitRepo reports whether dir is the root of a git checkout. A worktree
// and a submodule carry .git as a regular file rather than a directory, so
// existence is the test, not its kind.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
