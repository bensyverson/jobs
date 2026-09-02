package main

import (
	"fmt"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job rekey <rep>:<id>` — resolve a cross-replica short-id collision.
//
// It opens the cache without reconciling it against the store, because the
// rebuild is what failed, and works from the raw log instead.
func newRekeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rekey <replica>:<id>",
		Short: "Give one replica's task a fresh short id after a collision",
		Long: "Two replicas can mint the same short id while apart. That fails the rebuild, because no automatic remap is safe once an id is in notes and commit messages: a person has to say which task keeps it.\n\n" +
			"The rebuild's error names both replicas, both titles, and the exact command to run. `job rekey <replica>:<id>` records a `rekeyed` event in this replica's log giving the named replica's task a fresh six-character id, and rebuilds. From then on every machine that pulls the log applies the same rename without deciding again — the earlier task keeps the id notes and commits already cite, and the log names both so a reader can tell.\n\n" +
			"It reads .jobs/log directly rather than the cache, since the cache is what refused to build.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := job.ResolveDBPath(dbPath)
			db, err := job.OpenDBForRecovery(path)
			if err != nil {
				return err
			}
			defer db.Close()

			actor, err := requireAs(db)
			if err != nil {
				return err
			}

			res, err := job.RunRekey(db, args[0], actor)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Rekeyed %s's %s %q to %s. The cache is rebuilt; commit .jobs/log to carry the rename.\n",
				res.Rep, res.OldID, res.Title, res.NewID)
			return nil
		},
	}
	return cmd
}
