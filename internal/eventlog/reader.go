package eventlog

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// errTruncated marks a file whose last line was never finished — a crash
// between write and fsync, or a hand edit.
var errTruncated = errors.New("line is truncated (the file does not end in a newline)")

// ParseError names the file and the 1-based line number of a line that could
// not be read. A truncated final line — no trailing newline, or invalid JSON —
// is reported the same way.
type ParseError struct {
	Path string
	Line int
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s:%d: %v", e.Path, e.Line, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// SeqGapError reports a break in a file's gapless per-replica sequence, which
// means the file is incomplete: an event was lost or the file was edited.
type SeqGapError struct {
	Path     string
	Line     int
	Expected uint64
	Got      uint64
}

func (e *SeqGapError) Error() string {
	return fmt.Sprintf("%s:%d: seq %d where %d was expected; the log file is missing events", e.Path, e.Line, e.Got, e.Expected)
}

// File describes one replica's log file. Size is the byte offset a fully
// applied cache would record as its watermark.
type File struct {
	Rep  string
	Path string
	Size int64
}

// Files lists the replica log files under storeDir, sorted by replica id. A
// store with no log directory yet lists nothing and is not an error. Names that
// are not "<replica id>.jsonl" are ignored.
func Files(storeDir string) ([]File, error) {
	dir := LogDir(storeDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("eventlog: read %s: %w", dir, err)
	}
	var out []File
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		rep, ok := strings.CutSuffix(entry.Name(), LogExt)
		if !ok || !ValidReplicaID(rep) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("eventlog: stat %s: %w", entry.Name(), err)
		}
		out = append(out, File{Rep: rep, Path: LogPath(storeDir, rep), Size: info.Size()})
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Rep, b.Rep) })
	return out, nil
}

// ReadFile parses one replica's log file in file order, checking that seq runs
// gaplessly from 1 and that every line names the file's replica.
func ReadFile(path string) ([]Envelope, error) {
	rep, ok := strings.CutSuffix(filepath.Base(path), LogExt)
	if !ok || !ValidReplicaID(rep) {
		return nil, fmt.Errorf("eventlog: %s is not named for a replica", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eventlog: read %s: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, &ParseError{Path: path, Line: bytes.Count(raw, []byte("\n")) + 1, Err: errTruncated}
	}

	lines := bytes.Split(raw[:len(raw)-1], []byte("\n"))
	out := make([]Envelope, 0, len(lines))
	for i, line := range lines {
		n := i + 1
		e, err := Unmarshal(line)
		if err != nil {
			return nil, &ParseError{Path: path, Line: n, Err: err}
		}
		if e.Rep != rep {
			return nil, &ParseError{Path: path, Line: n, Err: fmt.Errorf("event names replica %q, but the file is %s's", e.Rep, rep)}
		}
		if want := uint64(n); e.Seq != want {
			return nil, &SeqGapError{Path: path, Line: n, Expected: want, Got: e.Seq}
		}
		out = append(out, e)
	}
	return out, nil
}

// ReadAll parses every replica file under storeDir and returns the union sorted
// by position.
func ReadAll(storeDir string) ([]Envelope, error) {
	files, err := Files(storeDir)
	if err != nil {
		return nil, err
	}
	var out []Envelope
	for _, f := range files {
		evs, err := ReadFile(f.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, evs...)
	}
	Sort(out)
	return out, nil
}
