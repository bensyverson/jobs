package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newFocusCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "focus",
		Short: "Show (or --clear) your active root — the tree scoping your no-arg next/claim/orient",
		Long: "Show your current focus: the root tree that scopes bare `next`, `claim --next`, `status`'s " +
			"Next: hint, and `orient`'s default target.\n\n" +
			"There is deliberately no setter — claiming is the setter. Claim any task (`claim <id>`, " +
			"`claim --next <root>`) and your focus follows its root, last claim wins. Focus releases " +
			"automatically when the root closes; `focus --clear` releases it manually (the \"pause this " +
			"tree\" case), returning the no-arg defaults to the global frontier.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			out := cmd.OutOrStdout()

			if clear {
				// Clearing writes an event; hold it to the write-verb
				// identity bar.
				actor, err := requireAs(db)
				if err != nil {
					return err
				}
				current, err := job.GetFocus(db, actor)
				if err != nil {
					return err
				}
				if current == nil {
					fmt.Fprintln(out, "No focus set — nothing to clear.")
					return nil
				}
				if err := job.ReleaseFocus(db, actor); err != nil {
					return err
				}
				fmt.Fprintf(out, "Released focus: %s %q. No-arg next/claim/orient are global again; claiming sets a new focus.\n",
					current.ShortID, current.Title)
				return nil
			}

			// Showing is read-only: resolve the identity softly, like
			// status and orient.
			actor, _ := job.ResolveIdentity(db, asFlag)
			focus, err := job.GetFocus(db, actor)
			if err != nil {
				return err
			}
			if focus == nil {
				fmt.Fprintln(out, "No focus set. Claiming any task focuses its root; no-arg next/claim/orient are global until then.")
				return nil
			}
			rollup, err := job.RunSummary(db, focus.ShortID)
			if err != nil {
				return err
			}
			t := rollup.Target
			fmt.Fprintf(out, "Focus: %s %q — %d of %d done, %d available\n",
				focus.ShortID, focus.Title, t.Done, t.Done+t.Open, t.Available)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "release your focus (no-arg defaults go global until the next claim)")
	return cmd
}
