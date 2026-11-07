package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLabelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label",
		Short: "Add or remove labels on one or more tasks",
		Long:  "Manage flat, free-form labels on tasks. Subcommands: 'add' and 'remove'. Names are variadic per call, idempotent, and atomic; `<id> [<id>...] with <name> [<name>...]` applies them to several tasks in one atomic call. Labels are local to each task (no inheritance). Reserved: \"decision\" — surfaces as a Decision: line in `job status` until done or canceled.",
	}
	cmd.AddCommand(newLabelAddCmd())
	cmd.AddCommand(newLabelRemoveCmd())
	return cmd
}

// labelTargets is a parsed `label add|remove` argument list.
type labelTargets struct {
	ids   []string
	names []string
	// many is the keyword form (`<id>... with <name>...`), whose JSON
	// output is an array; the single-task form keeps its object.
	many bool
}

// parseLabelArgs splits `<id> <name>...` or `<id>... with <name>...`. The
// first `with` is the separator, so a label named "with" is still reachable
// as `<id> with with`.
func parseLabelArgs(verb string, args []string) (labelTargets, error) {
	for i, a := range args {
		if a != "with" {
			continue
		}
		if i == 0 || i == len(args)-1 {
			return labelTargets{}, fmt.Errorf("usage: job label %s <id> [<id>...] with <name> [<name>...]  (or: job label %s <id> <name> [<name>...])", verb, verb)
		}
		return labelTargets{ids: args[:i], names: args[i+1:], many: true}, nil
	}
	if len(args) < 2 {
		return labelTargets{}, fmt.Errorf("usage: job label %s <id> <name> [<name>...]  (or: job label %s <id> [<id>...] with <name> [<name>...])", verb, verb)
	}
	return labelTargets{ids: args[:1], names: args[1:]}, nil
}
