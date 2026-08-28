package main

import (
	"fmt"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newReleaseCmd() *cobra.Command {
	return buildReleaseCmd("release")
}

// newUnclaimCmd is a convenience alias for `release` — agents reach for
// `unclaim` instinctively, so absorb it at zero cost.
func newUnclaimCmd() *cobra.Command {
	cmd := buildReleaseCmd("unclaim")
	cmd.Short = "Release a claimed task (alias for `release`)"
	return cmd
}

func buildReleaseCmd(use string) *cobra.Command {
	var note string
	var noteFile string
	cmd := &cobra.Command{
		Use:   use + " <id>",
		Short: "Release a claimed task",
		Long:  "Release a claim, returning the task to available status. Pass -m \"<text>\" to record a note before releasing, or -F <path> to read it from a file.",
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

			resolved, _, rerr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:       use,
				InlineName: "message",
				Inline:     note,
				File:       noteFile,
			})
			if rerr != nil {
				return rerr
			}

			if err := job.RunRelease(db, args[0], resolved, actor); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Released: %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVarP(&note, "message", "m", "", "record a note before releasing (supports @path and `-` for stdin)")
	registerFileFlag(cmd, &noteFile, "note", "message")
	return cmd
}
