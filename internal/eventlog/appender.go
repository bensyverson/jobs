package eventlog

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// Appender appends to one replica's log file. It assigns each envelope's
// version, replica and a gapless seq continuing from the last complete line in
// the file, writes the batch as whole lines, and fsyncs.
//
// The seq is resolved under the store lock on every batch, not cached at open,
// so two Appenders for the same replica — in one process or two — never repeat
// a seq or interleave a line.
type Appender struct {
	rep       string
	path      string
	cachePath string
	f         *os.File
}

// OpenAppender opens rep's log file under storeDir for append, creating the log
// directory if needed. cachePath names the cache the store lock sits beside.
func OpenAppender(storeDir, cachePath, rep string) (*Appender, error) {
	if !ValidReplicaID(rep) {
		return nil, fmt.Errorf("eventlog: %q is not a replica id", rep)
	}
	path := LogPath(storeDir, rep)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("eventlog: open %s: %w", path, err)
	}
	return &Appender{rep: rep, path: path, cachePath: cachePath, f: f}, nil
}

// Rep returns the replica this appender writes for.
func (a *Appender) Rep() string { return a.rep }

// Path returns the log file this appender writes to.
func (a *Appender) Path() string { return a.path }

// Append takes the store lock and writes evs as one batch, assigning V, Rep and
// Seq in place. It is a no-op for an empty batch.
func (a *Appender) Append(evs []*Envelope) error {
	if len(evs) == 0 {
		return nil
	}
	l, err := AcquireLock(a.cachePath)
	if err != nil {
		return err
	}
	defer l.Release()
	return a.AppendLocked(evs)
}

// AppendLocked is Append for a caller that already holds the store lock — the
// command path holds it across append plus apply. Calling Append while holding
// the lock would deadlock.
func (a *Appender) AppendLocked(evs []*Envelope) error {
	if len(evs) == 0 {
		return nil
	}
	last, err := a.lastSeq()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	for _, e := range evs {
		last++
		e.V = Version
		e.Rep = a.rep
		e.Seq = last
		line, err := Marshal(*e)
		if err != nil {
			return err
		}
		buf.Write(line)
	}
	// One write of whole lines: a concurrent appender under the same lock can
	// never land between two of them.
	if _, err := a.f.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("eventlog: append to %s: %w", a.path, err)
	}
	if err := a.f.Sync(); err != nil {
		return fmt.Errorf("eventlog: fsync %s: %w", a.path, err)
	}
	return nil
}

// LastSeq returns the seq of the last line in the file, 0 if it is empty. It
// takes the store lock, so it reflects other writers.
func (a *Appender) LastSeq() (uint64, error) {
	l, err := AcquireLock(a.cachePath)
	if err != nil {
		return 0, err
	}
	defer l.Release()
	return a.lastSeq()
}

// lastSeq re-reads the file rather than trusting a cached value: another
// Appender or another process may have written since the last batch. The caller
// holds the store lock.
func (a *Appender) lastSeq() (uint64, error) {
	raw, err := os.ReadFile(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("eventlog: read %s: %w", a.path, err)
	}
	if len(raw) == 0 {
		return 0, nil
	}
	if raw[len(raw)-1] != '\n' {
		// Appending onto a half-written line would corrupt it beyond repair.
		return 0, &ParseError{
			Path: a.path,
			Line: bytes.Count(raw, []byte("\n")) + 1,
			Err:  errTruncated,
		}
	}
	body := raw[:len(raw)-1]
	if i := bytes.LastIndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	e, err := Unmarshal(body)
	if err != nil {
		return 0, &ParseError{
			Path: a.path,
			Line: bytes.Count(raw, []byte("\n")),
			Err:  err,
		}
	}
	if e.Rep != a.rep {
		return 0, &ParseError{
			Path: a.path,
			Line: bytes.Count(raw, []byte("\n")),
			Err:  fmt.Errorf("event names replica %q, not %q", e.Rep, a.rep),
		}
	}
	return e.Seq, nil
}

// Close releases the file handle. It does not touch the store lock.
func (a *Appender) Close() error {
	if a.f == nil {
		return nil
	}
	err := a.f.Close()
	a.f = nil
	return err
}
