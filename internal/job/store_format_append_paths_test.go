package job

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The paths that append to a log file outside commit.
//
// commit re-declares a file's store format before appending to it
// (store_format_redeclare_test.go). Rekey, adoption and the bootstrap each
// write a file without going through commit, so each is held to the same
// promise here: whatever they leave behind declares at least the format its
// newest event needs.

// storeFormatRequired is what every guard compares against: the newest format
// among the types of the lines, where a history-only line needs nothing and a
// type this binary does not know needs more than it can declare.
func TestStoreFormatRequired(t *testing.T) {
	line := func(ty EventType) eventlog.Envelope { return eventlog.Envelope{Type: eventlog.Type(ty)} }

	if got := storeFormatRequired(nil); got != 1 {
		t.Fatalf("no lines require format %d, want 1", got)
	}
	for format, types := range storeFormatAdded {
		for _, ty := range types {
			evs := []eventlog.Envelope{line(EventCreated), line(ty)}
			if got := storeFormatRequired(evs); got != format {
				t.Fatalf("a %q line requires format %d, want %d", ty, got, format)
			}
		}
	}

	legacy := line(EventImported)
	legacy.Legacy = true
	if got := storeFormatRequired([]eventlog.Envelope{line(EventCreated), legacy}); got != 1 {
		t.Fatalf("a legacy %q line requires format %d, want 1: it applies nothing", EventImported, got)
	}

	if got := storeFormatRequired([]eventlog.Envelope{line("from-the-future")}); got <= StoreFormat {
		t.Fatalf("an unknown type requires format %d, want more than this binary's %d", got, StoreFormat)
	}
}

// Rekey appends without applying, because the cache it would apply into is
// the one that refused to build — but the file it appends to is held to the
// same promise commit keeps: re-declared before the first event this binary
// writes into it.
func TestRekeyRedeclaresAnOlderFormatFile(t *testing.T) {
	r, rep := forgeOwnFormat1Store(t)
	r.do(func(db *sql.DB) {
		if _, err := RunRekey(db, rep+":Old01", "sam"); err != nil {
			t.Fatalf("rekey: %v", err)
		}
	})

	evs := r.ownEvents(rep)
	if len(evs) != 4 {
		t.Fatalf("own file holds %d events, want 4 (announce, created, marker, rekeyed)", len(evs))
	}
	marker, rekeyed := evs[2], evs[3]
	if EventType(marker.Type) != EventReplica {
		t.Fatalf("event before the rekey is %q, want a replica marker", marker.Type)
	}
	var p ReplicaPayload
	if err := json.Unmarshal(marker.Data, &p); err != nil {
		t.Fatalf("decode marker: %v", err)
	}
	if p.Format != StoreFormat || p.Label != "old-label" {
		t.Fatalf("marker declares format %d label %q, want %d and the file's existing %q",
			p.Format, p.Label, StoreFormat, "old-label")
	}
	if EventType(rekeyed.Type) != EventRekeyed {
		t.Fatalf("last event is %q, want %q", rekeyed.Type, EventRekeyed)
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("file now declares format %d, want %d", got, StoreFormat)
	}
}

// A replica whose first write is a rekey says who it is first, as it would
// had its first write gone through commit.
func TestRekeyAsTheFirstWriteAnnouncesTheReplica(t *testing.T) {
	quietNotices(t)
	dir := t.TempDir()
	cache := filepath.Join(dir, ".jobs.db")
	theirs := forgeReplicaFile(t, dir, "Abc12", "someone else's")

	db, err := OpenDB(cache)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := RunRekey(db, theirs+":Abc12", "ben"); err != nil {
		t.Fatalf("rekey: %v", err)
	}

	mine := repOf(t, dir)
	evs, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(cache), mine))
	if err != nil {
		t.Fatalf("read own file: %v", err)
	}
	if len(evs) != 2 || EventType(evs[0].Type) != EventReplica || EventType(evs[1].Type) != EventRekeyed {
		types := make([]string, len(evs))
		for i, e := range evs {
			types[i] = string(e.Type)
		}
		t.Fatalf("own file holds %v, want [replica rekeyed]", types)
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("own file declares format %d, want %d", got, StoreFormat)
	}
}

// Adoption mints its lines with this binary, so the file it writes them into
// declares this binary's format — and the check that makes adoption safe,
// comparing the legacy cache against the rebuilt one, still passes.
func TestAdoptionDeclaresTheFormatOfTheFileItWrites(t *testing.T) {
	newMergeClock(t)
	quietNotices(t)
	dir := t.TempDir()
	path := legacyCache(t, dir, func(db *sql.DB) {
		MustAdd(t, db, "", "Alpha root")
		// A cache that predates the store predates `replica` events too.
		if _, err := db.Exec("DELETE FROM events WHERE event_type = ?", string(EventReplica)); err != nil {
			t.Fatal(err)
		}
	})
	report, err := adopt(path)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	evs, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(path), report.Rep))
	if err != nil {
		t.Fatalf("read adopted file: %v", err)
	}
	if len(evs) == 0 || EventType(evs[0].Type) != EventReplica || evs[0].Legacy {
		t.Fatalf("the adopted file does not open with this replica's own declaration")
	}
	if got := fileStoreFormat(evs); got != StoreFormat {
		t.Fatalf("the adopted file declares format %d, want %d", got, StoreFormat)
	}
	if report.LegacyEvents == 0 || report.Tasks != 1 {
		t.Fatalf("report = %+v, want legacy events and one task", report)
	}

	// The declaration is in the cache too, so the next write owes nothing.
	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("open adopted cache: %v", err)
	}
	defer db.Close()
	MustAdd(t, db, "", "After adoption")
	after, err := eventlog.ReadFile(eventlog.LogPath(eventlog.StoreDir(path), report.Rep))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(replicaEvents(t, after)); n != 1 {
		t.Fatalf("own file holds %d replica events after the next write, want 1", n)
	}
}

// The bootstrap copies cached lines verbatim, so it cannot insert a
// declaration without renumbering them — and a line copied for another
// replica is not this binary's to re-declare at all. A run that declares less
// than it holds is therefore left in the cache rather than written out as a
// file an older binary would misread.
func TestBootstrapRefusesARunDeclaringLessThanItHolds(t *testing.T) {
	notices := captureNotices(t)
	dir := t.TempDir()
	db := storeAt(t, dir)
	if _, err := RunAdd(db, "", "written before the store", "", "", nil, "ben"); err != nil {
		t.Fatalf("add: %v", err)
	}
	rep := repOf(t, dir)

	// A run as a binary without re-declaration would have left it: a format-1
	// declaration, and a line of a type format 1 does not have.
	stale, err := json.Marshal(ReplicaPayload{Label: "old-label"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE events SET detail = ? WHERE event_type = ?", string(stale), string(EventReplica)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE events SET event_type = ? WHERE event_type = ?", string(EventImported), string(EventCreated)); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(dir, ".jobs.db")
	if err := os.RemoveAll(eventlog.LogDir(eventlog.StoreDir(cache))); err != nil {
		t.Fatalf("remove log dir: %v", err)
	}
	if _, err := db.Exec("DELETE FROM log_watermarks"); err != nil {
		t.Fatalf("clear watermarks: %v", err)
	}

	before := applyDump(t, db)
	db, _ = reopenStore(t, db, dir)
	if _, err := os.Stat(eventlog.LogPath(eventlog.StoreDir(cache), rep)); !os.IsNotExist(err) {
		t.Fatalf("the bootstrap wrote a file for a run declaring format 1 that holds a format-%d line", StoreFormat)
	}
	if after := applyDump(t, db); after != before {
		t.Fatalf("the refused bootstrap changed the cache")
	}
	if !strings.Contains(notices.String(), "store format") {
		t.Fatalf("no notice names the store format:\n%s", notices.String())
	}
}
