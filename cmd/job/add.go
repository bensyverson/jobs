package main

import (
	"fmt"
	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var desc string
	var descFile string
	var before string
	var labels []string
	var criteria []string
	var parentFlag string
	var idOnly bool
	var foundIn string
	cmd := &cobra.Command{
		Use:   "add [parent] <title>",
		Short: "Add a new task",
		Long:  "Add a new task. If parent is provided, the task is added as a child. Use --desc for a description (or -F <path> to read it from a file), --before to insert before a specific sibling, --criterion (repeatable) to attach acceptance criteria, and --found-in to record the task that surfaced this one.",
		Args:  cobra.RangeArgs(1, 2),
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

			// --desc stays literal on add/edit; -F is the only file form, so a
			// description that legitimately begins with "@" survives.
			resolvedDesc, _, derr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:          "add",
				InlineName:    "desc",
				Inline:        desc,
				File:          descFile,
				InlineLiteral: true,
			})
			if derr != nil {
				return derr
			}
			desc = resolvedDesc

			var parentShortID, title string
			if len(args) == 2 {
				parentShortID = args[0]
				title = args[1]
			} else {
				title = args[0]
			}

			// --parent= (explicitly empty) is the literal-title escape hatch
			// for the single-arg ambiguous-id case below. Distinguish "user
			// set the flag, even to empty" from "user did not pass the flag"
			// via cmd.Flags().Changed — Cobra's StringVar default cannot tell
			// them apart on its own.
			parentChanged := cmd.Flags().Changed("parent")
			if parentChanged {
				if parentShortID != "" && parentShortID != parentFlag {
					return fmt.Errorf("add: --parent %q conflicts with positional parent %q", parentFlag, parentShortID)
				}
				parentShortID = parentFlag
			}

			// Branch the error on what's actually wrong, not on a single
			// "task not found" that misdirects the operator. Skipped when
			// --parent was explicitly passed (the user has stated intent).
			if !parentChanged {
				switch len(args) {
				case 2:
					// Two args, leading positional must be a short id.
					if t, _ := job.GetTaskByShortID(db, args[0]); t == nil {
						return fmt.Errorf(
							"add: no such parent %q. The positional order is `add <parent> <title>`; if you meant to create a root task with this title, use --parent=\"\" to disambiguate.",
							args[0],
						)
					}
				case 1:
					// Single arg that resolves to an existing task is almost
					// certainly a "forgot the title" slip — silently creating
					// a root task literally titled with the short id is the
					// failure mode this guard exists to prevent.
					if t, _ := job.GetTaskByShortID(db, args[0]); t != nil {
						return fmt.Errorf(
							"add: ambiguous single arg %q is an existing task — did you mean `add %s <title>`? (To create a root task literally named %q, pass --parent=\"\" to disambiguate.)",
							args[0], args[0], args[0],
						)
					}
				}
			}

			// Resolve --found-in before the task exists, so a mistyped source
			// fails without leaving a task behind. The edge itself is written
			// after RunAdd rather than inside it — see internal/job/foundin.go.
			if foundIn != "" {
				src, err := job.GetTaskByShortID(db, foundIn)
				if err != nil {
					return err
				}
				if src == nil {
					return fmt.Errorf("add: no such --found-in source %q", foundIn)
				}
			}

			var priorChildCount int
			if parentShortID != "" {
				priorChildCount, _, _ = job.CountOpenChildrenOfShortID(db, parentShortID)
			}

			res, err := job.RunAdd(db, parentShortID, title, desc, before, labels, actor)
			if err != nil {
				return err
			}
			if foundIn != "" {
				if err := job.RunSetFoundIn(db, res.ShortID, foundIn, actor); err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), res.ShortID)
			// --id-only is the scriptable contract: stdout is exactly the bare
			// id and nothing else, so `ID=$(job add ... --id-only)` captures a
			// clean value. Criteria are still attached (side effect preserved);
			// only the advisory chatter is suppressed.
			if !idOnly {
				if res.AutoReleasedParent != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Released: %s (prior claim by %s auto-released — parent now has open children)\n",
						res.AutoReleasedParent, res.AutoReleasedByActor)
				}
				if parentShortID != "" && priorChildCount > 0 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  %s now has %d children; complete them all to auto-close the parent.\n",
						parentShortID, priorChildCount+1)
				}
			}
			if len(criteria) > 0 {
				items := make([]job.Criterion, 0, len(criteria))
				for _, label := range criteria {
					items = append(items, job.Criterion{Label: label})
				}
				if _, err := job.RunAddCriteria(db, res.ShortID, items, actor); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&desc, "desc", "d", "", "task description")
	registerFileFlag(cmd, &descFile, "description", "desc")
	cmd.Flags().StringVarP(&before, "before", "b", "", "insert before this sibling task ID")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label to attach (repeatable)")
	cmd.Flags().StringArrayVar(&criteria, "criterion", nil, "acceptance criterion to attach, defaults to pending state (repeatable)")
	cmd.Flags().StringVar(&parentFlag, "parent", "", "parent task ID (alias for the positional parent argument)")
	cmd.Flags().StringVar(&foundIn, "found-in", "", "record the task that surfaced this one (provenance only — creates no blocking relationship)")
	cmd.Flags().BoolVar(&idOnly, "id-only", false, "print only the new task's bare ID on stdout (suppress advisory lines), for `ID=$(job add ... --id-only)`")
	return cmd
}
