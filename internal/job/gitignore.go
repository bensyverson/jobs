package job

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// gitignoreEntry is one comment and the patterns it describes. The comment
// prints on its own line above them: gitignore has no trailing-comment
// syntax, so "pattern # comment" is a single literal pattern that matches
// nothing. Grouping lets related patterns — the WAL sidecars — share one
// comment instead of repeating a near-identical line.
type gitignoreEntry struct {
	comment  string
	patterns []string
}

// The record is .jobs/log/*.jsonl and it is meant to be committed; everything
// these two patterns cover is disposable or machine-local. The `*` on the
// cache takes its WAL sidecars, the store lock and the adoption backup with it
// (project/2026-09-01-git-native-event-log.md, "The store").
var jobGitignoreEntries = []gitignoreEntry{
	{"Jobs cache and its sidecars — disposable, rebuilt from .jobs/log", []string{".jobs.db*"}},
	{"This machine's replica id, identity and focus", []string{".jobs/local.json"}},
}

// gitDirName is what marks a checkout. A worktree and a submodule carry it as
// a regular file, so existence is the test, not its kind.
const gitDirName = ".git"

// IsGitRepo reports whether dir is the root of a git checkout. Advice about a
// .gitignore, and about committing the log, only means something inside one.
func IsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, gitDirName))
	return err == nil
}

// GitignoreHint renders the recommended entries as a block that pastes into
// a .gitignore unchanged. It is rendered from the same table the writer
// uses, so what a reader pastes is what `job gitignore` would have written.
func GitignoreHint() string {
	return strings.TrimRight(gitignoreBlock(jobGitignoreEntries), "\n")
}

func gitignoreBlock(entries []gitignoreEntry) string {
	var buf strings.Builder
	for _, e := range entries {
		if len(e.patterns) == 0 {
			continue
		}
		buf.WriteString("# ")
		buf.WriteString(e.comment)
		buf.WriteString("\n")
		for _, p := range e.patterns {
			buf.WriteString(p)
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

// gitignorePatterns flattens the entry table to the bare pattern list, in
// the order they are written.
func gitignorePatterns() []string {
	var names []string
	for _, e := range jobGitignoreEntries {
		names = append(names, e.patterns...)
	}
	return names
}

// missingGitignoreEntries partitions the entry table against the content of
// an existing .gitignore, keeping the group structure so a comment still
// prints above whichever of its patterns are absent.
func missingGitignoreEntries(existing string) (missing []gitignoreEntry, absent, present []string) {
	for _, e := range jobGitignoreEntries {
		var gap []string
		for _, p := range e.patterns {
			if gitignoreHasEntry(existing, p) {
				present = append(present, p)
			} else {
				gap = append(gap, p)
			}
		}
		if len(gap) > 0 {
			missing = append(missing, gitignoreEntry{comment: e.comment, patterns: gap})
			absent = append(absent, gap...)
		}
	}
	return missing, absent, present
}

// MissingGitignoreEntries lists the recommended patterns that dir's
// .gitignore does not already carry, so a caller can stay quiet when there
// is nothing to suggest. A missing .gitignore is simply one that carries
// nothing.
func MissingGitignoreEntries(dir string) ([]string, error) {
	existing, err := readGitignore(dir)
	if err != nil {
		return nil, err
	}
	_, absent, _ := missingGitignoreEntries(existing)
	return absent, nil
}

func readGitignore(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// PendingGitignoreEntries is WriteGitignoreEntries without the write: the
// same two lists, so a --dry-run report is rendered from the same partition
// the real run would act on rather than from a second reading of the table.
func PendingGitignoreEntries(dir string) (missing []string, alreadyPresent []string, err error) {
	existing, err := readGitignore(dir)
	if err != nil {
		return nil, nil, err
	}
	_, missing, alreadyPresent = missingGitignoreEntries(existing)
	return missing, alreadyPresent, nil
}

func WriteGitignoreEntries(dir string) (written []string, alreadyPresent []string, err error) {
	path := filepath.Join(dir, ".gitignore")
	existing, err := readGitignore(dir)
	if err != nil {
		return nil, nil, err
	}

	missing, written, alreadyPresent := missingGitignoreEntries(existing)
	if len(written) == 0 {
		return written, alreadyPresent, nil
	}

	var buf bytes.Buffer
	buf.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		buf.WriteString("\n")
	}
	if existing != "" {
		buf.WriteString("\n")
	}
	buf.WriteString("# job\n")
	buf.WriteString(gitignoreBlock(missing))

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return nil, nil, err
	}

	return written, alreadyPresent, nil
}

// gitignoreHasEntry reports whether content already has a bare-pattern line
// matching name exactly. gitignore has no trailing-comment syntax: a line
// like "name\t# comment" is one literal pattern, not name plus a comment, so
// it does NOT satisfy the entry — an old line like that is left in place and
// a correct bare line is appended alongside it rather than rewritten.
func gitignoreHasEntry(content, name string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}
