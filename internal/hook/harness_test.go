package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableExecutable_PrefersTheBrewSymlink(t *testing.T) {
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "job", "0.2.0", "bin", "job")
	if err := os.MkdirAll(filepath.Dir(cellar), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cellar, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bin", "job")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Cellar/job/0.2.0/bin/job", link); err != nil {
		t.Fatal(err)
	}
	if got := StableExecutable(cellar); got != link {
		t.Errorf("from the Cellar path: got %q, want %q", got, link)
	}
	if got := StableExecutable(link); got != link {
		t.Errorf("from the symlink: got %q, want %q", got, link)
	}
}

// When the symlink points at another version, this binary is not what the
// symlink runs, so the hook names this binary.
func TestStableExecutable_KeepsTheCellarPathWhenTheLinkPointsElsewhere(t *testing.T) {
	prefix := t.TempDir()
	mine := filepath.Join(prefix, "Cellar", "job", "0.2.0", "bin", "job")
	other := filepath.Join(prefix, "Cellar", "job", "0.3.0", "bin", "job")
	for _, p := range []string{mine, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(prefix, "bin", "job")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if got := StableExecutable(mine); got != mine {
		t.Errorf("got %q, want %q", got, mine)
	}
}

func TestStableExecutable_OutsideHomebrewKeepsThePath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "job")
	if err := os.WriteFile(p, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := StableExecutable(p); got != p {
		t.Errorf("got %q, want %q", got, p)
	}
}

func TestPlanModeCommand_QuotesAPathWithSpaces(t *testing.T) {
	if got := PlanModeCommand("/opt/homebrew/bin/job"); got != "/opt/homebrew/bin/job hook plan-mode" {
		t.Errorf("got %q", got)
	}
	if got := PlanModeCommand("/Users/a b/job"); got != "'/Users/a b/job' hook plan-mode" {
		t.Errorf("got %q", got)
	}
}

func TestParseHarness(t *testing.T) {
	for in, want := range map[string]Harness{"claude-code": ClaudeCode, "opencode": OpenCode} {
		got, err := ParseHarness(in)
		if err != nil || got != want {
			t.Errorf("ParseHarness(%q) = %q, %v", in, got, err)
		}
	}
	_, err := ParseHarness("cursor")
	if err == nil || !strings.Contains(err.Error(), "claude-code") || !strings.Contains(err.Error(), "opencode") {
		t.Errorf("unknown harness error should list the choices: %v", err)
	}
}

func testEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{Home: home, ConfigHome: filepath.Join(home, ".config")}
}

func TestPresent(t *testing.T) {
	env := testEnv(t)
	if got := env.Present(); len(got) != 0 {
		t.Errorf("empty home: present = %v", got)
	}
	if err := os.MkdirAll(filepath.Join(env.Home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := env.Present(); len(got) != 1 || got[0] != ClaudeCode {
		t.Errorf("with ~/.claude: present = %v", got)
	}
	if err := os.MkdirAll(filepath.Join(env.ConfigHome, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := env.Present(); len(got) != 2 || got[0] != ClaudeCode || got[1] != OpenCode {
		t.Errorf("with both: present = %v", got)
	}
}

func TestInstall_OpenCodeHasNothingToInstall(t *testing.T) {
	env := testEnv(t)
	for _, run := range []func() ([]string, error){
		func() ([]string, error) { return env.Install(OpenCode, testCommand) },
		func() ([]string, error) { return env.Uninstall(OpenCode) },
	} {
		lines, err := run()
		if err != nil {
			t.Fatalf("opencode: %v", err)
		}
		if len(lines) != 1 || !strings.Contains(lines[0], "OpenCode") || !strings.Contains(lines[0], "nothing") {
			t.Errorf("lines = %q", lines)
		}
	}
	entries, _ := os.ReadDir(env.Home)
	if len(entries) != 0 {
		t.Errorf("opencode install wrote files: %v", entries)
	}
}

func TestInstall_ClaudeCodeWritesUnderHome(t *testing.T) {
	env := testEnv(t)
	if _, err := env.Install(ClaudeCode, testCommand); err != nil {
		t.Fatal(err)
	}
	if cmds := planModeCommands(t, readFile(t, filepath.Join(env.Home, ".claude", "settings.json"))); len(cmds) != 1 {
		t.Errorf("commands = %q", cmds)
	}
}
