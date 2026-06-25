package main

import (
	"encoding/json"
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
	"strings"
)

func newImportCmd() *cobra.Command {
	var parent string
	var dryRun bool
	var format string
	cmd := &cobra.Command{
		Use:   "import <file.md>",
		Short: "Import tasks from a Markdown plan with a YAML tasks: block",
		Long:  "Parse the first fenced YAML block whose top-level key is tasks: and create every task atomically. Use --dry-run to validate without writing. Use --parent <id> to nest the import under an existing task.",
		Args:  cobra.ExactArgs(1),
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

			res, err := job.RunImport(db, args[0], parent, dryRun, actor)
			if err != nil {
				return err
			}

			// Selection warnings go to stderr so they're visible on both the
			// human and --format=json paths without polluting parsable stdout.
			for _, w := range res.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+w)
			}

			if format == "json" {
				b, err := json.Marshal(res)
				if err != nil {
					return err
				}
				cmd.OutOrStdout().Write(b)
				fmt.Fprintln(cmd.OutOrStdout())
				return nil
			}

			if res.DryRun {
				// Indented checklist so parent/child shape and blocker edges
				// are both visible at a glance — the flat form hid parenting.
				depth := make(map[string]int, len(res.Tasks))
				for _, t := range res.Tasks {
					if strings.HasPrefix(t.Parent, "<new-") {
						depth[t.ID] = depth[t.Parent] + 1
					} else {
						depth[t.ID] = 0
					}
				}
				for _, t := range res.Tasks {
					indent := strings.Repeat("  ", depth[t.ID])
					line := fmt.Sprintf("%s- [ ] `%s` %s", indent, t.ID, t.Title)
					if len(t.BlockedBy) > 0 {
						line += fmt.Sprintf(" (blocked on %s)", strings.Join(t.BlockedBy, ", "))
					}
					fmt.Fprintln(cmd.OutOrStdout(), line)
				}
				return nil
			}

			for _, t := range res.Tasks {
				if len(t.BlockedBy) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s (blocked on %s)\n",
						t.ID, t.Title, strings.Join(t.BlockedBy, ", "))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", t.ID, t.Title)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&parent, "parent", "p", "", "make imported roots children of this task")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "validate the plan without writing to the database")
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}
