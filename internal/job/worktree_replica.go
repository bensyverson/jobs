package job

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A linked git worktree is a second checkout of the same repository, and a
// replica id lives in .jobs/local.json, which is gitignored — so a worktree
// that opens its own store for the first time becomes a new replica whose
// writes the main checkout cannot see until the branch merges. That is the
// designed behavior; what bit agents was not knowing it had happened.

// mainStoreOfWorktree reports where the main checkout keeps the store that
// corresponds to cachePath, when cachePath sits inside a linked worktree. ok
// is false in the main checkout, outside git, for a bare or relocated git
// directory (no main working tree to name), and for a store that lives
// outside the worktree, which the worktree does not own.
func mainStoreOfWorktree(cachePath string) (string, bool) {
	abs, err := filepath.Abs(cachePath)
	if err != nil {
		return "", false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", false
	}
	out, err := gitCommand(dir, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--git-dir", "--git-common-dir").Output()
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 3 {
		return "", false
	}
	toplevel, gitDir, commonDir := lines[0], lines[1], lines[2]
	if gitDir == commonDir || filepath.Base(commonDir) != ".git" {
		return "", false
	}
	rel, err := filepath.Rel(toplevel, filepath.Join(dir, filepath.Base(abs)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(filepath.Dir(commonDir), rel), true
}

// warnIfWorktreeReplica tells w that the replica just minted for cachePath
// belongs to a linked worktree. It is called only at the moment of minting,
// so a later open of the same replica stays quiet by construction.
func warnIfWorktreeReplica(w io.Writer, cachePath string) {
	mainStore, ok := mainStoreOfWorktree(cachePath)
	if !ok {
		return
	}
	fmt.Fprintf(w, "warning: this git worktree is now its own replica; its writes stay invisible to the "+
		"main checkout's store until the branch merges. "+
		"Pass --db %s to write to the shared store instead.\n", mainStore)
}

// gitCommand runs git in dir with the repository-locating variables cleared.
// A git hook exports GIT_DIR, GIT_INDEX_FILE and friends for its own repo, and
// git obeys them over dir, so a job run from a hook would otherwise describe
// the hook's repository rather than the store's.
func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); !gitRepoEnv[name] {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	return cmd
}

// gitRepoEnv names the variables that point git at a repository other than
// the one it would discover from its working directory.
var gitRepoEnv = map[string]bool{
	"GIT_DIR":                          true,
	"GIT_WORK_TREE":                    true,
	"GIT_INDEX_FILE":                   true,
	"GIT_COMMON_DIR":                   true,
	"GIT_OBJECT_DIRECTORY":             true,
	"GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_PREFIX":                       true,
}
