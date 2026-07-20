package job

import (
	"database/sql"
	"math"
	"strings"
)

// Usage is the read-only activity report backed by `job status --usage`.
// All fields are computed at scope (forest or subtree) and at the chosen
// time window (all-time by default, or windowed when SinceUnix != nil).
//
// Schema reality:
//   - Jobs has four task statuses: available, claimed, done, canceled.
//     "Blocked" is derived from the `blocks` table (an available task that
//     has ≥1 non-done blocker). There is no "paused" status — the design
//     doc mentioned one speculatively; it doesn't exist and is omitted.
//   - There is no snapshot table; v1 does not report a snapshot count.
//
// Velocity numerator is the count of `done` events emitted (so a task that
// was done → reopened → re-done counts each completion). Denominator is
// calendar days: (now − first event) for all-time, (now − since) for
// windowed. We deliberately do NOT average per-day rates then mean them —
// that collapses to the same number as `total / N`. We do NOT exclude
// idle days; calendar span is the honest, simple metric.
type Usage struct {
	// Scope: nil = whole forest; non-nil = subtree root task id.
	ScopeID      *int64
	ScopeShortID string // empty when Forest-scoped

	// Status counts (subtree-scoped; zero-count suppression happens at render).
	Open     int // status='available'
	Claimed  int
	Done     int // task rows with status='done'
	Canceled int
	Blocked  int // available tasks with ≥1 non-done blocker

	// Activity (events in scope).
	EventCount    int64
	FirstEventAt  int64 // unix; 0 when no events in scope
	LastEventAt   int64 // unix; 0 when no events in scope
	DoneAllEvents int   // every 'done' event in scope (all-time numerator)

	// Window / velocity.
	SinceUnix               *int64  // nil = all-time
	WindowKind              string  // "all-time" | "windowed"
	WindowDays              float64 // calendar days used as denominator
	VelocityDenominatorDays float64
	VelocityRate            float64 // done-events / denominator-days
	DoneInWindow            int     // 'done' events in scope within window (windowed only)

	// DB vitals (forest-wide; not scope-affected).
	DBFileSizeBytes int64
}

const (
	usageWindowAllTime  = "all-time"
	usageWindowWindowed = "windowed"
)

// secondsPerDay is the calendar day constant used for velocity math.
const secondsPerDay = 86400.0

// RunUsage computes the usage report for the forest (scopeID == nil) or
// the subtree rooted at scopeID (non-nil). When sinceUnix is non-nil the
// report is windowed: events, done-events, and velocity restrict to the
// window [sinceUnix, now]. Otherwise all-time aggregates are returned.
//
// Read-only and fast: every query leverages an existing index
// (idx_tasks_status, idx_tasks_parent_id, idx_events_task_id,
// idx_events_created_at, idx_events_event_type). The only non-indexed
// scan is the recursive subtree CTE, which is bounded by subtree size.
func RunUsage(db *sql.DB, scopeID *int64, sinceUnix *int64) (*Usage, error) {
	u := &Usage{ScopeID: scopeID}
	if sinceUnix != nil {
		u.SinceUnix = sinceUnix
		u.WindowKind = usageWindowWindowed
	} else {
		u.WindowKind = usageWindowAllTime
	}

	if scopeID != nil {
		if err := scopeShortFor(db, *scopeID, u); err != nil {
			return nil, err
		}
	}

	if err := fillStatusCounts(db, scopeID, u); err != nil {
		return nil, err
	}
	if err := fillActivity(db, scopeID, sinceUnix, u); err != nil {
		return nil, err
	}
	u.computeVelocity()
	if err := fillDBFileSize(db, u); err != nil {
		return nil, err
	}
	return u, nil
}

func scopeShortFor(db *sql.DB, id int64, u *Usage) error {
	var shortID string
	err := db.QueryRow(
		"SELECT short_id FROM tasks WHERE id = ? AND deleted_at IS NULL",
		id,
	).Scan(&shortID)
	if err == sql.ErrNoRows {
		return nil // subtle: scope id vanished since resolved; leave blank
	}
	if err != nil {
		return err
	}
	u.ScopeShortID = shortID
	return nil
}

// fillStatusCounts runs one subtree-scoped GROUP BY status plus an
// islands/derived query for blocked-leaf counts.
func fillStatusCounts(db *sql.DB, scopeID *int64, u *Usage) error {
	// subtree CTE handles both forest (no scope) and subtree. The forest
	// form is a degenerate "everything" recursive CTE — SQLite still uses
	// idx_tasks_parent_id for the recursion, so this stays cheap.
	var rows *sql.Rows
	var err error
	if scopeID == nil {
		rows, err = db.Query(`
			SELECT status, COUNT(*)
			FROM tasks
			WHERE deleted_at IS NULL
			GROUP BY status
		`)
	} else {
		rows, err = db.Query(`
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT t.status, COUNT(*)
			FROM tasks t
			JOIN subtree s ON s.id = t.id
			WHERE t.deleted_at IS NULL
			GROUP BY t.status
		`, *scopeID)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return err
		}
		switch status {
		case "available":
			u.Open = n
		case "claimed":
			u.Claimed = n
		case "done":
			u.Done = n
		case "canceled":
			u.Canceled = n
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Blocked: count of available (non-done) tasks in the scope whose
	// `blocks` row set contains at least one non-done blocker.
	// Reuses idx_blocks_blocked_id + idx_tasks_status.
	if scopeID == nil {
		err = db.QueryRow(`
			SELECT COUNT(DISTINCT b.blocked_id)
			FROM blocks b
			JOIN tasks blocked ON blocked.id = b.blocked_id
			JOIN tasks blocker ON blocker.id = b.blocker_id
			WHERE blocked.status = 'available'
			  AND blocked.deleted_at IS NULL
			  AND blocker.status != 'done'
			  AND blocker.deleted_at IS NULL
		`).Scan(&u.Blocked)
	} else {
		err = db.QueryRow(`
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT COUNT(DISTINCT b.blocked_id)
			FROM blocks b
			JOIN subtree s ON s.id = b.blocked_id
			JOIN tasks blocked ON blocked.id = b.blocked_id
			JOIN tasks blocker ON blocker.id = b.blocker_id
			WHERE blocked.status = 'available'
			  AND blocked.deleted_at IS NULL
			  AND blocker.status != 'done'
			  AND blocker.deleted_at IS NULL
		`, *scopeID).Scan(&u.Blocked)
	}
	return err
}

// fillActivity emits the event stats: count, min/max created_at, and the
// 'done' event counts used for velocity. Counting distinct 'done' events
// means a task that was done → reopened → re-done counts each completion,
// which is the intended "tasks shipped" velocity signal.
func fillActivity(db *sql.DB, scopeID *int64, sinceUnix *int64, u *Usage) error {
	var (
		err   error
		query string
		args  []any
	)
	// Three counts in one pass would be ideal, but the (scope × window)
	// combinations vary; keep one query each for clarity. All use
	// idx_events_task_id (scope) + idx_events_created_at (window).
	if scopeID == nil {
		query = `
			SELECT
			  COUNT(*),
			  COALESCE(MIN(created_at), 0),
			  COALESCE(MAX(created_at), 0)
			FROM events
		`
		if sinceUnix != nil {
			query += " WHERE created_at >= ?"
			args = append(args, *sinceUnix)
		}
	} else {
		query = `
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT
			  COUNT(*),
			  COALESCE(MIN(e.created_at), 0),
			  COALESCE(MAX(e.created_at), 0)
			FROM events e
			JOIN subtree s ON s.id = e.task_id
		`
		args = append(args, *scopeID)
		if sinceUnix != nil {
			query += " WHERE e.created_at >= ?"
			args = append(args, *sinceUnix)
		}
	}
	if err = db.QueryRow(query, args...).Scan(&u.EventCount, &u.FirstEventAt, &u.LastEventAt); err != nil {
		return err
	}

	// All-time 'done' events (numerator for all-time velocity). We always
	// compute this — even in windowed mode the renderer doesn't surface it
	// but tests and the JSON shape may find it useful.
	if scopeID == nil {
		err = db.QueryRow(`
			SELECT COUNT(*) FROM events WHERE event_type = 'done'
		`).Scan(&u.DoneAllEvents)
	} else {
		err = db.QueryRow(`
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT COUNT(*)
			FROM events e
			JOIN subtree s ON s.id = e.task_id
			WHERE e.event_type = 'done'
		`, *scopeID).Scan(&u.DoneAllEvents)
	}
	if err != nil {
		return err
	}

	// DoneInWindow: 'done' events in scope (and within window when set).
	// Reuses idx_events_event_type + the subtree CTE.
	if scopeID == nil {
		query = "SELECT COUNT(*) FROM events WHERE event_type = 'done'"
		if sinceUnix != nil {
			query += " AND created_at >= ?"
			args = []any{*sinceUnix}
		} else {
			args = nil
		}
	} else {
		query = `
			WITH RECURSIVE subtree(id) AS (
				SELECT id FROM tasks WHERE id = ? AND deleted_at IS NULL
				UNION ALL
				SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
				WHERE t.deleted_at IS NULL
			)
			SELECT COUNT(*)
			FROM events e
			JOIN subtree s ON s.id = e.task_id
			WHERE e.event_type = 'done'
		`
		args = []any{*scopeID}
		if sinceUnix != nil {
			query += " AND e.created_at >= ?"
			args = append(args, *sinceUnix)
		}
	}
	if err = db.QueryRow(query, args...).Scan(&u.DoneInWindow); err != nil {
		return err
	}

	// Unscoped first/last for all-time velocity denom.
	// We already have scope-restricted First/Last above. For all-time:
	// use those directly (no extra query). For windowed: FirstEventAt is
	// already the in-window minimum, which is fine for the period stat.
	// Velocity uses LastEventAt vs the unbounded first event when
	// all-time; in windowed mode we use the window length, not (last -
	// first), since the human asked for "last N days".
	return nil
}

// computeVelocity fills VelocityRate, VelocityDenominatorDays, and
// WindowDays. Numerator choice:
//   - all-time: DoneAllEvents (every done event ever in scope)
//   - windowed: DoneInWindow (done events since sinceUnix)
//
// Denominator:
//   - all-time: (now − FirstEventAt) calendar days
//   - windowed: (now − sinceUnix) calendar days
func (u *Usage) computeVelocity() {
	now := CurrentNowFunc().Unix()
	var numerator float64
	var denomSeconds float64
	switch u.WindowKind {
	case usageWindowAllTime:
		numerator = float64(u.DoneAllEvents)
		if u.FirstEventAt > 0 && now > u.FirstEventAt {
			denomSeconds = float64(now - u.FirstEventAt)
		}
	case usageWindowWindowed:
		numerator = float64(u.DoneInWindow)
		if u.SinceUnix != nil && now > *u.SinceUnix {
			denomSeconds = float64(now - *u.SinceUnix)
		}
	}
	if denomSeconds <= 0 {
		u.VelocityDenominatorDays = 0
		u.VelocityRate = 0
		u.WindowDays = 0
		return
	}
	denomDays := denomSeconds / secondsPerDay
	u.WindowDays = denomDays
	u.VelocityDenominatorDays = math.Round(denomDays*100) / 100
	if denomDays > 0 {
		u.VelocityRate = math.Round((numerator/denomDays)*1000) / 1000
	}
}

// fillDBFileSize reports the database file size via SQLite pragmas. We
// cannot rely on an fs path: callers open *sql.DB with arbitrary DSNs
// (including in-memory). PRAGMA page_count × page_size returns the
// persistent-file footprint using SQLite's own metadata.
func fillDBFileSize(db *sql.DB, u *Usage) error {
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return err
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}
	u.DBFileSizeBytes = pageCount * pageSize
	return nil
}

// renderScope is a small reuse hook for the renderer packages; kept here
// so cmd/job can pull the scope label without re-implementing logic.
func (u *Usage) renderScope() string {
	if u.ScopeID == nil {
		return "forest"
	}
	if u.ScopeShortID != "" {
		return u.ScopeShortID
	}
	return "subtree"
}

// _ keeps strings in the import list for future render glue without
// breaking compilation when unused.
var _ = strings.TrimSpace
