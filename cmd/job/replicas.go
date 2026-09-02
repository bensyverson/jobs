package main

import (
	"fmt"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job replicas` and `job replica rename` — who is writing to this store.
//
// Every log file opens with a `replica` event naming the checkout that owns
// it, so the store already knows every machine that has ever written. These
// two verbs are the reader and the writer of that name.

func newReplicasCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "replicas",
		Short: "List every replica that has written to this store",
		Long: "A replica is one checkout on one machine, identified by six base62 characters. Every log file opens with a `replica` event naming the checkout that owns it — its label, hostname, checkout path and OS user — so this listing is read straight out of the log.\n\n" +
			"The replica marked as this checkout is the one this command appends to. Use `job replica rename <label>` to change its name; the rename is a new event, so the old name stays in the history.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			replicas, err := job.RunReplicas(db)
			if err != nil {
				return err
			}
			if format == "json" {
				return job.RenderReplicasJSON(cmd.OutOrStdout(), replicas)
			}
			job.RenderReplicas(cmd.OutOrStdout(), replicas)
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}

func newReplicaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replica",
		Short: "Inspect and name this checkout's replica",
	}
	cmd.AddCommand(newReplicaRenameCmd())
	return cmd
}

func newReplicaRenameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename <label>",
		Short: "Give this checkout's replica a human-readable name",
		Long: "Append a `replica` event naming this checkout. Every reader takes the latest one, so the rename propagates with the log like any other event and the previous name stays in the history.\n\n" +
			"The default name is this machine's hostname and the checkout's path; a name of your own is worth setting when a machine's hostname says nothing useful.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			actor, err := requireAs(db)
			if err != nil {
				return err
			}

			res, err := job.RunReplicaRename(db, args[0], actor)
			if err != nil {
				return err
			}
			if res.Prior != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Replica %s renamed from %q to %q.\n", res.Rep, res.Prior, res.Label)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Replica %s is now %q.\n", res.Rep, res.Label)
			}
			return nil
		},
	}
	return cmd
}
