package main

import (
	"database/sql"
	"fmt"
	"strings"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

// foundInNone is the literal that suppresses the defaulted provenance edge.
// Short ids are five characters, so it can never collide with one.
const foundInNone = "none"

// newIssueCmd builds the `issue` verb: `add` with the parent resolved and
// the provenance defaulted. Filing a bug mid-task is the everyday path, and
// it should not need the issue root's id or the id of the leaf you are on.
func newIssueCmd() *cobra.Command {
	var desc string
	var descFile string
	var labels []string
	var foundIn string
	cmd := &cobra.Command{
		Use:   "issue <title>",
		Short: "File an issue under the issue-tree root you are working in",
		Long: "File an issue: a task under the resolved issue-tree root. The root is your focused issue root " +
			"(claiming inside an issue tree sets it), else the only issue root in the database; with several and " +
			"no focus, the error names them and `job focus <id>` picks one. --found-in defaults to your live claim " +
			"when you hold exactly one, so provenance is right by construction; `--found-in none` suppresses it and " +
			"`--found-in <id>` names a different source. Bodies work as on `add`: --desc, -F <path>, -F - for stdin.",
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

			resolvedDesc, _, derr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:          "issue",
				InlineName:    "desc",
				Inline:        desc,
				File:          descFile,
				InlineLiteral: true,
			})
			if derr != nil {
				return derr
			}
			desc = resolvedDesc

			root, err := job.ResolveIssueRoot(db, actor)
			if err != nil {
				return err
			}

			// Both the root and the source are resolved before anything is
			// written, so a mistyped id fails without leaving a task behind.
			source, hint, err := resolveIssueFoundIn(db, actor, foundIn, cmd.Flags().Changed("found-in"))
			if err != nil {
				return err
			}

			priorChildCount, _, _ := job.CountOpenChildrenOfShortID(db, root.ShortID)
			res, err := job.RunAddKind(db, root.ShortID, args[0], desc, "", labels, actor, job.KindTask)
			if err != nil {
				return err
			}
			if source != "" {
				if err := job.RunSetFoundIn(db, res.ShortID, source, actor); err != nil {
					return err
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintln(out, res.ShortID)
			printAddAdvisories(out, res, root.ShortID, priorChildCount, true)
			if source != "" {
				fmt.Fprintf(out, "Found in: %s was found in %s\n", res.ShortID, source)
			}
			if hint != "" {
				fmt.Fprintln(out, hint)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&desc, "desc", "d", "", "issue description")
	registerFileFlag(cmd, &descFile, "description", "desc")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "label to attach (repeatable)")
	cmd.Flags().StringVar(&foundIn, "found-in", "",
		"task that surfaced this issue: an id, or \"none\" for no edge; defaults to your live claim when you hold exactly one")
	return cmd
}

// resolveIssueFoundIn decides the new issue's provenance source. An explicit
// flag wins; otherwise the caller's live claim does, but only when there is
// exactly one — with several, guessing would be worse than the hint, which
// is returned as a line to print rather than an error.
func resolveIssueFoundIn(db *sql.DB, actor, flag string, flagChanged bool) (source, hint string, err error) {
	if flagChanged {
		if strings.EqualFold(strings.TrimSpace(flag), foundInNone) || strings.TrimSpace(flag) == "" {
			return "", "", nil
		}
		src, err := job.GetTaskByShortID(db, flag)
		if err != nil {
			return "", "", err
		}
		if src == nil {
			return "", "", fmt.Errorf("issue: no such --found-in source %q", flag)
		}
		return src.ShortID, "", nil
	}

	claims, err := job.LiveClaims(db, actor)
	if err != nil {
		return "", "", err
	}
	switch len(claims) {
	case 0:
		return "", "", nil
	case 1:
		return claims[0].ShortID, "", nil
	default:
		ids := make([]string, 0, len(claims))
		for _, c := range claims {
			ids = append(ids, c.ShortID)
		}
		return "", fmt.Sprintf("  No found-in recorded: you hold %d live claims (%s). Name the source with --found-in <id>.",
			len(claims), strings.Join(ids, ", ")), nil
	}
}
