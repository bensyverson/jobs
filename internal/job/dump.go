package job

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// dumpCache renders every table the log owns in a stable, row-id-free form.
//
// Row ids are minted by the cache and differ between two caches holding the
// same content, so "the same database" can only mean "the same dump". Adoption
// compares a legacy cache against the one rebuilt from its translation, and the
// merge tests compare a database against itself after a re-merge.
//
// Events are compared on the tuple a line's content makes — task short id,
// type, actor, detail and created_at — and never on rep, seq or ts: adoption's
// whole job is to give a legacy row a position it did not have, so a comparison
// that counted the position would always differ.
func dumpCache(db *sql.DB) (string, error) { return dumpCacheWith(db, dumpEventsQuery) }

// dumpHistory is dumpCache without the snapshot rows. A snapshot is state
// rather than history — adoption adds exactly one, describing the very content
// being compared — so counting it would make every adoption differ from itself.
func dumpHistory(db *sql.DB) (string, error) { return dumpCacheWith(db, dumpHistoryQuery) }

func dumpCacheWith(db *sql.DB, eventsQuery string) (string, error) {
	var b strings.Builder
	for _, q := range dumpQueries {
		b.WriteString("== " + q.name + "\n")
		rows, err := dumpRows(db, q.query)
		if err != nil {
			return "", fmt.Errorf("dump %s: %w", q.name, err)
		}
		for _, r := range rows {
			b.WriteString(r + "\n")
		}
	}

	events, err := dumpRows(db, eventsQuery)
	if err != nil {
		return "", fmt.Errorf("dump events: %w", err)
	}
	sort.Strings(events)
	b.WriteString("== events\n" + strings.Join(events, "\n") + "\n")
	return b.String(), nil
}

var dumpQueries = []struct{ name, query string }{
	{"tasks", `SELECT t.short_id, COALESCE(p.short_id,''), t.title, t.description, t.status,
		t.sort_key, COALESCE(t.claimed_by,''), COALESCE(t.claim_expires_at,0),
		COALESCE(t.completion_note,''), t.created_at, t.updated_at,
		COALESCE(t.deleted_at,0), t.kind
		FROM tasks t LEFT JOIN tasks p ON p.id = t.parent_id ORDER BY t.short_id`},
	{"labels", `SELECT t.short_id, l.name, l.created_at FROM task_labels l JOIN tasks t ON t.id = l.task_id
		ORDER BY t.short_id, l.name`},
	{"blocks", `SELECT br.short_id, bd.short_id, b.created_at FROM blocks b
		JOIN tasks br ON br.id = b.blocker_id JOIN tasks bd ON bd.id = b.blocked_id
		ORDER BY br.short_id, bd.short_id`},
	{"criteria", `SELECT t.short_id, COALESCE(c.short_id,''), c.label, c.state, c.sort_key,
		c.created_at, c.updated_at FROM task_criteria c JOIN tasks t ON t.id = c.task_id
		ORDER BY t.short_id, c.short_id, c.label`},
	{"found_in", `SELECT t.short_id, s.short_id, f.created_at FROM found_in f
		JOIN tasks t ON t.id = f.task_id JOIN tasks s ON s.id = f.source_id ORDER BY t.short_id`},
	{"users", `SELECT name, created_at FROM users ORDER BY name`},
}

// The event dumps order canonically by content rather than by row id: row ids
// are renumbered by every rebuild, and a legacy row and a positioned row
// written in the same second can swap places across one without any state
// having changed.
const dumpEventsQuery = `
	SELECT COALESCE(t.short_id, ''), e.event_type, e.actor, COALESCE(e.detail, ''), e.created_at
	FROM events e LEFT JOIN tasks t ON t.id = e.task_id
	ORDER BY e.created_at, COALESCE(t.short_id, ''), e.event_type, e.actor, e.detail`

const dumpHistoryQuery = `
	SELECT COALESCE(t.short_id, ''), e.event_type, e.actor, COALESCE(e.detail, ''), e.created_at
	FROM events e LEFT JOIN tasks t ON t.id = e.task_id
	WHERE e.event_type != 'snapshot'
	ORDER BY e.created_at, COALESCE(t.short_id, ''), e.event_type, e.actor, e.detail`

// dumpRows renders each row as its columns joined by a pipe, with NULL and the
// empty string rendered alike — a distinction no reader of this cache makes.
func dumpRows(db *sql.DB, query string) ([]string, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		cells := make([]any, len(cols))
		for i := range cells {
			cells[i] = &vals[i]
		}
		if err := rows.Scan(cells...); err != nil {
			return nil, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = v.String
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out, rows.Err()
}
