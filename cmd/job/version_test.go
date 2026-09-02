package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(version string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{}
	info.Main.Version = version
	for k, v := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: v})
	}
	return info
}

func TestVersion_FormatsAStampedBuild(t *testing.T) {
	got := formatVersion(buildInfo("v1.2.0", map[string]string{
		"vcs.revision": "b19cfe2c0ffee0000000000000000000000000000",
		"vcs.time":     "2026-09-02T16:18:00Z",
		"vcs.modified": "false",
	}))
	want := "job v1.2.0 (commit b19cfe2, 2026-09-02T16:18:00Z)"
	if got != want {
		t.Errorf("formatVersion = %q, want %q", got, want)
	}
}

func TestVersion_MarksAModifiedCheckout(t *testing.T) {
	got := formatVersion(buildInfo("(devel)", map[string]string{
		"vcs.revision": "b19cfe2c0ffee0000000000000000000000000000",
		"vcs.time":     "2026-09-02T16:18:00Z",
		"vcs.modified": "true",
	}))
	want := "job (devel) (commit b19cfe2, 2026-09-02T16:18:00Z, modified)"
	if got != want {
		t.Errorf("formatVersion = %q, want %q", got, want)
	}
}

func TestVersion_WithoutBuildInfo(t *testing.T) {
	if got := formatVersion(nil); got != "job (unknown)" {
		t.Errorf("formatVersion(nil) = %q", got)
	}
	if got := formatVersion(buildInfo("(devel)", nil)); got != "job (devel)" {
		t.Errorf("formatVersion without vcs = %q", got)
	}
}

// `job version` and `job --version` both print the same line and need no
// database.
func TestVersion_CommandAndFlagPrintTheSameLine(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("JOBS_DB", "")
	var lines []string
	for _, args := range [][]string{{"version"}, {"--version"}} {
		resetFlags()
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		line := strings.TrimSpace(out.String())
		if !strings.HasPrefix(line, "job ") {
			t.Errorf("%v printed %q", args, line)
		}
		lines = append(lines, line)
	}
	if lines[0] != lines[1] {
		t.Errorf("version and --version disagree: %q vs %q", lines[0], lines[1])
	}
}
