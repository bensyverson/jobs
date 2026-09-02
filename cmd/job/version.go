package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// `job version` — which build this is. Both the module version and the commit
// come from the build info the Go toolchain stamps into every binary built
// from a checkout, so there is nothing to pass at build time and nothing to
// forget. It touches no database and records no event.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and commit this binary was built from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionLine())
			return err
		},
	}
}

func versionLine() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return formatVersion(nil)
	}
	return formatVersion(info)
}

// formatVersion renders "job <module version> (commit <short>, <time>[, modified])".
// A binary built outside a checkout has no vcs settings and prints the module
// version alone; one built without build info at all prints "(unknown)".
func formatVersion(info *debug.BuildInfo) string {
	if info == nil {
		return "job (unknown)"
	}
	version := info.Main.Version
	if version == "" {
		version = "(unknown)"
	}
	var revision, at string
	modified := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			at = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return "job " + version
	}
	if len(revision) > 7 {
		revision = revision[:7]
	}
	detail := "commit " + revision
	if at != "" {
		detail += ", " + at
	}
	if modified {
		detail += ", modified"
	}
	return fmt.Sprintf("job %s (%s)", version, detail)
}
