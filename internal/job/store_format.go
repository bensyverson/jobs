package job

import (
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
// binaries yet to be built.

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
// latest `replica` event it carries. `job replica rename` appends another, so
// there may be several, and the latest is the one that describes the file as
// it now stands. A file with no `replica` event — or one whose payload omits
// the field, which is every file written before the format existed — is
// format 1.
func fileStoreFormat(events []eventlog.Envelope) StoreFormatVersion {
	format := StoreFormatVersion(1)
	for _, e := range events {
		if EventType(e.Type) != EventReplica {
			continue
		}
		var p ReplicaPayload
		if err := decodeEventPayload(e, &p); err != nil {
			continue
		}
		if p.Format >= 1 {
			format = p.Format
		}
	}
	return format
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
func checkStoreFormatFor(binary StoreFormatVersion, path string, events []eventlog.Envelope) error {
	if format := fileStoreFormat(events); format > binary {
		return &StoreFormatAheadError{Path: path, LogFormat: format, BinaryFormat: binary}
	}
	return nil
}
