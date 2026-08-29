package main

import (
	"fmt"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job identity` — group for managing the DB-level default writer identity
// and strict mode. Both subcommands are writes and therefore require --as
// (bootstrap discipline: changing who "default" means is itself an
// attributable event).
func newIdentityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage the default writer identity and strict mode",
		Long:  "Commands: `set <name>` records a DB-level default identity; `strict on|off` toggles strict mode (when on, writes always require --as). Both require --as <name>.",
	}
	cmd.AddCommand(newIdentitySetCmd())
	cmd.AddCommand(newIdentityStrictCmd())
	return cmd
}

// The two identity-required refusals, each spelled once. Verbs share the
// first; `init` needs its own because it is also explaining what the name
// will be used for and naming the way out.
const (
	identityRequiredMsg     = "identity required. Pass --as <name> before the verb."
	initIdentityRequiredMsg = "identity required. Pass --as <name> (writes without --as are attributed to it), or --strict to require --as on every write."
)

// identity verbs require --as explicitly — they change who "the default"
// means, so the bootstrap discipline is: the change itself must be
// attributed, no fall-through to the current default.
func requireAsStrict() (string, error) {
	if asFlag == "" {
		return "", fmt.Errorf("%s", identityRequiredMsg)
	}
	return asFlag, nil
}

func newIdentitySetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "Set the default writer identity for this database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := requireAsStrict(); err != nil {
				return err
			}
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()
			if err := job.SetDefaultIdentity(db, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Default identity: %s\n", args[0])
			return nil
		},
	}
}

func newIdentityStrictCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "strict <on|off>",
		Short: "Toggle strict mode (on: all writes require --as; off: default identity is used when --as is omitted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := requireAsStrict(); err != nil {
				return err
			}
			db, err := openDBFromCmd()
			if err != nil {
				return err
			}
			defer db.Close()
			switch args[0] {
			case "on":
				if err := job.SetStrict(db, true); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Strict mode: on")
			case "off":
				if err := job.SetStrict(db, false); err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), "Strict mode: off")
			default:
				return fmt.Errorf("identity strict: expected 'on' or 'off', got %q", args[0])
			}
			return nil
		},
	}
}
