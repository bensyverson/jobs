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
					fmt.Fprintf(cmd.OutOrStdout(), "%s- [ ] `%s` %s%s\n",
						indent, t.ID, t.Title, importAnnotations(t))
				}
				return nil
			}

			for _, t := range res.Tasks {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s%s\n", t.ID, t.Title, importAnnotations(t))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&parent, "parent", "p", "", "make imported roots children of this task")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "validate the plan without writing to the database")
	cmd.Flags().StringVar(&format, "format", "md", "output format (md|json)")
	return cmd
}

// importAnnotations renders the parenthesised trailers on one echoed import
// row: the tree kind, the blockers, and the found-in source, in that order.
// Each is omitted when the plan did not ask for it, so a plan using none of
// them echoes exactly as it always has. `(issue-tree)` matches the tag `ls`
// puts on an issue root; the default task-tree is silent.
func importAnnotations(t job.ImportedTask) string {
	var out string
	if t.Kind == string(job.KindIssue) {
		out += " (issue-tree)"
	}
	if len(t.BlockedBy) > 0 {
		out += fmt.Sprintf(" (blocked on %s)", strings.Join(t.BlockedBy, ", "))
	}
	if t.FoundIn != "" {
		out += fmt.Sprintf(" (found in %s)", t.FoundIn)
	}
	return out
}
