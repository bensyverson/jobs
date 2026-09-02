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

// checkStoreFormat refuses a file this binary is too old to apply.
func checkStoreFormat(path string, events []eventlog.Envelope) error {
	if format := fileStoreFormat(events); format > StoreFormat {
		return &StoreFormatAheadError{Path: path, LogFormat: format, BinaryFormat: StoreFormat}
	}
	return nil
}
