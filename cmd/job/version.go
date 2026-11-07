package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// releaseVersion is the version this source states; scripts/release reads it.
// Only a build from a release tarball shows it, as that has no stamp of its own.
const releaseVersion = "0.3.1"

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

// develVersion is what the toolchain stamps when it knows no module version.
const develVersion = "(devel)"

func versionLine() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return formatVersion(nil)
	}
	return formatVersion(info)
}

// formatVersion renders "job <module version> (commit <short>, <time>[, modified])".
// A binary installed by module version has no vcs settings and prints that
// version alone; one built from a release tarball has neither and prints
// releaseVersion; one built without build info at all prints "(unknown)".
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
		if version == develVersion {
			version = releaseVersion
		}
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
