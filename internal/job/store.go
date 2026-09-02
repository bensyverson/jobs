package job

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The store: .jobs/log/*.jsonl is the record, .jobs.db is a cache of it.
//
// Everything here answers one question, asked on every open: does this cache
// still reflect the log? The answer is a stat per log file against the offset
// the cache recorded when it applied that file. Equal everywhere, with no
// unknown file, means yes and the open costs nothing more. Anything else means
// rebuild (project/2026-09-01-git-native-event-log.md, "Rebuild, and when it
// runs").

// StoreState is what an open did about the log.
type StoreState string

const (
	// StoreInSync means every file's size matched its watermark.
	StoreInSync StoreState = "in sync"
	// StoreRebuilt means the cache was dropped and replayed from the log.
	StoreRebuilt StoreState = "rebuilt on open"
	// StoreLegacy means the cache holds pre-store history that no log line
	// reproduces, so it is never rebuilt until adoption has run.
	StoreLegacy StoreState = "predates the store"
	// StoreIncomplete means a log file the cache has applied is not on disk
	// and could not be written back out. Rebuilding would replay less than the
	// cache already holds, so the cache is left exactly as it is.
	StoreIncomplete StoreState = "log incomplete"
)

// StoreSync is what one open of a cache did with its store. `job status`
// renders it; nothing else depends on it.
type StoreSync struct {
	// Rep is this checkout's replica id, "" until this checkout first writes.
	Rep string
	// Files is the number of replica log files in the store.
	Files int
	// State is what the open did.
	State StoreState
	// Repairs is one line per event reconcile appended.
	Repairs []string
}

// StoreNotices is where the store prints what it did — a rebuild's repairs,
// and the notice that a database predates the store. Tests redirect it.
var StoreNotices io.Writer = os.Stderr

// syncRecords remembers the last sync per cache path, so `job status` can
// report what this process's open did without re-deriving it.
var syncRecords sync.Map

// StoreSyncOf returns what the open of db's cache did with the store, or nil
// if this process never opened it through OpenDB.
func StoreSyncOf(db dbtx) *StoreSync {
	path, err := CachePathOf(db)
	if err != nil {
		return nil
	}
	if v, ok := syncRecords.Load(path); ok {
		return v.(*StoreSync)
	}
	return nil
}

// legacyNotice is printed instead of a rebuild when the cache still holds
// pre-store history. Adoption is the leaf that fixes it.
const legacyNotice = "note: this database predates the store, so its cache is not rebuilt from .jobs/log; adoption is pending"

// setWatermark records how much of a replica's log file the cache has applied.
func setWatermark(tx dbtx, rep string, offset int64) error {
	_, err := tx.Exec(
		"INSERT INTO log_watermarks (rep, offset) VALUES (?, ?) ON CONFLICT(rep) DO UPDATE SET offset = excluded.offset",
		rep, offset,
	)
	return err
}

// readWatermarks reads every recorded offset.
func readWatermarks(db dbtx) (map[string]int64, error) {
	rows, err := db.Query("SELECT rep, offset FROM log_watermarks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var rep string
		var off int64
		if err := rows.Scan(&rep, &off); err != nil {
			return nil, err
		}
		out[rep] = off
	}
	return out, rows.Err()
}

// logFileSize is the byte length a fully applied cache records as its
// watermark. A file that does not exist is zero rather than an error: an
// appender creates it on first write.
func logFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.Size(), nil
}

// hasLegacyRows reports whether the cache holds events from before the store —
// rows with no position, whose payloads were never replayable. A cache holding
// any of them carries state the log cannot reproduce, so rebuilding it would
// destroy that state.
func hasLegacyRows(db dbtx) (bool, error) {
	var n int
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM events WHERE rep = '')").Scan(&n); err != nil {
		return false, err
	}
	return n == 1, nil
}

// inSync reports whether every file's size equals its watermark and no file is
// unknown. A watermark with no file is also out of sync: the file was deleted
// or has not been pulled yet, and either way the cache must be rebuilt from
// what is actually there.
func inSync(files []eventlog.File, marks map[string]int64) bool {
	if len(files) != len(marks) {
		return false
	}
	for _, f := range files {
		if mark, ok := marks[f.Rep]; !ok || mark != f.Size {
			return false
		}
	}
	return true
}

// syncStore is the open-time reconciliation between cache and log. It runs
// after the migrations and the backfills, on every OpenDB.
func syncStore(db *sql.DB, path string) (*StoreSync, error) {
	storeDir := eventlog.StoreDir(path)
	local, err := LoadLocalState(path)
	if err != nil {
		return nil, err
	}
	sync := &StoreSync{Rep: local.Rep}

	files, err := eventlog.Files(storeDir)
	if err != nil {
		return nil, err
	}
	marks, err := readWatermarks(db)
	if err != nil {
		return nil, err
	}
	sync.Files = len(files)

	// The hot path: a stat per file against the offset the cache applied.
	if len(files) > 0 && inSync(files, marks) {
		sync.State = StoreInSync
		return sync, nil
	}

	// No file for a replica whose events the cache already holds. That is
	// either a cache written before the log files existed — this replica's
	// history since the apply refactor, never appended anywhere — or a copy of
	// a cache taken without its store. Either way the events are here and the
	// file is not, so write the file rather than replay less than the cache
	// holds.
	missing, err := repsWithoutFiles(db, files)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		if err := bootstrapFiles(db, path, missing); err != nil {
			return nil, err
		}
		if files, err = eventlog.Files(storeDir); err != nil {
			return nil, err
		}
		sync.Files = len(files)
		if missing, err = repsWithoutFiles(db, files); err != nil {
			return nil, err
		}
		if marks, err = readWatermarks(db); err != nil {
			return nil, err
		}
	}
	if inSync(files, marks) {
		sync.State = StoreInSync
		return sync, nil
	}
	if len(missing) > 0 {
		sync.State = StoreIncomplete
		fmt.Fprintf(StoreNotices,
			"note: no log file for replica %s, whose events this cache holds; the cache was left as it is rather than rebuilt from less\n",
			missing[0])
		return sync, nil
	}

	legacy, err := hasLegacyRows(db)
	if err != nil {
		return nil, err
	}
	if legacy {
		sync.State = StoreLegacy
		fmt.Fprintln(StoreNotices, legacyNotice)
		return sync, nil
	}

	repairs, err := rebuildAndReconcile(db, path, marks, local.Rep)
	if err != nil {
		return nil, err
	}
	sync.State = StoreRebuilt
	sync.Repairs = repairs
	for _, r := range repairs {
		fmt.Fprintln(StoreNotices, r)
	}
	if files, err = eventlog.Files(storeDir); err == nil {
		sync.Files = len(files)
	}
	return sync, nil
}

// repsWithoutFiles lists the replicas whose positioned events the cache holds
// but which have no log file on disk.
func repsWithoutFiles(db *sql.DB, files []eventlog.File) ([]string, error) {
	have := map[string]bool{}
	for _, f := range files {
		have[f.Rep] = true
	}
	rows, err := db.Query("SELECT DISTINCT rep FROM events WHERE rep != '' ORDER BY rep")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var rep string
		if err := rows.Scan(&rep); err != nil {
			return nil, err
		}
		if !have[rep] {
			out = append(out, rep)
		}
	}
	return out, rows.Err()
}

// bootstrapFiles writes each replica's cached events out to its log file.
//
// Only a gapless run from seq 1 is written. A cache whose sequence has holes —
// purge erases the purged subtree's event rows — cannot be turned back into an
// append-only file, and inventing one would produce a log that reads as
// truncated on every other machine. That case says so and leaves the cache
// alone; adoption is what handles it properly.
func bootstrapFiles(db *sql.DB, path string, reps []string) error {
	lock, err := eventlog.AcquireLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()

	for _, rep := range reps {
		events, err := ownCachedEnvelopes(db, rep)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			continue
		}
		gapped := false
		for i, e := range events {
			if e.Seq != uint64(i+1) {
				fmt.Fprintf(StoreNotices,
					"note: this cache's events for replica %s are not a gapless run (seq %d at line %d), so no log file was written for them\n",
					rep, e.Seq, i+1)
				gapped = true
				break
			}
		}
		if gapped {
			continue
		}
		if err := writeBootstrapFile(db, path, rep, events); err != nil {
			return err
		}
	}
	return nil
}

func writeBootstrapFile(db *sql.DB, path, rep string, events []eventlog.Envelope) error {
	appender, err := eventlog.OpenAppender(eventlog.StoreDir(path), path, rep)
	if err != nil {
		return err
	}
	defer appender.Close()
	last, err := appender.LastSeqLocked()
	if err != nil {
		return err
	}
	if last != 0 {
		// Another process bootstrapped between the stat and the lock.
		return nil
	}
	refs := make([]*eventlog.Envelope, len(events))
	for i := range events {
		refs[i] = &events[i]
	}
	if err := appender.AppendLocked(refs); err != nil {
		return err
	}
	size, err := logFileSize(appender.Path())
	if err != nil {
		return err
	}
	return setWatermark(db, rep, size)
}

// ownCachedEnvelopes reads one replica's positioned events out of the cache,
// in seq order.
func ownCachedEnvelopes(db *sql.DB, rep string) ([]eventlog.Envelope, error) {
	rows, err := db.Query(`
		SELECT e.seq, e.ts, e.actor, e.event_type,
		       COALESCE(t.short_id, ''), COALESCE(e.detail, '')
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		WHERE e.rep = ? ORDER BY e.seq`, rep)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventlog.Envelope
	for rows.Next() {
		var e eventlog.Envelope
		var seq int64
		var typ, detail string
		if err := rows.Scan(&seq, &e.TS, &e.Actor, &typ, &e.Task, &detail); err != nil {
			return nil, err
		}
		e.V = eventlog.Version
		e.Rep = rep
		e.Seq = uint64(seq)
		e.Type = eventlog.Type(typ)
		if detail != "" {
			e.Data = json.RawMessage(detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// HasStore reports whether a log directory sits beside the cache named by
// cachePath. A clone carries the log and no cache, and that is enough to work
// with: the cache is built from it on the first command.
func HasStore(cachePath string) bool {
	info, err := os.Stat(eventlog.LogDir(eventlog.StoreDir(cachePath)))
	return err == nil && info.IsDir()
}

// StoreEventCount counts the lines in every log file under the store beside
// cachePath. It reads the files, so it belongs to `job status` rather than to
// the open path.
func StoreEventCount(cachePath string) (int, error) {
	events, err := eventlog.ReadAll(eventlog.StoreDir(cachePath))
	if err != nil {
		return 0, err
	}
	return len(events), nil
}
