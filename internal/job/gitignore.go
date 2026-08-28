package job

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

const GitignoreHint = `Recommended .gitignore entries:
  .jobs.db          # Jobs event store (local by default; remove this line to share it)
  .jobs.db-shm      # SQLite WAL index (always local)
  .jobs.db-wal      # SQLite WAL journal (always local)

Run: job init --gitignore  to write these for you.`

type gitignoreEntry struct {
	name    string
	comment string
}

var jobGitignoreEntries = []gitignoreEntry{
	{".jobs.db", "Jobs event store (local by default; remove this line to share it)"},
	{".jobs.db-shm", "SQLite WAL index (always local)"},
	{".jobs.db-wal", "SQLite WAL journal (always local)"},
}

func WriteGitignoreEntries(dir string) (written []string, alreadyPresent []string, err error) {
	path := filepath.Join(dir, ".gitignore")
	existing := ""
	if data, readErr := os.ReadFile(path); readErr == nil {
		existing = string(data)
	} else if !os.IsNotExist(readErr) {
		return nil, nil, readErr
	}

	for _, e := range jobGitignoreEntries {
		if gitignoreHasEntry(existing, e.name) {
			alreadyPresent = append(alreadyPresent, e.name)
		} else {
			written = append(written, e.name)
		}
	}

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
	for _, e := range jobGitignoreEntries {
		if gitignoreHasEntry(existing, e.name) {
			continue
		}
		buf.WriteString("# ")
		buf.WriteString(e.comment)
		buf.WriteString("\n")
		buf.WriteString(e.name)
		buf.WriteString("\n")
	}

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
