package main

import (
	"database/sql"
	"fmt"
	"io"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

// focusLineLabel is the label each kind's focus prints under, padded so the
// two lines align.
func focusLineLabel(kind job.TreeKind) string {
	if kind.IsIssue() {
		return "Issues:"
	}
	return "Task:  "
}

// writeFocusRoot renders a focused root and its availability rollup.
func writeFocusRoot(out io.Writer, db *sql.DB, focus *job.Task) error {
	rollup, err := job.RunSummary(db, focus.ShortID)
	if err != nil {
		return err
	}
	t := rollup.Target
	fmt.Fprintf(out, "%s %q — %d of %d done, %d available\n",
		focus.ShortID, focus.Title, t.Done, t.Done+t.Open, t.Available)
	return nil
}

func newFocusCmd() *cobra.Command {
	var release bool
	var issues bool
	cmd := &cobra.Command{
		Use:   "focus [id]",
		Short: "Show, set (or --release) your active roots — one per tree kind",
		Long: "Show your focus: the roots that scope bare `next`, `claim --next`, `status`'s Next: hint, and " +
			"`orient`'s default target. Focus is per tree kind — one task-tree and one issue-tree — so " +
			"triaging a bug never loses your place in the plan.\n\n" +
			"Claiming is the usual setter: claim any task and the focus for *that root's kind* follows it, " +
			"last claim wins. `job focus <id>` sets it explicitly (name any task in the tree; its root is " +
			"used). Focus releases automatically when its root closes; `focus --release` releases both kinds " +
			"manually (the \"pause this tree\" case) and `--release --issues` releases only the issue focus.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if issues && !release {
				return fmt.Errorf("--issues modifies --release; bare `job focus` already prints both kinds, and `job focus <id>` takes the kind from the root")
			}
			if release && len(args) == 1 {
				return fmt.Errorf("focus: --release takes no id; run `job focus --release` to release, or `job focus %s` to set", args[0])
			}

			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()

			out := cmd.OutOrStdout()

			switch {
			case release:
				return runFocusRelease(db, out, issues)
			case len(args) == 1:
				return runFocusSet(db, out, args[0])
			default:
				return runFocusShow(db, out)
			}
		},
	}
	cmd.Flags().BoolVar(&release, "release", false, "release your focus (both kinds unless --issues)")
	cmd.Flags().BoolVar(&issues, "issues", false, "with --release: release only the issue-tree focus")
	return cmd
}

// runFocusShow prints one line per kind: the focused root and its rollup, or
// `(none)`.
func runFocusShow(db *sql.DB, out io.Writer) error {
	// Showing is read-only: resolve the identity softly, like status and
	// orient.
	actor, _ := job.ResolveIdentity(db, asFlag)
	for _, kind := range job.FocusKinds {
		focus, err := job.GetFocusKind(db, actor, kind)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s ", focusLineLabel(kind))
		if focus == nil {
			fmt.Fprintln(out, "(none)")
			continue
		}
		if err := writeFocusRoot(out, db, focus); err != nil {
			return err
		}
	}
	return nil
}

// runFocusSet points the caller's focus at the named task's root. The root's
// kind decides which slot moves; the other is untouched.
func runFocusSet(db *sql.DB, out io.Writer, shortID string) error {
	actor, err := requireAs(db)
	if err != nil {
		return err
	}
	root, err := job.SetFocus(db, shortID, actor)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Focused %s-tree ", root.Kind)
	return writeFocusRoot(out, db, root)
}

// runFocusRelease clears the caller's focus: both kinds, or only the issue
// kind with --issues.
func runFocusRelease(db *sql.DB, out io.Writer, issuesOnly bool) error {
	// Releasing writes events; hold it to the write-verb identity bar.
	actor, err := requireAs(db)
	if err != nil {
		return err
	}
	kinds := job.FocusKinds
	if issuesOnly {
		kinds = []job.TreeKind{job.KindIssue}
	}
	released := 0
	for _, kind := range kinds {
		root, err := job.ReleaseFocusKind(db, actor, kind)
		if err != nil {
			return err
		}
		if root == nil {
			continue
		}
		released++
		fmt.Fprintf(out, "Released %s focus: %s %q\n", kind, root.ShortID, root.Title)
	}
	if released == 0 {
		if issuesOnly {
			fmt.Fprintln(out, "No focus set on an issue-tree — nothing to release.")
		} else {
			fmt.Fprintln(out, "No focus set — nothing to release.")
		}
		return nil
	}
	fmt.Fprintln(out, "The matching no-arg defaults are global again; claiming sets a new focus.")
	return nil
}
