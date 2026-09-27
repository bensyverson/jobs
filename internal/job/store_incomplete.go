package job

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The cache can hold a replica's events with no log file behind them: a cache
// written before the files existed, or one copied without its store, whose
// run the bootstrap could not write out (a gap left by purge, or a run that
// declares a lower store format than its lines need). Those events exist
// nowhere else, so two things must not happen until the file is back:
//
//   - a rebuild, which replays only the files and would drop them;
//   - a write by that replica, which primes its seq from the file and would
//     mint numbers the cache already holds — two events at one position in
//     the log, one of which every later rebuild silently loses.

// CacheAheadOfLogError refuses a write whose seq the cache already holds: the
// replica's log file ends before the cache's own run for that replica does.
type CacheAheadOfLogError struct {
	Rep string
	// FileSeq is the last seq in the replica's log file, 0 when there is none.
	FileSeq uint64
	// CacheSeq is the highest seq the cache holds for the replica.
	CacheSeq uint64
	// File is the log file, as the user would name it from the checkout.
	File string
}

func (e *CacheAheadOfLogError) Error() string {
	return fmt.Sprintf(
		"refusing to write: this cache holds replica %s's events up to seq %d, but %s ends at seq %d, "+
			"so the next event would reuse seq %d and put two events at one position in the log.\n"+
			"Restore %s (from git, or from the machine that wrote it) and run the command again; reads keep working meanwhile. "+
			"If no copy of it exists, moving .jobs.db aside rebuilds the cache from the log without the events only this cache holds.",
		e.Rep, e.CacheSeq, e.File, e.FileSeq, e.FileSeq+1, e.File)
}

// LogIncompleteError refuses a rebuild while the cache holds events for a
// replica that has no log file.
type LogIncompleteError struct {
	// Reps are the replicas without a file, sorted.
	Reps []string
	// Files are their log files, as the user would name them.
	Files []string
}

func (e *LogIncompleteError) Error() string {
	return fmt.Sprintf(
		"the cache holds events for replica %s but %s is missing, so it was not rebuilt: replaying the log would drop them. "+
			"Restore the file (from git, or from the machine that wrote it) and the next command rebuilds.",
		strings.Join(e.Reps, ", "), strings.Join(e.Files, ", "))
}

func newLogIncompleteError(cachePath string, reps []string) *LogIncompleteError {
	e := &LogIncompleteError{Reps: slices.Sorted(slices.Values(reps))}
	for _, rep := range e.Reps {
		e.Files = append(e.Files, displayLogPath(cachePath, rep))
	}
	return e
}

// displayLogPath names rep's log file relative to the checkout holding the
// cache, which is where the user runs git from.
func displayLogPath(cachePath, rep string) string {
	abs := eventlog.LogPath(eventlog.StoreDir(cachePath), rep)
	if rel, err := filepath.Rel(filepath.Dir(cachePath), abs); err == nil {
		return rel
	}
	return abs
}

// cachedLastSeq is the highest seq the cache holds for rep, 0 for none.
func cachedLastSeq(db dbtx, rep string) (uint64, error) {
	var last int64
	if err := db.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events WHERE rep = ?", rep).Scan(&last); err != nil {
		return 0, err
	}
	return uint64(last), nil
}

// refuseSeqReuse is the check every writer makes after reading the file's last
// seq under the store lock. The file is ahead of the cache after a crash or a
// rekey, which is fine; the cache ahead of the file means the next seq minted
// is one it already holds.
func refuseSeqReuse(db dbtx, cachePath, rep string, fileLast uint64) error {
	cached, err := cachedLastSeq(db, rep)
	if err != nil {
		return err
	}
	if cached <= fileLast {
		return nil
	}
	return &CacheAheadOfLogError{Rep: rep, FileSeq: fileLast, CacheSeq: cached, File: displayLogPath(cachePath, rep)}
}
