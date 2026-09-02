package job

import (
	"database/sql"
	"fmt"
	"strings"
)

// CriterionState is the lifecycle state of an acceptance criterion.
type CriterionState string

const (
	CriterionPending CriterionState = "pending"
	CriterionPassed  CriterionState = "passed"
	CriterionSkipped CriterionState = "skipped"
	CriterionFailed  CriterionState = "failed"
)

// Criterion is a single acceptance-criterion row. ShortID is the
// server-minted handle (3-char base62) used for stable references across
// label edits, shell-friendly --criterion flags, and DOM ids; Label is
// the human-facing string, free to be edited later without orphaning the
// event log.
type Criterion struct {
	ID      int64
	ShortID string
	TaskID  int64
	Label   string
	State   CriterionState
	// SortKey is the fractional key that orders this criterion among its
	// task's criteria; see internal/job/sortkey.go.
	SortKey string
}

func ValidateCriterionState(raw string) (CriterionState, error) {
	switch CriterionState(raw) {
	case CriterionPending, CriterionPassed, CriterionSkipped, CriterionFailed:
		return CriterionState(raw), nil
	default:
		return "", fmt.Errorf("invalid criterion state %q (want %s|%s|%s|%s)",
			raw, CriterionPending, CriterionPassed, CriterionSkipped, CriterionFailed)
	}
}

func validateCriterionLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if label == "" {
		return "", fmt.Errorf("criterion label is empty")
	}
	return label, nil
}

// insertCriteria appends each label as a new criterion at the end of taskID's
// list, with state defaulting to pending unless overridden in the input.
// Returns the inserted Criterion records in input order.
func insertCriteria(tx dbtx, taskID int64, items []Criterion) ([]Criterion, error) {
	if len(items) == 0 {
		return nil, nil
	}

	var lastKey string
	if err := tx.QueryRow(
		"SELECT COALESCE(MAX(sort_key), '') FROM task_criteria WHERE task_id = ?", taskID,
	).Scan(&lastKey); err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	now := CurrentNowFunc().Unix()
	out := make([]Criterion, 0, len(items))
	for _, c := range items {
		label, err := validateCriterionLabel(c.Label)
		if err != nil {
			return nil, err
		}
		state := c.State
		if state == "" {
			state = CriterionPending
		}
		if _, err := ValidateCriterionState(string(state)); err != nil {
			return nil, err
		}
		shortID, err := generateCriterionShortID(tx, taskID)
		if err != nil {
			return nil, err
		}
		sortKey, err := KeyBetween(lastKey, "")
		if err != nil {
			return nil, err
		}
		var id int64
		err = tx.QueryRow(
			`INSERT INTO task_criteria (task_id, short_id, label, state, sort_key, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			taskID, shortID, label, string(state), sortKey, now, now,
		).Scan(&id)
		if err != nil {
			return nil, err
		}
		out = append(out, Criterion{
			ID:      id,
			ShortID: shortID,
			TaskID:  taskID,
			Label:   label,
			State:   state,
			SortKey: sortKey,
		})
		lastKey = sortKey
	}
	return out, nil
}

// GetCriteria returns the criteria for taskID in sort order.
func GetCriteria(db dbtx, taskID int64) ([]Criterion, error) {
	rows, err := db.Query(
		`SELECT id, COALESCE(short_id, ''), task_id, label, state, sort_key
		 FROM task_criteria WHERE task_id = ? ORDER BY sort_key, id`,
		taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Criterion
	for rows.Next() {
		var c Criterion
		var state string
		if err := rows.Scan(&c.ID, &c.ShortID, &c.TaskID, &c.Label, &state, &c.SortKey); err != nil {
			return nil, err
		}
		c.State = CriterionState(state)
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCriterionState updates the state of one criterion identified by `ref`,
// which may be either the criterion's short_id or its verbatim label
// (short_id is tried first because it is the stable identity; label is
// kept as a fallback so callers without the short_id — including legacy
// `--criterion "label=state"` use — keep working). Returns the resolved
// criterion (so the caller can record stable identifiers on the event
// detail) and the prior state.
func SetCriterionState(tx dbtx, taskID int64, ref string, state CriterionState) (resolved Criterion, prior CriterionState, err error) {
	if _, err := ValidateCriterionState(string(state)); err != nil {
		return Criterion{}, "", err
	}
	// Try short_id first.
	row := tx.QueryRow(
		"SELECT id, COALESCE(short_id, ''), label, state FROM task_criteria WHERE task_id = ? AND short_id = ?",
		taskID, ref,
	)
	var found Criterion
	var existingState string
	if err := row.Scan(&found.ID, &found.ShortID, &found.Label, &existingState); err != nil {
		if err != sql.ErrNoRows {
			return Criterion{}, "", err
		}
		// Fall back to label match (including legacy rows whose short_id
		// pre-dates the backfill, and the historical CLI form).
		row = tx.QueryRow(
			"SELECT id, COALESCE(short_id, ''), label, state FROM task_criteria WHERE task_id = ? AND label = ?",
			taskID, ref,
		)
		if err := row.Scan(&found.ID, &found.ShortID, &found.Label, &existingState); err != nil {
			if err == sql.ErrNoRows {
				return Criterion{}, "", fmt.Errorf("no criterion %q on task", ref)
			}
			return Criterion{}, "", err
		}
	}
	now := CurrentNowFunc().Unix()
	if _, err := tx.Exec(
		"UPDATE task_criteria SET state = ?, updated_at = ? WHERE id = ?",
		string(state), now, found.ID,
	); err != nil {
		return Criterion{}, "", err
	}
	found.State = state
	found.TaskID = taskID
	return found, CriterionState(existingState), nil
}

// criteriaEventDetail shapes a list of Criterion records for inclusion in an
// event detail JSON payload. The short_id rides along so the JS replay-fold
// can establish the criterion's stable identity at criteria_added time and
// then match subsequent criterion_state events by short_id rather than by
// label.
func criteriaEventDetail(items []Criterion) []CriterionEntry {
	out := make([]CriterionEntry, 0, len(items))
	for _, c := range items {
		out = append(out, CriterionEntry{
			Label:   c.Label,
			State:   string(c.State),
			ShortID: c.ShortID,
		})
	}
	return out
}

// RunAddCriteria appends new criteria to an existing task, records a
// criteria_added event, and extends the actor's claim if held.
func RunAddCriteria(db *sql.DB, shortID string, items []Criterion, actor string) ([]Criterion, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no criteria supplied")
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return nil, err
	}
	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found", shortID)
	}

	inserted, err := insertCriteria(tx, task.ID, items)
	if err != nil {
		return nil, err
	}
	if err := recordEvent(tx, task.ID, EventCriteriaAdded, actor, CriteriaAddedPayload{
		Criteria: criteriaEventDetail(inserted),
	}); err != nil {
		return nil, err
	}
	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return inserted, nil
}

// RunSetCriterion updates one criterion's state on a task and records a
// criterion_state event. `ref` may be either the criterion's short_id or
// its verbatim label; the resolved criterion's stable identifiers (short
// id + label) are recorded on the event so the JS replay-fold can match
// by short_id while the human-readable label remains available for
// rendering and as a legacy-event fallback.
func RunSetCriterion(db *sql.DB, taskShortID, ref string, state CriterionState, actor string) (prior CriterionState, err error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return "", err
	}
	task, err := GetTaskByShortID(tx, taskShortID)
	if err != nil {
		return "", err
	}
	if task == nil {
		return "", fmt.Errorf("task %q not found", taskShortID)
	}
	resolved, prior, err := SetCriterionState(tx, task.ID, ref, state)
	if err != nil {
		return "", err
	}
	payload := CriterionStatePayload{
		Label: resolved.Label,
		State: string(state),
		Prior: string(prior),
	}
	if resolved.ShortID != "" {
		payload.ShortID = resolved.ShortID
	}
	if err := recordEvent(tx, task.ID, EventCriterionState, actor, payload); err != nil {
		return "", err
	}
	if err := maybeExtendClaim(tx, task.ID, actor); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return prior, nil
}

// CountPendingCriteria returns the number of criteria currently in pending state.
func CountPendingCriteria(db dbtx, taskID int64) (int, error) {
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM task_criteria WHERE task_id = ? AND state = 'pending'",
		taskID,
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// PendingCriteriaByShortID returns, for each shortID with pending criteria,
// the count keyed by shortID. shortIDs that don't resolve, or whose tasks
// have no pending criteria, are omitted from the map. One query, not N+1.
func PendingCriteriaByShortID(db *sql.DB, shortIDs []string) (map[string]int, error) {
	if len(shortIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(shortIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(shortIDs))
	for i, id := range shortIDs {
		args[i] = id
	}
	q := `
		SELECT t.short_id, COUNT(c.id)
		FROM tasks t
		JOIN task_criteria c ON c.task_id = t.id AND c.state = 'pending'
		WHERE t.short_id IN (` + placeholders + `) AND t.deleted_at IS NULL
		GROUP BY t.short_id
		HAVING COUNT(c.id) > 0
	`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int, len(shortIDs))
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
