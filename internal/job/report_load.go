package job

import (
	"database/sql"
	"fmt"
	"strings"
)

// reportPayloadTypes are the event types whose payload the fold or the
// figures read. Every other row is loaded without its detail, which keeps
// note and description text — most of a store's bytes — out of the replay.
var reportPayloadTypes = []EventType{
	EventCreated, EventReparented, EventKindChanged, EventBlocked,
	EventUnblocked, EventImported, EventSnapshot,
}

// reportStoreTask is a task the store holds now: the only tasks a report can
// count (a purged one is gone from here), with the title an import marker
// shows.
type reportStoreTask struct {
	title string
}

// loadReportTasks reads the tasks the store holds, by short id.
func loadReportTasks(db *sql.DB) (map[string]reportStoreTask, error) {
	rows, err := db.Query("SELECT short_id, title FROM tasks WHERE deleted_at IS NULL")
	if err != nil {
		return nil, fmt.Errorf("report: read tasks: %w", err)
	}
	defer rows.Close()
	out := map[string]reportStoreTask{}
	for rows.Next() {
		var id string
		var t reportStoreTask
		if err := rows.Scan(&id, &t.title); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// loadReportEvents reads every event at or before untilMS in log order
// (ts, rep, seq). Rows that belong to no task the cache holds are dropped,
// except snapshots, which belong to no task by design.
func loadReportEvents(db *sql.DB, untilMS int64) ([]reportEvent, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(reportPayloadTypes)), ",")
	args := make([]any, 0, len(reportPayloadTypes)+1)
	for _, t := range reportPayloadTypes {
		args = append(args, string(t))
	}
	args = append(args, untilMS)
	rows, err := db.Query(`
		SELECT e.ts, e.actor, e.event_type, COALESCE(t.short_id, ''),
		       CASE WHEN e.event_type IN (`+placeholders+`) THEN COALESCE(e.detail, '') ELSE '' END
		FROM events e LEFT JOIN tasks t ON t.id = e.task_id
		WHERE e.ts <= ?
		ORDER BY e.ts, e.rep, e.seq, e.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("report: read events: %w", err)
	}
	defer rows.Close()
	var out []reportEvent
	for rows.Next() {
		var e reportEvent
		var typ, detail string
		if err := rows.Scan(&e.ts, &e.actor, &typ, &e.task, &detail); err != nil {
			return nil, err
		}
		e.typ = EventType(typ)
		if e.task == "" && e.typ != EventSnapshot {
			continue
		}
		if detail != "" && detail != "null" {
			e.data = []byte(detail)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
