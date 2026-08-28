package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newKindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kind <root> [task|issue]",
		Short: "Read or set a root's tree kind (task-tree or issue-tree)",
		Long: `Read or set the tree kind of a root task.

A task-tree is planned work: decomposed, with a bottom, closing when its children close. An issue-tree is discovered work — a bug, a defect, anything encountered rather than planned — whose lifetime is not bounded by the plan that surfaced it.

'next', 'orient' and 'claim --next' answer "what is next in my plan" and so skip issue-trees; pass --issues to those verbs to ask the opposite question. An explicit id, an explicit scope, or a 'job focus' set on an issue root overrides the default — you asked for that tree.

Kind is a property of the root only. Children of an issue root are ordinary tasks, so every verb keeps working inside an issue-tree. Converting in either direction loses nothing: the change is recorded as a kind_changed event and nothing else about the tree is touched.

With no kind argument, prints the root's current kind.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			shortID := args[0]

			if len(args) == 1 {
				task, err := job.GetTaskByShortID(db, shortID)
				if err != nil {
					return err
				}
				if task == nil {
					return fmt.Errorf("task %q not found", shortID)
				}
				if task.ParentID != nil {
					return fmt.Errorf(
						"tree kind is a property of the root only; %s is a child. Ask its root instead.",
						shortID,
					)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %q: %s-tree\n", task.ShortID, task.Title, task.Kind)
				return nil
			}

			kind, err := job.ParseTreeKind(args[1])
			if err != nil {
				return err
			}

			actor, err := requireAs(db)
			if err != nil {
				return err
			}

			res, err := job.RunSetKind(db, shortID, kind, actor)
			if err != nil {
				return err
			}
			if !res.Changed {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %q is already %s\n", res.ShortID, res.Title, res.To.Label())
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s %q is now %s (was %s) as=%s\n",
				res.ShortID, res.Title, res.To.Label(), res.From.Label(), actor)
			return nil
		},
	}
	return cmd
}
