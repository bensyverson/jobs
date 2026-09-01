package eventlog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// StoreDirName is the tracked directory beside the cache that holds the record.
const StoreDirName = ".jobs"

// LogDirName is the subdirectory of the store holding one file per replica.
const LogDirName = "log"

// LogExt is the extension of a replica's log file.
const LogExt = ".jsonl"

// LockExt is appended to the cache path to name the store lock. It sits beside
// the cache rather than inside the store so the existing .jobs.db* ignore
// pattern covers it.
const LockExt = ".lock"

// StoreDir returns the store directory beside the cache named by cachePath.
// --db keeps its meaning: it names the cache, and the store is the .jobs/
// directory next to it.
func StoreDir(cachePath string) string {
	return filepath.Join(filepath.Dir(cachePath), StoreDirName)
}

// LogDir returns the directory holding the replica log files.
func LogDir(storeDir string) string {
	return filepath.Join(storeDir, LogDirName)
}

// LogPath returns the path of rep's log file inside storeDir.
func LogPath(storeDir, rep string) string {
	return filepath.Join(LogDir(storeDir), rep+LogExt)
}

// LockPath returns the path of the store lock for cachePath's store.
func LockPath(cachePath string) string { return cachePath + LockExt }

// Lock is an exclusive flock on the store lock file, held around append plus
// apply so concurrent job processes on one machine serialize.
//
// The lock is per open file description, so two Locks in one process contend
// exactly as two processes do.
type Lock struct {
	f *os.File
}

// AcquireLock blocks until it holds the store lock for cachePath's store.
func AcquireLock(cachePath string) (*Lock, error) {
	path := LockPath(cachePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("eventlog: create store lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("eventlog: open store lock %s: %w", path, err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		f.Close()
		return nil, fmt.Errorf("eventlog: lock %s: %w", path, err)
	}
}

// Release drops the lock and closes its file.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("eventlog: unlock: %w", err)
	}
	return closeErr
}
