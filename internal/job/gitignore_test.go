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
	if len(alreadyPresent) != len(gitignorePatterns()) {
		t.Errorf("second run alreadyPresent = %v, want all %d entries", alreadyPresent, len(gitignorePatterns()))
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

// --- The hint (rendered from the same entry table the writer uses) ---

func TestGitignoreHint_NoLineIsBothPatternAndComment(t *testing.T) {
	for line := range strings.SplitSeq(GitignoreHint(), "\n") {
		if trailingComment.MatchString(line) {
			t.Errorf("hint line pairs a pattern with a trailing comment, which gitignore takes literally: %q\nfull hint:\n%s", line, GitignoreHint())
		}
	}
}

func TestGitignoreHint_LinesAreUnindented(t *testing.T) {
	hint := GitignoreHint()
	for line := range strings.SplitSeq(hint, "\n") {
		if line != strings.TrimLeft(line, " \t") {
			t.Errorf("hint line is indented and cannot be pasted as-is: %q\nfull hint:\n%s", line, hint)
		}
	}
	for _, name := range []string{".jobs.db", ".jobs.db-shm", ".jobs.db-wal"} {
		if !containsBareLine(hint, name) {
			t.Errorf("hint missing a bare line for %q:\n%s", name, hint)
		}
	}
}

func TestGitignoreHint_MatchesTheDocumentedBlock(t *testing.T) {
	want := strings.Join([]string{
		"# Jobs event store — local by default; delete this line to share it",
		".jobs.db",
		"# SQLite WAL sidecars — always local",
		".jobs.db-shm",
		".jobs.db-wal",
	}, "\n")
	if GitignoreHint() != want {
		t.Errorf("hint:\n  got:\n%s\n  want:\n%s", GitignoreHint(), want)
	}
}

// The writer and the hint render from one table, so a file written by
// `job gitignore` and a file pasted from the hint are the same lines.
func TestGitignoreHint_AgreesWithTheWriterBlock(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := WriteGitignoreEntries(dir); err != nil {
		t.Fatalf("WriteGitignoreEntries: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	written := strings.TrimRight(string(data), "\n")
	const header = "# job\n"
	if !strings.HasPrefix(written, header) {
		t.Fatalf("written file should open with the %q header:\n%s", strings.TrimSuffix(header, "\n"), written)
	}
	block := strings.TrimPrefix(written, header)
	if block != GitignoreHint() {
		t.Errorf("writer block and hint differ:\n  writer:\n%s\n  hint:\n%s", block, GitignoreHint())
	}
}

func TestMissingGitignoreEntries(t *testing.T) {
	dir := t.TempDir()
	missing, err := MissingGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("MissingGitignoreEntries on an empty dir: %v", err)
	}
	if strings.Join(missing, ",") != ".jobs.db,.jobs.db-shm,.jobs.db-wal" {
		t.Errorf("no .gitignore at all: missing = %v, want every pattern", missing)
	}

	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".jobs.db-shm\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}
	missing, err = MissingGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("MissingGitignoreEntries: %v", err)
	}
	if strings.Join(missing, ",") != ".jobs.db,.jobs.db-wal" {
		t.Errorf("one pattern present: missing = %v, want the other two", missing)
	}

	if _, _, err := WriteGitignoreEntries(dir); err != nil {
		t.Fatalf("WriteGitignoreEntries: %v", err)
	}
	missing, err = MissingGitignoreEntries(dir)
	if err != nil {
		t.Fatalf("MissingGitignoreEntries: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("after writing, missing = %v, want none", missing)
	}
}
