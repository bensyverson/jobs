package job

import (
	"database/sql"
	"fmt"
)

// The two databases read into memory. Every map here is keyed on short ids,
// which are the only identity two SQLite files share: row ids are minted per
// file and are never copied across.

// mergeClosedStatuses are the statuses that mean "this task is finished". A
// close on either side beats a live claim on the other.
var mergeClosedStatuses = map[string]bool{"done": true, "canceled": true}

func isClosedStatus(status string) bool { return mergeClosedStatuses[status] }

type mergeTaskRow struct {
	shortID        string
	parentShortID  string
	title          string
	description    string
	status         string
	sortKey        string
	claimedBy      sql.NullString
	claimExpiresAt sql.NullInt64
	completionNote sql.NullString
	createdAt      int64
	updatedAt      int64
	deletedAt      sql.NullInt64
	kind           string
}

func (r mergeTaskRow) hasLiveClaim(now int64) bool {
	return r.claimedBy.Valid && r.claimedBy.String != "" &&
		r.claimExpiresAt.Valid && r.claimExpiresAt.Int64 > now
}

type mergeCriterionRow struct {
	taskShortID string
	shortID     sql.NullString
	label       string
	state       string
	sortKey     string
	createdAt   int64
	updatedAt   int64
}

// key is a criterion's cross-database identity: its task plus its short id,
// because short ids are unique per task, not per table (migration 0008).
// The label fallback covers rows old enough to pre-date migration 0005 on a
// database that was copied before the backfill ran.
func (c mergeCriterionRow) key() string {
	if c.shortID.Valid && c.shortID.String != "" {
		return "sid:" + c.taskShortID + "\x00" + c.shortID.String
	}
	return "label:" + c.taskShortID + "\x00" + c.label
}

func (c mergeCriterionRow) sameAs(o mergeCriterionRow) bool {
	return c.taskShortID == o.taskShortID && c.label == o.label && c.state == o.state &&
		c.sortKey == o.sortKey && c.createdAt == o.createdAt && c.updatedAt == o.updatedAt
}

type mergeEventRow struct {
	// taskShortID is empty when the event's task was purged; events.task_id
	// is nullable and such a row still merges on the rest of the tuple.
	taskShortID string
	eventType   string
	actor       string
	detail      string
	createdAt   int64
}

func (e mergeEventRow) key() string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", e.taskShortID, e.eventType, e.actor, e.detail, e.createdAt)
}

type mergeBlockKey struct{ blocker, blocked string }

type mergeFoundIn struct {
	sourceShortID string
	createdAt     int64
}

// mergeSnapshot is one database read whole into memory, keyed by short id
// throughout. Both files are small enough that this is far simpler than
// streaming, and it lets the plan be computed before a transaction opens.
type mergeSnapshot struct {
	tasks    map[string]*mergeTaskRow
	labels   map[string]map[string]int64
	blocks   map[mergeBlockKey]int64
	criteria map[string]*mergeCriterionRow
	foundIn  map[string]mergeFoundIn
	events   []mergeEventRow
	users    map[string]int64
}

func readMergeSnapshot(db *sql.DB) (*mergeSnapshot, error) {
	s := &mergeSnapshot{
		tasks:    map[string]*mergeTaskRow{},
		labels:   map[string]map[string]int64{},
		blocks:   map[mergeBlockKey]int64{},
		criteria: map[string]*mergeCriterionRow{},
		foundIn:  map[string]mergeFoundIn{},
		users:    map[string]int64{},
	}

	rows, err := db.Query(`
		SELECT t.short_id, COALESCE(p.short_id, ''), t.title, t.description, t.status,
		       t.sort_key, t.claimed_by, t.claim_expires_at, t.completion_note,
		       t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id`)
	if err != nil {
		return nil, fmt.Errorf("read tasks: %w", err)
	}
	for rows.Next() {
		var r mergeTaskRow
		if err := rows.Scan(&r.shortID, &r.parentShortID, &r.title, &r.description, &r.status,
			&r.sortKey, &r.claimedBy, &r.claimExpiresAt, &r.completionNote,
			&r.createdAt, &r.updatedAt, &r.deletedAt, &r.kind); err != nil {
			rows.Close()
			return nil, err
		}
		s.tasks[r.shortID] = &r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := scanRows(db, `
		SELECT t.short_id, l.name, l.created_at FROM task_labels l
		JOIN tasks t ON t.id = l.task_id`, func(sc scanner) error {
		var task, name string
		var at int64
		if err := sc.Scan(&task, &name, &at); err != nil {
			return err
		}
		if s.labels[task] == nil {
			s.labels[task] = map[string]int64{}
		}
		s.labels[task][name] = at
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read labels: %w", err)
	}

	if err := scanRows(db, `
		SELECT br.short_id, bd.short_id, b.created_at FROM blocks b
		JOIN tasks br ON br.id = b.blocker_id
		JOIN tasks bd ON bd.id = b.blocked_id`, func(sc scanner) error {
		var blocker, blocked string
		var at int64
		if err := sc.Scan(&blocker, &blocked, &at); err != nil {
			return err
		}
		s.blocks[mergeBlockKey{blocker, blocked}] = at
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read blocks: %w", err)
	}

	if err := scanRows(db, `
		SELECT t.short_id, c.short_id, c.label, c.state, c.sort_key, c.created_at, c.updated_at
		FROM task_criteria c JOIN tasks t ON t.id = c.task_id`, func(sc scanner) error {
		var c mergeCriterionRow
		if err := sc.Scan(&c.taskShortID, &c.shortID, &c.label, &c.state, &c.sortKey,
			&c.createdAt, &c.updatedAt); err != nil {
			return err
		}
		s.criteria[c.key()] = &c
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read criteria: %w", err)
	}

	if err := scanRows(db, `
		SELECT t.short_id, src.short_id, f.created_at FROM found_in f
		JOIN tasks t ON t.id = f.task_id
		JOIN tasks src ON src.id = f.source_id`, func(sc scanner) error {
		var task, source string
		var at int64
		if err := sc.Scan(&task, &source, &at); err != nil {
			return err
		}
		s.foundIn[task] = mergeFoundIn{sourceShortID: source, createdAt: at}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read found_in: %w", err)
	}

	if err := scanRows(db, `SELECT name, created_at FROM users`, func(sc scanner) error {
		var name string
		var at int64
		if err := sc.Scan(&name, &at); err != nil {
			return err
		}
		s.users[name] = at
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read users: %w", err)
	}

	if err := scanRows(db, `
		SELECT COALESCE(t.short_id, ''), e.event_type, e.actor, COALESCE(e.detail, ''), e.created_at
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		ORDER BY e.id`, func(sc scanner) error {
		var e mergeEventRow
		if err := sc.Scan(&e.taskShortID, &e.eventType, &e.actor, &e.detail, &e.createdAt); err != nil {
			return err
		}
		s.events = append(s.events, e)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}

	return s, nil
}

func scanRows(db *sql.DB, query string, fn func(scanner) error) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// commonEventPrefix counts the leading events the two databases agree on. A
// zero-length prefix with events on both sides means the files were never
// one database, which is the one situation merge must refuse: interleaving
// two unrelated histories produces a store that is nobody's.
func commonEventPrefix(local, other []mergeEventRow) (int, error) {
	n := 0
	for n < len(local) && n < len(other) && local[n].key() == other[n].key() {
		n++
	}
	if n == 0 && len(local) > 0 && len(other) > 0 {
		return 0, fmt.Errorf("these databases are unrelated: their event logs differ from the first event, so they are not two copies of one database")
	}
	return n, nil
}
