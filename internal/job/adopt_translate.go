package job

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/bensyverson/jobs/internal/eventlog"
)

// Reading a legacy cache out: its unpositioned event rows as history-only log
// lines, and its state as one snapshot payload.
//
// Both halves address tasks by short id, never by row id, because row ids are
// minted by the cache and a rebuild renumbers them.

// legacyEnvelopes translates every unpositioned event row into a log line
// marked `legacy`, in row-id order — the order they were written in, and the
// order `job log` renders them in within a second.
//
// ts is created_at in milliseconds, so the line keeps its place in the
// timeline; rep and seq are assigned by the caller under the store lock.
func legacyEnvelopes(db *sql.DB) ([]eventlog.Envelope, error) {
	rows, err := db.Query(`
		SELECT e.id, e.actor, e.event_type, COALESCE(e.detail, ''), e.created_at,
		       COALESCE(t.short_id, '')
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		WHERE e.rep = '' ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventlog.Envelope
	for rows.Next() {
		var id, createdAt int64
		var actor, typ, detail, short string
		if err := rows.Scan(&id, &actor, &typ, &detail, &createdAt, &short); err != nil {
			return nil, err
		}
		if createdAt <= 0 {
			return nil, fmt.Errorf("event %d has no timestamp, so it cannot be placed in the log", id)
		}
		e := eventlog.Envelope{
			V:      eventlog.Version,
			TS:     createdAt * 1000,
			Actor:  actor,
			Type:   eventlog.Type(typ),
			Task:   short,
			Legacy: true,
		}
		if detail != "" && detail != "null" {
			if !json.Valid([]byte(detail)) {
				return nil, fmt.Errorf("event %d has a detail that is not JSON, so it cannot become a log line: %q", id, detail)
			}
			e.Data = json.RawMessage(detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// snapshotEnvelope mints the one line that carries state. Its ts is chosen by
// snapshotTS, which places it just before whatever the log already holds.
func snapshotEnvelope(payload *SnapshotPayload, ts int64) (eventlog.Envelope, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return eventlog.Envelope{}, fmt.Errorf("marshal snapshot payload: %w", err)
	}
	return eventlog.Envelope{
		V:     eventlog.Version,
		TS:    ts,
		Actor: adoptActor,
		Type:  eventlog.Type(EventSnapshot),
		Data:  data,
	}, nil
}

// adoptActor attributes the snapshot. It is not a person and not a replica: it
// is the conversion itself, and naming it that way keeps `job log` honest.
const adoptActor = "adopt"

// cacheSnapshot reads a cache's whole state into one payload.
func cacheSnapshot(db *sql.DB) (*SnapshotPayload, error) {
	p := &SnapshotPayload{}
	var err error
	if p.Tasks, err = snapshotTasks(db); err != nil {
		return nil, err
	}
	if p.Blocks, err = snapshotBlocks(db); err != nil {
		return nil, err
	}
	if p.Labels, err = snapshotLabels(db); err != nil {
		return nil, err
	}
	if p.Criteria, err = snapshotCriteria(db); err != nil {
		return nil, err
	}
	if p.FoundIn, err = snapshotFoundIn(db); err != nil {
		return nil, err
	}
	if p.Users, err = snapshotUsers(db); err != nil {
		return nil, err
	}
	return p, nil
}

func snapshotTasks(db *sql.DB) ([]SnapshotTask, error) {
	rows, err := db.Query(`
		SELECT t.short_id, COALESCE(p.short_id, ''), t.title, t.description, t.status, t.sort_key,
		       t.claimed_by, t.claim_expires_at, t.completion_note,
		       t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SnapshotTask
	for rows.Next() {
		var t SnapshotTask
		var claimedBy, note sql.NullString
		var expires, deleted sql.NullInt64
		if err := rows.Scan(&t.ShortID, &t.ParentID, &t.Title, &t.Description, &t.Status, &t.SortKey,
			&claimedBy, &expires, &note, &t.CreatedAt, &t.UpdatedAt, &deleted, &t.Kind); err != nil {
			return nil, err
		}
		t.ClaimedBy = nullableString(claimedBy)
		t.CompletionNote = nullableString(note)
		t.ClaimExpiresAt = nullableInt(expires)
		t.DeletedAt = nullableInt(deleted)
		out = append(out, t)
	}
	return out, rows.Err()
}

func snapshotBlocks(db *sql.DB) ([]SnapshotBlock, error) {
	rows, err := db.Query(`
		SELECT br.short_id, bd.short_id, b.created_at FROM blocks b
		JOIN tasks br ON br.id = b.blocker_id JOIN tasks bd ON bd.id = b.blocked_id
		ORDER BY br.short_id, bd.short_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotBlock
	for rows.Next() {
		var b SnapshotBlock
		if err := rows.Scan(&b.BlockerID, &b.BlockedID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func snapshotLabels(db *sql.DB) ([]SnapshotLabel, error) {
	rows, err := db.Query(`
		SELECT t.short_id, l.name, l.created_at FROM task_labels l
		JOIN tasks t ON t.id = l.task_id ORDER BY t.short_id, l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotLabel
	for rows.Next() {
		var l SnapshotLabel
		if err := rows.Scan(&l.TaskID, &l.Name, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func snapshotCriteria(db *sql.DB) ([]SnapshotCriterion, error) {
	rows, err := db.Query(`
		SELECT t.short_id, c.short_id, c.label, c.state, c.sort_key, c.created_at, c.updated_at
		FROM task_criteria c JOIN tasks t ON t.id = c.task_id
		ORDER BY c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotCriterion
	for rows.Next() {
		var c SnapshotCriterion
		var short sql.NullString
		if err := rows.Scan(&c.TaskID, &short, &c.Label, &c.State, &c.SortKey, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.ShortID = nullableString(short)
		out = append(out, c)
	}
	return out, rows.Err()
}

func snapshotFoundIn(db *sql.DB) ([]SnapshotFoundIn, error) {
	rows, err := db.Query(`
		SELECT t.short_id, s.short_id, f.created_at FROM found_in f
		JOIN tasks t ON t.id = f.task_id JOIN tasks s ON s.id = f.source_id
		ORDER BY t.short_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotFoundIn
	for rows.Next() {
		var f SnapshotFoundIn
		if err := rows.Scan(&f.TaskID, &f.SourceID, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func snapshotUsers(db *sql.DB) ([]SnapshotUser, error) {
	rows, err := db.Query("SELECT name, created_at FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SnapshotUser
	for rows.Next() {
		var u SnapshotUser
		if err := rows.Scan(&u.Name, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func nullableString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
