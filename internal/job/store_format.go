package job

import (
	"encoding/json"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// The store format guard.
//
// The schema check in migrations.go guards a *cache* that is ahead of the
// binary. It cannot guard the log: a fresh clone builds its cache at whatever
// schema the binary ships, so an old binary meets a newer .jobs/log with
// nothing to notice. And it would not notice on its own — an event type it
// does not know applies as a no-op, which is the forward tolerance a
// distributed log needs and, without a declared format, indistinguishable
// from silently losing half the record before appending to it.
//
// So every log file declares the format it was written at, in the `replica`
// event that opens it, and a file declaring more than this binary knows stops
// the rebuild. Refuse rather than warn, for the reason the cache check does:
// the log is the record, and the next append would be computed from a
// misread of it.
//
// A file outlives the binary that opened it, so the opening declaration alone
// goes stale: a newer binary appending to a format-1 file would leave it
// declaring format 1 while holding types format 1 does not have. So before
// this binary appends to a file declaring an older format, it re-declares it
// with another `replica` event (envelope.go, owedReplicaEvent) — in commit,
// rekey and adoption alike, the three paths that append lines this binary
// mints. The fourth, the bootstrap, copies cached lines verbatim and so cannot
// re-declare; it refuses a run that declares less than storeFormatRequired
// says it holds (store.go). Binaries already in the field honour the latest
// declaration, which is what makes this a guard for them and not only for
// binaries yet to be built — and why no writer may leave a `replica` line
// declaring less than the one before it: every such line is built by
// newReplicaPayload, which stamps this binary's format, and adoption carries
// no other replica's `replica` row into the log (legacyEnvelopes).

// StoreFormatAheadError reports a log file written at a store format newer
// than this binary knows.
type StoreFormatAheadError struct {
	Path         string
	LogFormat    StoreFormatVersion
	BinaryFormat StoreFormatVersion
}

func (e *StoreFormatAheadError) Error() string {
	name := e.Path
	if name == "" {
		name = "the log"
	}
	return fmt.Sprintf(
		"%s is at store format %d but this job only knows format %d: the binary is older than the log. Rebuild it (make install) or upgrade job.",
		name, e.LogFormat, e.BinaryFormat,
	)
}

// fileStoreFormat is the format one log file declares: the format on the
// latest `replica` event that carries one. `job replica rename` appends
// another, so there may be several, and the latest is the one that describes
// the file as it now stands. A payload that omits the field — every line
// written before the format existed — declares nothing, so it never lowers a
// declaration before it; a file with no declaration at all is format 1.
//
// This is the rule every binary in the field reads by, so it is the one a
// writer answers to: it is what decides whether a file owes a re-declaration
// (owedReplicaEvent). The refusal is stricter — see declaredFormats.
func fileStoreFormat(events []eventlog.Envelope) StoreFormatVersion {
	latest, _ := declaredFormats(events)
	return latest
}

// declaredFormats is the latest format a file declares (fileStoreFormat) and
// the highest it has ever declared. The two differ only when a later line
// declares less than an earlier one, which no writer of this binary leaves: a
// binary declares only its own format, so a file declared downward was written
// by a newer binary and then declared by an older one, and it still holds
// whatever the newer one wrote.
func declaredFormats(events []eventlog.Envelope) (latest, highest StoreFormatVersion) {
	latest, highest = 1, 1
	for _, e := range events {
		if EventType(e.Type) != EventReplica {
			continue
		}
		var p ReplicaPayload
		if err := decodeEventPayload(e, &p); err != nil {
			continue
		}
		if p.Format >= 1 {
			latest = p.Format
			highest = max(highest, p.Format)
		}
	}
	return latest, highest
}

// storeFormatRequired is the lowest format a file holding events must declare
// for a binary to replay them faithfully: the format that introduced the
// newest of their types.
//
// A legacy line applies nothing whatever its type, so it requires nothing. A
// type this binary does not know requires more than it can declare, so a
// guard comparing against the answer fails closed rather than passing it.
func storeFormatRequired(events []eventlog.Envelope) StoreFormatVersion {
	required := StoreFormatVersion(1)
	for _, e := range events {
		if e.Legacy {
			continue
		}
		format, ok := storeFormatOf[EventType(e.Type)]
		if !ok {
			return StoreFormat + 1
		}
		required = max(required, format)
	}
	return required
}

// storeFormatOf is storeFormatAdded inverted: each type's introducing format.
var storeFormatOf = func() map[EventType]StoreFormatVersion {
	out := map[EventType]StoreFormatVersion{}
	for format, types := range storeFormatAdded {
		for _, ty := range types {
			out[ty] = format
		}
	}
	return out
}()

// checkStoreFormat refuses a file this binary is too old to apply.
func checkStoreFormat(path string, events []eventlog.Envelope) error {
	return checkStoreFormatFor(StoreFormat, path, events)
}

// checkStoreFormatFor is checkStoreFormat as a binary that knows only binary
// would run it — the seam that lets a test play an older binary.
//
// It refuses on the highest format the file has ever declared, not the
// latest: a file declared downward still holds what the newer binary wrote,
// and reading it by its latest line is exactly the misread the format exists
// to prevent.
func checkStoreFormatFor(binary StoreFormatVersion, path string, events []eventlog.Envelope) error {
	if _, highest := declaredFormats(events); highest > binary {
		return &StoreFormatAheadError{Path: path, LogFormat: highest, BinaryFormat: binary}
	}
	return nil
}

// checkCachedStoreFormat is checkStoreFormat for an open that does not read
// the log. A cache a newer binary rebuilt in place has watermarks matching the
// files, so syncStore's hot path never reads a line and the rebuild's check
// never runs. But the cache holds every `replica` event it applied, file by
// file, so it already records every declaration the log makes: this reads
// them back — one query on the event_type index, a handful of rows per
// replica — and refuses by the same rule.
//
// It records the log's formats, not the format of the binary that built the
// cache. A newer binary rebuilding a log that declares only this binary's
// format leaves nothing this binary cannot read — it re-declares only when it
// appends — so refusing there would lock out a binary the log still fits. A
// cache laid out in a way this binary cannot read is the schema check's to
// refuse, and moving `replica` events out of the events table would be such a
// change.
func checkCachedStoreFormat(db dbtx, storeDir string) error {
	rows, err := db.Query(
		"SELECT rep, COALESCE(detail, '') FROM events WHERE event_type = ? ORDER BY rep, seq",
		string(EventReplica),
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	byRep := map[string][]eventlog.Envelope{}
	var reps []string
	for rows.Next() {
		var rep, detail string
		if err := rows.Scan(&rep, &detail); err != nil {
			return err
		}
		if _, ok := byRep[rep]; !ok {
			reps = append(reps, rep)
		}
		e := eventlog.Envelope{Type: eventlog.Type(EventReplica)}
		if detail != "" {
			e.Data = json.RawMessage(detail)
		}
		byRep[rep] = append(byRep[rep], e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, rep := range reps {
		path := ""
		if rep != "" {
			path = eventlog.LogPath(storeDir, rep)
		}
		if err := checkStoreFormat(path, byRep[rep]); err != nil {
			return err
		}
	}
	return nil
}
