package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/spf13/cobra"
)

func newNoteCmd() *cobra.Command {
	var message string
	var messageFile string
	var resultStr string
	cmd := &cobra.Command{
		Use:   "note <id> [text]",
		Short: "Record a timestamped note on a task",
		Long:  "Record a timestamped note on a task. Notes are stored as `noted` events with actor + body, surfaced in the Notes: section of `job show`, and remain searchable. The task's description is not modified. Pass the body positionally, via -m, via -F <path> to read a file, or read from stdin with `-`. Use --result to attach a structured JSON blob to the event.",
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

			shortID := args[0]
			stdinForm := len(args) == 2 && args[1] == "-"
			positionalForm := len(args) == 2 && !stdinForm

			// The shared helper resolves -m/-F and rejects -F against either
			// the inline flag or a positional body; the -m-against-positional
			// case is note's alone, since only note takes a positional body.
			text, provided, rerr := resolveBodyFlag(cmd, bodyFlagSpec{
				Verb:          "note",
				InlineName:    "message",
				Inline:        message,
				File:          messageFile,
				HasPositional: len(args) == 2,
			})
			if rerr != nil {
				return rerr
			}
			if cmd.Flags().Changed("message") && len(args) == 2 {
				return fmt.Errorf("note: positional text, -m, -F, and the stdin form are mutually exclusive")
			}

			if !provided {
				switch {
				case stdinForm:
					b, err := io.ReadAll(cmd.InOrStdin())
					if err != nil {
						return err
					}
					text = strings.TrimRight(string(b), "\n\r")
				case positionalForm:
					text = args[1]
				default:
					return fmt.Errorf("note requires text — pass it positionally, via -m \"<text>\", via -F <path>, or via stdin (-)")
				}
			}
			if text == "" {
				return fmt.Errorf("note text is empty")
			}

			var resultRaw json.RawMessage
			if resultStr != "" {
				if !json.Valid([]byte(resultStr)) {
					return fmt.Errorf("--result: invalid JSON: %s", resultStr)
				}
				resultRaw = json.RawMessage(resultStr)
			}

			task, err := job.GetTaskByShortID(db, shortID)
			if err != nil {
				return err
			}
			if task == nil {
				return fmt.Errorf("task %q not found", shortID)
			}
			title := task.Title
			if err := job.RunNote(db, shortID, text, resultRaw, actor); err != nil {
				return err
			}
			count, preview := job.NotePreview(text)
			fmt.Fprintf(cmd.OutOrStdout(), "Noted: %s %q\n  note: %d chars · %q\n", shortID, title, count, preview)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "note text to append (supports @path and `-` for stdin)")
	registerFileFlag(cmd, &messageFile, "note text", "message")
	cmd.Flags().StringVar(&resultStr, "result", "", "structured JSON result recorded on the noted event")
	return cmd
}
