package job

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// apply is the only writer of the state tables.
//
// It is a dumb, total function from an event to rows: it never reads the
// clock, never mints an id, never derives a cascade and never reads
// local.json. Everything it needs arrives in the envelope — short ids, sort
// keys, and the timestamp every row it stamps comes from
// (project/2026-09-01-git-native-event-log.md, "Apply never derives").
//
// That is what makes replay deterministic. A handler that closed a parent
// because its last child closed emits the parent's close as its own event;
// apply just writes what it is told, in the order it is told, so the union of
// every replica's log sorted by (ts, rep, seq) rebuilds the same tables
// everywhere.
//
// Legacy rows — the ones with rep '' that pre-date the store — are history.
// apply is never called on them.

// applyTable maps an event type to the state write it means. It is
// deliberately explicit and deliberately small: the claims family and the
// relations/criteria/provenance/kind family add their own entries and their
// own apply_<family>.go files. A type with no entry here is recorded and
// changes no state, which is what a cache that has not learned a type yet
// should do with it.
var applyTable = map[EventType]func(tx dbtx, e eventlog.Envelope) error{
	EventCreated:    applyCreated,
	EventEdited:     applyEdited,
	EventNoted:      applyNoted,
	EventDone:       applyDone,
	EventReopened:   applyReopened,
	EventCanceled:   applyCanceled,
	EventPurged:     applyPurged,
	EventMoved:      applyMoved,
	EventReparented: applyReparented,
	// The claims family (apply_claims.go). `released` is theirs even though
	// the task family emits it too: adding an open child to a claimed parent,
	// or reparenting one under it, releases the claim, and that state write
	// has to travel with those events or a rebuild of them would leave a
	// claim the original had dropped.
	EventClaimed:      applyClaimed,
	EventReleased:     applyReleased,
	EventClaimExpired: applyClaimExpired,
	EventHeartbeat:    applyHeartbeat,

	// The relations, criteria, provenance and kind family
	// (apply_relations.go, apply_criteria.go).
	EventBlocked:        applyBlocked,
	EventUnblocked:      applyUnblocked,
	EventLabeled:        applyLabeled,
	EventUnlabeled:      applyUnlabeled,
	EventCriteriaAdded:  applyCriteriaAdded,
	EventCriterionState: applyCriterionState,
	EventFoundInSet:     applyFoundInSet,
	EventFoundInCleared: applyFoundInCleared,
	EventKindChanged:    applyKindChanged,

	// Adoption and compaction (apply_snapshot.go).
	EventSnapshot: applySnapshot,
}

// apply writes the state e means, then records e in the events table.
//
// The order matters both ways. `created` has no row to hang its event on
// until the state write has made one; `purged` erases the subtree's event
// rows, and its own tombstone — recorded on the parent, or as an orphan for a
// root — must survive that.
func apply(tx dbtx, e eventlog.Envelope) error {
	// A legacy line is history and nothing else, whatever its type says. Its
	// payload was written before any of it was replayable, and the state it
	// used to imply arrives instead in adoption's snapshot. The check is here
	// rather than in each apply function so no per-type function has to know
	// the rule.
	if e.Legacy {
		return insertEventRow(tx, e)
	}
	if fn := applyTable[EventType(e.Type)]; fn != nil {
		if err := fn(tx, e); err != nil {
			return fmt.Errorf("apply %s: %w", e.Type, err)
		}
	}
	return insertEventRow(tx, e)
}

// insertEventRow writes the cache's copy of the log line. task_id is resolved
// from the envelope's short id: NULL for an event that belongs to no task, and
// NULL as well for one whose task this cache does not hold (a tombstoned id,
// or an event that arrived before its `created`).
func insertEventRow(tx dbtx, e eventlog.Envelope) error {
	_, err := insertEventRowID(tx, e)
	return err
}

// insertEventRowID is insertEventRow for the one caller that needs the row it
// wrote: the rebuild links a legacy row's task after the fact, because the task
// does not exist yet when the row goes in.
func insertEventRowID(tx dbtx, e eventlog.Envelope) (int64, error) {
	var taskID any
	if e.Task != "" {
		if id, ok, err := taskRowID(tx, e.Task); err != nil {
			return 0, err
		} else if ok {
			taskID = id
		}
	}
	detail := ""
	if len(e.Data) > 0 && string(e.Data) != "null" {
		detail = string(e.Data)
	}
	res, err := tx.Exec(`
		INSERT INTO events (task_id, event_type, actor, detail, created_at, rep, seq, ts)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, string(e.Type), e.Actor, detail, e.TS/1000, e.Rep, int64(e.Seq), e.TS,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// decodeEventPayload unmarshals an envelope's data into the payload struct for
// its type. A missing payload decodes as the zero value rather than an error:
// apply is total.
func decodeEventPayload(e eventlog.Envelope, into any) error {
	if len(e.Data) == 0 || string(e.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(e.Data, into); err != nil {
		return fmt.Errorf("decode %s payload: %w", e.Type, err)
	}
	return nil
}

// taskRowID resolves a short id to this cache's row id. Row ids are local, so
// they never appear in an event; every lookup goes through here.
func taskRowID(tx dbtx, shortID string) (int64, bool, error) {
	var id int64
	err := tx.QueryRow("SELECT id FROM tasks WHERE short_id = ?", shortID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// eventSeconds is the second-resolution timestamp a row is stamped with. The
// envelope carries milliseconds; every task and event column is seconds.
func eventSeconds(e eventlog.Envelope) int64 { return e.TS / 1000 }

// cachedEnvelopes reads the cache's events back as envelopes, in global order.
//
// Legacy rows are skipped: they carry no position, their payloads were never
// replayable, and apply is never called on them. This is what a rebuild, the
// determinism test, and eventually adoption read the local history with.
func cachedEnvelopes(db *sql.DB) ([]eventlog.Envelope, error) {
	rows, err := db.Query(`
		SELECT e.rep, e.seq, e.ts, e.actor, e.event_type,
		       COALESCE(t.short_id, ''), COALESCE(e.detail, '')
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		WHERE e.rep != ''
		ORDER BY e.ts, e.rep, e.seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventlog.Envelope
	for rows.Next() {
		var e eventlog.Envelope
		var seq int64
		var typ, detail string
		if err := rows.Scan(&e.Rep, &seq, &e.TS, &e.Actor, &typ, &e.Task, &detail); err != nil {
			return nil, err
		}
		e.V = eventlog.Version
		e.Seq = uint64(seq)
		e.Type = eventlog.Type(typ)
		if detail != "" {
			e.Data = json.RawMessage(detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// rebuildStateTables names every table the log owns, in an order that lets
// them be emptied without tripping a foreign key. Anything not here is either
// schema bookkeeping or machine-local.
var rebuildStateTables = []string{
	"events", "found_in", "task_criteria", "task_labels", "blocks", "users", "tasks",
}

// rebuildFrom drops the state tables and re-applies events in global order.
//
// This is the operation the whole design rests on: the cache is disposable
// because this reproduces it. The store leaf calls it on open when a log
// file's size no longer matches its watermark; here it is what the
// determinism test rebuilds a shuffled log with.
func rebuildFrom(db *sql.DB, events []eventlog.Envelope) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := rebuildFromInTx(tx, events); err != nil {
		return err
	}
	return tx.Commit()
}

// rebuildFromInTx is rebuildFrom's body, for a caller that has more to do in
// the same transaction — the store advances every watermark there, so a cache
// can never record that it applied a file it did not.
func rebuildFromInTx(tx dbtx, events []eventlog.Envelope) error {
	ordered := append([]eventlog.Envelope(nil), events...)
	eventlog.Sort(ordered)

	for _, table := range rebuildStateTables {
		if _, err := tx.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("rebuild: clear %s: %w", table, err)
		}
	}
	// Legacy lines are recorded in their place in the order like everything
	// else, but linked to their tasks only at the end.
	//
	// A legacy line's ts is historical, so it sorts before the snapshot that
	// creates its task — and at its own position insertEventRow would resolve
	// task_id to NULL and sever the history `job log` and `job show` read. A
	// legacy line writes no state, so nothing else depends on when it is
	// recorded; the row id it returns is the thread back to it once the
	// snapshot has made the tasks.
	type legacyRow struct {
		id   int64
		task string
	}
	var links []legacyRow
	for _, e := range ordered {
		if !e.Legacy {
			if err := apply(tx, e); err != nil {
				return fmt.Errorf("rebuild at %s: %w", e.Position(), err)
			}
			continue
		}
		id, err := insertEventRowID(tx, e)
		if err != nil {
			return fmt.Errorf("rebuild at %s: %w", e.Position(), err)
		}
		if e.Task != "" {
			links = append(links, legacyRow{id: id, task: e.Task})
		}
	}
	for _, l := range links {
		// A task the replay never produced was purged; its history went with
		// it, and the row simply stays unattached.
		id, ok, err := taskRowID(tx, l.task)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec("UPDATE events SET task_id = ? WHERE id = ?", id, l.id); err != nil {
			return err
		}
	}
	return nil
}
