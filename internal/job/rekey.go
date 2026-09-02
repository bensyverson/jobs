package job

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math/big"
	"strings"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// `job rekey <rep>:<id>` — the way out of a cross-replica short-id collision.
//
// Two replicas can mint the same id while apart, and once an id is in notes
// and commit messages no automatic remap is safe: a human has to say which
// task keeps it. That decision is recorded as a `rekeyed` event in this
// replica's file, so every machine that pulls the log converges on the same
// rename without deciding again.
//
// It reads the raw log rather than the cache, because the cache is what
// refused to build.

// RekeyResult names the rename that was recorded.
type RekeyResult struct {
	Rep   string
	OldID string
	NewID string
	Title string
}

// RunRekey gives the named replica's task a fresh short id and rebuilds.
//
// ref is "<rep>:<id>" — the form the collision error prints.
func RunRekey(db *sql.DB, ref, actor string) (*RekeyResult, error) {
	rep, oldID, ok := strings.Cut(ref, ":")
	if !ok || rep == "" || oldID == "" {
		return nil, fmt.Errorf("rekey: expected <replica>:<id>, got %q", ref)
	}
	if !eventlog.ValidReplicaID(rep) {
		return nil, fmt.Errorf("rekey: %q is not a replica id", rep)
	}
	path, err := CachePathOf(db)
	if err != nil {
		return nil, err
	}

	events, err := eventlog.ReadAll(eventlog.StoreDir(path))
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	title := ""
	created := false
	for _, e := range events {
		if EventType(e.Type) != EventCreated {
			continue
		}
		var p CreatedPayload
		if err := decodeEventPayload(e, &p); err != nil {
			return nil, err
		}
		if p.ShortID == "" {
			continue
		}
		used[p.ShortID] = true
		if p.ShortID == oldID && e.Rep == rep {
			created = true
			title = p.Title
		}
	}
	if !created {
		return nil, fmt.Errorf("rekey: replica %s never created %s", rep, oldID)
	}
	newID, err := freeShortID(used)
	if err != nil {
		return nil, err
	}

	if err := appendOwnEvent(db, path, actor, EventRekeyed, "", RekeyedPayload{
		Rep: rep, OldID: oldID, NewID: newID,
	}); err != nil {
		return nil, err
	}
	if err := rebuildStore(db, path); err != nil {
		return nil, err
	}
	return &RekeyResult{Rep: rep, OldID: oldID, NewID: newID, Title: title}, nil
}

// freeShortID mints an id no `created` event in the log has used. The log,
// not the cache: the cache is a projection of it and may be missing the very
// events that make an id taken.
func freeShortID(used map[string]bool) (string, error) {
	for range 100 {
		id := make([]byte, shortIDLen)
		for i := range id {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(base62Chars))))
			if err != nil {
				return "", fmt.Errorf("generate ID: %w", err)
			}
			id[i] = base62Chars[n.Int64()]
		}
		if !used[string(id)] {
			return string(id), nil
		}
	}
	return "", fmt.Errorf("rekey: could not mint an unused short id")
}

// appendOwnEvent writes one event to this replica's log file without applying
// it.
//
// The command path (commit) appends and applies together, which is right for
// every ordinary write. Rekey cannot: the cache it would apply into is the one
// that refused to build. The event goes to the file, and the rebuild that
// follows is what puts it into the cache.
func appendOwnEvent(db *sql.DB, path, actor string, typ EventType, task string, payload any) error {
	lock, err := eventlog.AcquireLock(path)
	if err != nil {
		return err
	}
	defer lock.Release()

	rec, err := newRecorderLocked(path)
	if err != nil {
		return err
	}
	appender, err := eventlog.OpenAppender(eventlog.StoreDir(path), path, rec.rep)
	if err != nil {
		return err
	}
	defer appender.Close()
	last, err := appender.LastSeqLocked()
	if err != nil {
		return err
	}
	rec.primeSeq(last)

	e, err := rec.envelope(typ, task, actor, payload)
	if err != nil {
		return err
	}
	if err := appender.AppendLocked([]*eventlog.Envelope{&e}); err != nil {
		return err
	}
	return rec.persistLocked()
}
