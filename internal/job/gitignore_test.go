package job

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// trailingComment matches a non-comment line that carries a "#" later on
// the same line — the shape gitignore treats as one literal pattern rather
// than pattern-plus-comment.
var trailingComment = regexp.MustCompile(`^[^#\s].*#`)

func containsBareLine(content, name string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

func TestWriteGitignoreEntries_NoTrailingCommentLines(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := WriteGitignoreEntries(dir); err != nil {
		t.Fatalf("WriteGitignoreEntries: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)

	for line := range strings.SplitSeq(content, "\n") {
		if trailingComment.MatchString(line) {
			t.Errorf("line pairs a pattern with a trailing comment, which gitignore takes literally: %q\nfull content:\n%s", line, content)
		}
	}
}

func TestWriteGitignoreEntries_WritesBareLinesForAllRecommendedEntries(t *testing.T) {
	dir := t.TempDir()
	written, alreadyPresent, err := WriteGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("WriteGitignoreEntries: %v", err)
	}
	if len(alreadyPresent) != 0 {
		t.Fatalf("expected nothing already present on a fresh dir, got %v", alreadyPresent)
	}

	wantWritten := []string{".jobs.db", ".jobs.db-shm", ".jobs.db-wal"}
	if strings.Join(written, ",") != strings.Join(wantWritten, ",") {
		t.Fatalf("written = %v, want %v", written, wantWritten)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, name := range wantWritten {
		if !containsBareLine(content, name) {
			t.Errorf(".gitignore missing a bare line for %q:\n%s", name, content)
		}
	}
}

func TestWriteGitignoreEntries_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := WriteGitignoreEntries(dir); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore after first write: %v", err)
	}

	written, alreadyPresent, err := WriteGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("second run wrote entries, want none: %v", written)
	}
	if len(alreadyPresent) != len(jobGitignoreEntries) {
		t.Errorf("second run alreadyPresent = %v, want all %d entries", alreadyPresent, len(jobGitignoreEntries))
	}

	second, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore after second write: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("second run changed the file:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestWriteGitignoreEntries_OldBrokenInlineCommentLineDoesNotSatisfyTheEntry(t *testing.T) {
	dir := t.TempDir()
	broken := ".jobs.db-shm\t# SQLite WAL index (always local)\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(broken), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	written, _, err := WriteGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("WriteGitignoreEntries: %v", err)
	}
	if !contains(written, ".jobs.db-shm") {
		t.Errorf("expected the old broken -shm line to NOT satisfy the entry (so a bare line gets appended), written = %v", written)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if !strings.HasPrefix(content, broken) {
		t.Errorf("the old broken line should be left in place, not rewritten or removed:\n%s", content)
	}
	if !containsBareLine(content, ".jobs.db-shm") {
		t.Errorf("expected a new bare .jobs.db-shm line to be appended:\n%s", content)
	}
}
