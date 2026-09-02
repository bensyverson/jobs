package job

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Adoption: turning a cache that predates the store into one the log
// reproduces (project/2026-09-01-git-native-event-log.md, "Adoption of a
// legacy database").
//
// A pre-store cache holds two things the log cannot: event rows whose payloads
// were never replayable, and the state those payloads produced. Adoption keeps
// both, in the only way that keeps them honest. Every legacy row becomes a log
// line marked `legacy`, which a reader records and applies nothing from, so
// `log`, `show` and the scrubber see the same history they always did. One
// `snapshot` line carries the state itself, placed in front of whatever the log
// already holds so those lines replay on top of it. The cache is then rebuilt
// from the result and compared against the original: same dump, or nothing
// happens at all.
//
// It runs on the first open of such a cache, before the connection OpenDB
// returns is even made, because every `job` invocation already migrates the
// schema silently and a conversion that needs a verb is not seamless.

const (
	// adoptBackupSuffix names the untouched legacy cache kept beside the new
	// one. The `.jobs.db*` ignore pattern covers it.
	adoptBackupSuffix = ".pre-adopt"
	// adoptCandidateSuffix names the cache built from the translated log while
	// it is still a candidate. It exists only between the rebuild and the swap.
	adoptCandidateSuffix = ".adopting"
	// adoptDiffSuffix names where an aborted adoption leaves its evidence.
	adoptDiffSuffix = ".adopt-failed"
	// adoptOffEnv suppresses adoption for one invocation, which is how a
	// legacy cache can still be read exactly as it was.
	adoptOffEnv = "JOBS_NO_ADOPT"
)

// adoptMutateSnapshot is a test seam: it corrupts the snapshot payload so the
// candidate rebuild cannot match, which is the only way to exercise the abort
// path without a real bug.
var adoptMutateSnapshot func(*SnapshotPayload)

// AdoptReport is what one adoption did.
type AdoptReport struct {
	Rep string
	// LegacyEvents is how many unpositioned rows became log lines.
	LegacyEvents int
	// Tasks is how many tasks the snapshot carries.
	Tasks int
	// Backup is where the untouched legacy cache was kept.
	Backup string
}

func (r *AdoptReport) notice() string {
	return fmt.Sprintf(
		"adopted this database into the store: %d events carried across as history, "+
			"a snapshot of %d tasks written, replica %s. The previous cache is at %s "+
			"and can be deleted once you trust the new store.",
		r.LegacyEvents, r.Tasks, r.Rep, r.Backup)
}

// nextStep is what the reader does about it. Adoption turns a cache into a
// store, and everything that follows from that is invisible from the note
// above: that the record is now a directory of text files, that those files
// belong in git, and that the cache beside them does not. An agent that
// adopted a real database had to reconstruct all three from `git status`
// (project/2026-09-02-adoption-note-and-gitignore-dry-run.md).
//
// Like `init`'s hint, it is only printed where it can be acted on: inside a
// repository, and it only names the verb while something is still unignored.
// dir is the directory holding the cache.
func (r *AdoptReport) nextStep(dir string) string {
	if !IsGitRepo(dir) {
		return ""
	}
	step := eventlog.StoreDirName + "/" + eventlog.LogDirName + " is the record now — commit it."
	missing, err := MissingGitignoreEntries(dir)
	if err == nil && len(missing) > 0 {
		step += " Run `job gitignore` to ignore the cache and its sidecars."
	}
	return step
}

// adoptIfLegacy converts the cache at path when it predates the store.
//
// It is called before OpenDB opens the connection it returns, so no handle to
// the old file survives the swap. A failed adoption is never fatal: the legacy
// cache still works exactly as it did, and blocking every command on it would
// turn a conversion into an outage. It is recorded in local.json so the next
// open does not repeat the work, and the diff is left on disk.
func adoptIfLegacy(path string) error {
	if os.Getenv(adoptOffEnv) != "" {
		return nil
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	legacy, err := probeLegacy(path)
	if err != nil || !legacy {
		return err
	}
	if diff := path + adoptDiffSuffix; fileExists(diff) {
		fmt.Fprintf(StoreNotices,
			"note: adoption of this database refused earlier; the difference is in %s. Delete that file to retry.\n",
			diff)
		return nil
	}

	report, err := adopt(path)
	if err != nil {
		fmt.Fprintf(StoreNotices, "note: %v\n", err)
		return nil
	}
	fmt.Fprintln(StoreNotices, "note: "+report.notice())
	if step := report.nextStep(filepath.Dir(path)); step != "" {
		fmt.Fprintln(StoreNotices, "note: "+step)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// probeLegacy answers "does this cache hold rows the log cannot reproduce?"
// without migrating it. A cache written before migration 0010 has no `rep`
// column at all, and every row in it is legacy; one written before the events
// table existed is not a Jobs cache and is left alone.
func probeLegacy(path string) (bool, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return false, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	var n int
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM events WHERE rep = '')").Scan(&n)
	if err == nil {
		return n == 1, nil
	}
	if !strings.Contains(err.Error(), "no such column") {
		if strings.Contains(err.Error(), "no such table") {
			return false, nil
		}
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM events)").Scan(&n); err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	return n == 1, nil
}

// adopt runs the whole conversion. The order is the design's: the candidate
// cache is built and verified BEFORE a single byte reaches a log file, because
// a log file is append-only and there is no way to take a line back.
//
//  1. Mint the replica id if this checkout has none.
//  2. Translate every unpositioned row into a `legacy` envelope, historical ts
//     and all, and mint one `snapshot` envelope carrying the current state.
//  3. Build a candidate cache at <cache>.adopting from the existing log plus
//     those envelopes.
//  4. Dump both caches and compare. ABORT POINT: any difference removes the
//     candidate and returns, with nothing appended and nothing renamed.
//  5. Append the new lines, record every watermark, persist the clock.
//  6. Rename the legacy cache to <cache>.pre-adopt and the candidate into
//     place.
func adopt(path string) (*AdoptReport, error) {
	// openCache runs the migrations and moves a legacy default identity out of
	// the config table, and that move takes the store lock — so it happens
	// before this function takes it, since the lock does not nest.
	db, err := openCache(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	lock, err := eventlog.AcquireLock(path)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	// Another process may have adopted between the probe and the lock, in
	// which case this handle points at the renamed backup.
	if still, err := probeLegacy(path); err != nil || !still {
		return nil, err
	}

	state, err := LoadLocalState(path)
	if err != nil {
		return nil, err
	}
	if state.Rep == "" {
		if state.Rep, err = eventlog.NewReplicaID(); err != nil {
			return nil, err
		}
	}

	existing, err := eventlog.ReadAll(eventlog.StoreDir(path))
	if err != nil {
		return nil, err
	}
	minted, clock, err := mintAdoptionEnvelopes(db, path, state, existing)
	if err != nil {
		return nil, err
	}

	candidate := append(append([]eventlog.Envelope(nil), existing...), minted...)
	report, err := verifyAdoption(db, path, candidate, minted)
	if err != nil {
		return nil, err
	}
	report.Rep = state.Rep

	if err := commitAdoption(db, path, state, clock, minted); err != nil {
		return nil, err
	}
	report.Backup = path + adoptBackupSuffix
	return report, nil
}

// mintAdoptionEnvelopes builds the lines adoption will append: the legacy
// history the log does not hold yet, then a snapshot of the state.
//
// Only unpositioned rows the log has no legacy line for are minted. That
// distinguishes the two ways a cache can hold such rows after the log already
// carries a snapshot. After an earlier attempt appended and then failed to
// swap, every row is already in the log, the lines are the record, and
// re-minting them would double the history — so nothing is minted. After
// `job merge` wrote another copy's tail into an adopted cache, those rows are
// new, and the merged state has to be pinned by a second snapshot placed after
// everything the log holds, or the rebuild lands on the pre-merge state.
func mintAdoptionEnvelopes(db *sql.DB, path string, state *LocalState, existing []eventlog.Envelope) ([]eventlog.Envelope, *eventlog.Clock, error) {
	clock := eventlog.NewClockWith(func() time.Time { return CurrentNowFunc() })
	clock.Load(state.LastSeen)
	adoptedBefore := false
	for _, e := range existing {
		clock.Observe(e.TS)
		if EventType(e.Type) == EventSnapshot {
			adoptedBefore = true
		}
	}

	legacy, err := legacyEnvelopes(db)
	if err != nil {
		return nil, nil, err
	}
	legacy = withoutLoggedLegacy(legacy, existing)
	if adoptedBefore && len(legacy) == 0 {
		return nil, clock, nil
	}
	for _, e := range legacy {
		clock.Observe(e.TS)
	}
	payload, err := cacheSnapshot(db)
	if err != nil {
		return nil, nil, err
	}
	if adoptMutateSnapshot != nil {
		adoptMutateSnapshot(payload)
	}
	ts := snapshotTS(existing, clock)
	if adoptedBefore {
		ts = clock.Now()
	}
	snap, err := snapshotEnvelope(payload, ts)
	if err != nil {
		return nil, nil, err
	}
	minted := append(legacy, snap)

	// The seq the appender will assign, resolved under the lock this caller
	// holds, so the candidate cache is built from the exact lines that land.
	appender, err := eventlog.OpenAppender(eventlog.StoreDir(path), path, state.Rep)
	if err != nil {
		return nil, nil, err
	}
	defer appender.Close()
	last, err := appender.LastSeqLocked()
	if err != nil {
		return nil, nil, err
	}
	for i := range minted {
		minted[i].V = eventlog.Version
		minted[i].Rep = state.Rep
		minted[i].Seq = last + uint64(i) + 1
	}
	return minted, clock, nil
}

// snapshotTS places the snapshot in the global order: immediately before the
// earliest line the log already holds, or at the clock's now when it holds
// none.
//
// A snapshot summarizes what came before it, and what came before it here is
// the legacy history — which is exactly what the snapshot is for. Anything the
// log already holds was written after that history and replays on top, which is
// the only placement that works: those lines name tasks that no `created` event
// survives for, so applying them AFTER the snapshot is what lets `job log` and
// `job show` still find them. Their effects are already in the payload, and
// every apply writes absolute values, so replaying them over it lands on the
// same state — and the dump comparison is what proves it rather than assuming
// it.
func snapshotTS(existing []eventlog.Envelope, clock *eventlog.Clock) int64 {
	earliest := int64(0)
	for _, e := range existing {
		if earliest == 0 || e.TS < earliest {
			earliest = e.TS
		}
	}
	if earliest > 1 {
		return earliest - 1
	}
	return clock.Now()
}

// withoutLoggedLegacy drops every translated row the log already carries as a
// legacy line. Lines are matched on the content a legacy line has — task,
// type, actor, ts and payload — counted, so a history that genuinely holds one
// event twice keeps both.
func withoutLoggedLegacy(rows, existing []eventlog.Envelope) []eventlog.Envelope {
	logged := map[string]int{}
	for _, e := range existing {
		if e.Legacy {
			logged[legacyLineKey(e)]++
		}
	}
	if len(logged) == 0 {
		return rows
	}
	var out []eventlog.Envelope
	for _, e := range rows {
		k := legacyLineKey(e)
		if logged[k] > 0 {
			logged[k]--
			continue
		}
		out = append(out, e)
	}
	return out
}

func legacyLineKey(e eventlog.Envelope) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", e.Task, e.Type, e.Actor, e.TS, compactJSON(e.Data))
}

// compactJSON renders a payload without whitespace, so a line read back from
// the log matches the row it was translated from however either was encoded.
func compactJSON(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return string(data)
	}
	return buf.String()
}
