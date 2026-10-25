package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	job "github.com/bensyverson/jobs/internal/job"
)

// `job config` reads and writes the repo's committed settings,
// .jobs/config.json. It needs no identity: a setting is not an event, and
// the commit that changes the file is what attributes it.
func newConfigCmd() *cobra.Command {
	var unset bool
	cmd := &cobra.Command{
		Use:   "config [<setting> [<value>]]",
		Short: "Read or change the repo's shared settings (.jobs/config.json)",
		Long: `Read or change the repo's shared settings. They live in .jobs/config.json, beside .jobs/log/, and are meant to be committed: every checkout and worktree of the repo shares them. Machine-local state (identity, strict mode, focus) stays in the gitignored .jobs/local.json, and settings never enter the event log.

  job config                         list every setting and its effective value
  job config claim-duration          print the effective value, and whether it is the default
  job config claim-duration 2h       set it
  job config claim-duration --unset  remove it, so the default applies again

Settings:
  claim-duration   the claim window for 'claim' with no duration (and claim --next, done --claim-next, reopen's auto-claim), and the window a heartbeat renews to. Default 30m. Units: s, m, h, d.

The file is plain JSON ({"claim_duration": "2h"}) and can be edited by hand; an invalid value is rejected, naming the file, by every verb that reads it.`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cachePath, err := existingDBPath()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if len(args) == 0 {
				if unset {
					return fmt.Errorf("config: --unset needs a setting name")
				}
				for _, k := range job.ConfigKeys() {
					if err := printConfigValue(out, cachePath, k); err != nil {
						return err
					}
				}
				return nil
			}

			key, err := job.ParseConfigKey(args[0])
			if err != nil {
				return err
			}
			switch {
			case unset && len(args) == 2:
				return fmt.Errorf("config: pass a value or --unset, not both")
			case unset:
				if err := job.UnsetRepoConfig(cachePath, key); err != nil {
					return err
				}
			case len(args) == 2:
				if err := job.SetRepoConfig(cachePath, key, args[1]); err != nil {
					return err
				}
			}
			return printConfigValue(out, cachePath, key)
		},
	}
	cmd.Flags().BoolVar(&unset, "unset", false, "remove the setting, so the default applies again")
	return cmd
}

// printConfigValue prints one setting's effective value and where it came
// from: the file that sets it, or the built-in default.
func printConfigValue(out io.Writer, cachePath string, key job.ConfigKey) error {
	v, err := job.GetRepoConfig(cachePath, key)
	if err != nil {
		return err
	}
	if v.IsDefault {
		fmt.Fprintf(out, "%s: %s (default)\n", v.Key, v.Value)
		return nil
	}
	fmt.Fprintf(out, "%s: %s (set in %s)\n", v.Key, v.Value, job.RepoConfigPath(cachePath))
	return nil
}
