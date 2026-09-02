package job

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The half of adoption that touches disk: proving the candidate cache is the
// same database, then appending the lines and swapping the files. Everything
// here runs under the store lock adopt() holds.

// verifyAdoption builds the candidate cache and compares it against the legacy
// one. This is the abort point: on any difference the candidate is removed, the
// diff is written beside the cache, and the caller has appended nothing.
func verifyAdoption(db *sql.DB, path string, candidate, minted []eventlog.Envelope) (*AdoptReport, error) {
	target := path + adoptCandidateSuffix
	removeCache(target)

	fresh, err := openCache(target)
	if err != nil {
		return nil, err
	}
	if err := rebuildFrom(fresh, candidate); err != nil {
		fresh.Close()
		removeCache(target)
		return nil, fmt.Errorf("adoption aborted: replaying the translated log failed, so nothing was changed: %w", err)
	}

	before, err := dumpHistory(db)
	if err != nil {
		fresh.Close()
		removeCache(target)
		return nil, err
	}
	after, err := dumpHistory(fresh)
	if err != nil {
		fresh.Close()
		removeCache(target)
		return nil, err
	}
	if before != after {
		fresh.Close()
		removeCache(target)
		diff := path + adoptDiffSuffix
		_ = os.WriteFile(diff, []byte(adoptionDiff(before, after)), 0o644)
		return nil, fmt.Errorf(
			"adoption aborted: the cache rebuilt from the translated log differs from this one, "+
				"so no log line was appended and no file was renamed. The difference is in %s", diff)
	}
	if err := fresh.Close(); err != nil {
		removeCache(target)
		return nil, err
	}

	var legacyCount int
	for _, e := range minted {
		if e.Legacy {
			legacyCount++
		}
	}
	tasks := 0
	if n := len(minted); n > 0 {
		var p SnapshotPayload
		if err := decodeEventPayload(minted[n-1], &p); err == nil {
			tasks = len(p.Tasks)
		}
	}
	return &AdoptReport{LegacyEvents: legacyCount, Tasks: tasks}, nil
}

// commitAdoption appends the verified lines, records the watermarks in the
// candidate cache, and swaps it into place.
func commitAdoption(db *sql.DB, path string, state *LocalState, clock *eventlog.Clock, minted []eventlog.Envelope) error {
	if len(minted) > 0 {
		appender, err := eventlog.OpenAppender(eventlog.StoreDir(path), path, state.Rep)
		if err != nil {
			return err
		}
		refs := make([]*eventlog.Envelope, len(minted))
		want := make([]uint64, len(minted))
		for i := range minted {
			want[i] = minted[i].Seq
			refs[i] = &minted[i]
		}
		if err := appender.AppendLocked(refs); err != nil {
			appender.Close()
			return err
		}
		appender.Close()
		for i := range minted {
			if minted[i].Seq != want[i] {
				return fmt.Errorf("log and cache disagree on seq for replica %s: expected %d, appended %d",
					state.Rep, want[i], minted[i].Seq)
			}
		}
	}

	target := path + adoptCandidateSuffix
	fresh, err := openCache(target)
	if err != nil {
		return err
	}
	files, err := eventlog.Files(eventlog.StoreDir(path))
	if err != nil {
		fresh.Close()
		return err
	}
	if _, err := fresh.Exec("DELETE FROM log_watermarks"); err != nil {
		fresh.Close()
		return err
	}
	for _, f := range files {
		if err := setWatermark(fresh, f.Rep, f.Size); err != nil {
			fresh.Close()
			return err
		}
	}
	if err := fresh.Close(); err != nil {
		return err
	}

	if clock != nil {
		if v := clock.Save(); v > state.LastSeen {
			state.LastSeen = v
		}
	}
	if err := state.Save(path); err != nil {
		return err
	}

	if err := db.Close(); err != nil {
		return err
	}
	return swapCache(path)
}

// swapCache renames the legacy cache aside and the candidate into its place,
// sidecars included.
func swapCache(path string) error {
	backup := path + adoptBackupSuffix
	target := path + adoptCandidateSuffix
	for _, suffix := range cacheSidecars {
		if _, err := os.Stat(path + suffix); err == nil {
			if err := os.Rename(path+suffix, backup+suffix); err != nil {
				return fmt.Errorf("keep the legacy cache: %w", err)
			}
		}
	}
	if err := os.Rename(path, backup); err != nil {
		return fmt.Errorf("keep the legacy cache: %w", err)
	}
	// The candidate was closed cleanly, so SQLite has checkpointed its WAL;
	// anything left is dead weight that must not follow the file.
	for _, suffix := range cacheSidecars {
		os.Remove(target + suffix)
	}
	if err := os.Rename(target, path); err != nil {
		return fmt.Errorf("install the adopted cache: %w", err)
	}
	return nil
}

// cacheSidecars are the files SQLite keeps beside a WAL-mode database.
var cacheSidecars = []string{"-wal", "-shm"}

func removeCache(path string) {
	os.Remove(path)
	for _, suffix := range cacheSidecars {
		os.Remove(path + suffix)
	}
}

// adoptionDiff renders the lines that differ between two dumps, which is what
// an operator needs to see when adoption refuses.
func adoptionDiff(before, after string) string {
	b, a := strings.Split(before, "\n"), strings.Split(after, "\n")
	var out strings.Builder
	out.WriteString("Adoption compared the legacy cache against the one rebuilt from its\n")
	out.WriteString("translated log and found these differences. Nothing was changed.\n\n")
	n := max(len(a), len(b))
	shown := 0
	for i := 0; i < n && shown < 200; i++ {
		var x, y string
		if i < len(b) {
			x = b[i]
		}
		if i < len(a) {
			y = a[i]
		}
		if x == y {
			continue
		}
		fmt.Fprintf(&out, "line %d\n  legacy: %s\n  rebuilt: %s\n", i+1, x, y)
		shown++
	}
	return out.String()
}
