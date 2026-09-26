package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema [plan|stats]",
		Short: "Print a JSON Schema: the `job import` grammar (default) or `job stats` output",
		Long:  "Prints a JSON Schema (Draft 2020-12). `plan`, the default, is the grammar `job import` reads. `stats` is the shape `job stats --format=json` emits, versioned by its `schema` field.",
		Args:  cobra.MaximumNArgs(1),
		ValidArgs: []string{
			string(job.SchemaPlan), string(job.SchemaStats),
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			raw := ""
			if len(args) == 1 {
				raw = args[0]
			}
			kind, ok := job.ParseSchemaKind(raw)
			if !ok {
				return fmt.Errorf("schema: unknown schema %q (want one of %s)", raw, job.SchemaKindList())
			}
			return job.WriteSchema(cmd.OutOrStdout(), kind)
		},
	}
	return cmd
}
