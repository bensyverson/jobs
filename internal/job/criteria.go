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

// planNewCriteria validates a batch of new criteria and mints, per row, the
// short id and the fractional sort key that place it at the end of taskID's
// list. It writes nothing: the values it mints travel in the criteria_added
// event, and applyCriteriaAdded inserts the rows.
//
// Minting here rather than in apply is what makes criteria_added idempotent
// by (task, short id) and its order stable under a shuffle.
func planNewCriteria(tx dbtx, taskID int64, items []Criterion) ([]Criterion, error) {
	if len(items) == 0 {
		return nil, nil
	}

	var lastKey string
	if err := tx.QueryRow(
		"SELECT COALESCE(MAX(sort_key), '') FROM task_criteria WHERE task_id = ?", taskID,
	).Scan(&lastKey); err != nil && err != sql.ErrNoRows {
		return nil, err
	}

	// generateCriterionShortID checks the table, and nothing in this batch is
	// in the table yet, so two rows could otherwise be minted the same id.
	minted := make(map[string]bool, len(items))
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
		var shortID string
		for {
			shortID, err = generateCriterionShortID(tx, taskID)
			if err != nil {
				return nil, err
			}
			if !minted[shortID] {
				break
			}
		}
		minted[shortID] = true
		sortKey, err := KeyBetween(lastKey, "")
		if err != nil {
			return nil, err
		}
		out = append(out, Criterion{
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

// ResolveCriterion finds one criterion on a task by `ref`, which may be
// either its short_id or its verbatim label (short_id is tried first because
// it is the stable identity; label is kept as a fallback for callers without
// it, including the `--criterion "label=state"` form). It reads only: the
// state change is a criterion_state event, and applyCriterionState writes it.
func ResolveCriterion(tx dbtx, taskID int64, ref string) (Criterion, error) {
	row := tx.QueryRow(
		"SELECT id, COALESCE(short_id, ''), label, state, sort_key FROM task_criteria WHERE task_id = ? AND short_id = ?",
		taskID, ref,
	)
	var found Criterion
	var state string
	if err := row.Scan(&found.ID, &found.ShortID, &found.Label, &state, &found.SortKey); err != nil {
		if err != sql.ErrNoRows {
			return Criterion{}, err
		}
		row = tx.QueryRow(
			"SELECT id, COALESCE(short_id, ''), label, state, sort_key FROM task_criteria WHERE task_id = ? AND label = ?",
			taskID, ref,
		)
		if err := row.Scan(&found.ID, &found.ShortID, &found.Label, &state, &found.SortKey); err != nil {
			if err == sql.ErrNoRows {
				return Criterion{}, fmt.Errorf("no criterion %q on task", ref)
			}
			return Criterion{}, err
		}
	}
	found.State = CriterionState(state)
	found.TaskID = taskID
	return found, nil
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
			SortKey: c.SortKey,
		})
	}
	return out
}

// RunAddCriteria appends new criteria to an existing task as a
// criteria_added event, and extends the actor's claim if held.
func RunAddCriteria(db *sql.DB, shortID string, items []Criterion, actor string) ([]Criterion, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no criteria supplied")
	}
	var planned []Criterion
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		task, err := GetTaskByShortID(tx, shortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", shortID)
		}

		planned, err = planNewCriteria(tx, task.ID, items)
		if err != nil {
			return err
		}
		if err := b.emit(tx, EventCriteriaAdded, shortID, actor, CriteriaAddedPayload{
			Criteria: criteriaEventDetail(planned),
		}); err != nil {
			return err
		}
		// Row ids are minted by the cache, so they exist only after apply has
		// run; callers that hold the returned records expect them filled.
		for i := range planned {
			if err := tx.QueryRow(
				"SELECT id FROM task_criteria WHERE task_id = ? AND short_id = ?",
				task.ID, planned[i].ShortID,
			).Scan(&planned[i].ID); err != nil {
				return err
			}
		}
		return maybeExtendClaim(tx, b, task.ShortID, actor)
	})
	if err != nil {
		return nil, err
	}
	return planned, nil
}

// RunSetCriterion records one criterion's new state as a criterion_state
// event. `ref` may be either the criterion's short_id or its verbatim label;
// the resolved criterion's stable identifiers (short id + label) ride on the
// event so apply and the JS replay-fold can both match by short_id while the
// human-readable label remains available for rendering and as a legacy
// fallback.
func RunSetCriterion(db *sql.DB, taskShortID, ref string, state CriterionState, actor string) (prior CriterionState, err error) {
	if _, err := ValidateCriterionState(string(state)); err != nil {
		return "", err
	}
	err = commit(db, func(tx dbtx, b *eventBatch) error {
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}
		task, err := GetTaskByShortID(tx, taskShortID)
		if err != nil {
			return err
		}
		if task == nil {
			return fmt.Errorf("task %q not found", taskShortID)
		}
		resolved, err := ResolveCriterion(tx, task.ID, ref)
		if err != nil {
			return err
		}
		prior = resolved.State
		if err := b.emit(tx, EventCriterionState, taskShortID, actor, CriterionStatePayload{
			Label:   resolved.Label,
			State:   string(state),
			Prior:   string(prior),
			ShortID: resolved.ShortID,
		}); err != nil {
			return err
		}
		return maybeExtendClaim(tx, b, task.ShortID, actor)
	})
	if err != nil {
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
