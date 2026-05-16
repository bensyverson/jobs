package job

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// openLeavesUnder returns up to `limit` short IDs of open (status not in
// done/canceled, deleted_at NULL) leaves under taskID — leaves being tasks
// that themselves have no open children. Used by RunClaim's parent-rejection
// error to inline a few claimable candidates.
func openLeavesUnder(tx dbtx, taskID int64, limit int) ([]string, error) {
	rows, err := tx.Query(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
			UNION ALL
			SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT t.short_id FROM tasks t
		WHERE t.id IN (SELECT id FROM subtree)
		  AND t.status NOT IN ('done', 'canceled')
		  AND NOT EXISTS (
			  SELECT 1 FROM tasks c
			  WHERE c.parent_id = t.id
			    AND c.status NOT IN ('done', 'canceled')
			    AND c.deleted_at IS NULL
		  )
		ORDER BY t.sort_order
		LIMIT ?
	`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

const DefaultClaimTTLSeconds int64 = 1800

func ParseDuration(s string) (int64, error) {
	if s == "" {
		return DefaultClaimTTLSeconds, nil
	}

	last := s[len(s)-1]
	numStr := s[:len(s)-1]
	if len(numStr) == 0 {
		return 0, fmt.Errorf("invalid duration %q: missing number", s)
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}

	switch last {
	case 's':
		return num, nil
	case 'm':
		return num * 60, nil
	case 'h':
		return num * 3600, nil
	case 'd':
		return num * 86400, nil
	default:
		return 0, fmt.Errorf("invalid duration %q: unknown unit %q", s, string(last))
	}
}

func checkClaimOwnership(tx dbtx, shortID, caller string) error {
	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}
	if task.Status != "claimed" || task.ClaimedBy == nil {
		return nil
	}
	if *task.ClaimedBy == caller {
		return nil
	}

	var callerOnceHeld bool
	err = tx.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM events
			WHERE task_id = ? AND event_type = 'claimed' AND actor = ?
		)`, task.ID, caller,
	).Scan(&callerOnceHeld)
	if err != nil {
		return err
	}

	now := CurrentNowFunc().Unix()

	if callerOnceHeld {
		var claimedAt int64
		if err := tx.QueryRow(
			`SELECT created_at FROM events
			 WHERE task_id = ? AND event_type = 'claimed' AND actor = ?
			 ORDER BY created_at DESC, id DESC LIMIT 1`,
			task.ID, *task.ClaimedBy,
		).Scan(&claimedAt); err != nil && err != sql.ErrNoRows {
			return err
		}
		ago := max(now-claimedAt, 0)
		return fmt.Errorf("your claim on %s expired; it is now held by %s (claimed %s ago). Run 'claim %s' to take it back.",
			shortID, *task.ClaimedBy, FormatDuration(ago), shortID)
	}

	expires := "0s"
	if task.ClaimExpiresAt != nil {
		left := *task.ClaimExpiresAt - now
		if left > 0 {
			expires = FormatDuration(left)
		}
	}
	return fmt.Errorf("task %s is claimed by %s (expires in %s). Wait for expiry, or ask %s to release.",
		shortID, *task.ClaimedBy, expires, *task.ClaimedBy)
}

// maybeExtendClaim refreshes the claim TTL on a task that actor currently
// holds. Called as a side effect of writes (note, edit, label add/remove)
// so an agent actively working on a claimed task doesn't need to call
// heartbeat explicitly. Rules:
//   - Only extend when the caller IS the current claim holder.
//   - Only extend; never shorten. If the existing claim_expires_at is
//     further in the future than now + DefaultClaimTTLSeconds, leave it.
//   - No event is recorded — the write's own event (noted/edited/labeled)
//     already signals activity; the TTL bump is a silent side effect.
func maybeExtendClaim(tx dbtx, taskID int64, actor string) error {
	var claimedBy sql.NullString
	var claimExpiresAt sql.NullInt64
	err := tx.QueryRow(
		"SELECT claimed_by, claim_expires_at FROM tasks WHERE id = ?",
		taskID,
	).Scan(&claimedBy, &claimExpiresAt)
	if err != nil {
		return err
	}
	if !claimedBy.Valid || claimedBy.String != actor || !claimExpiresAt.Valid {
		return nil
	}
	newExpiry := CurrentNowFunc().Unix() + DefaultClaimTTLSeconds
	if newExpiry <= claimExpiresAt.Int64 {
		return nil
	}
	_, err = tx.Exec(
		"UPDATE tasks SET claim_expires_at = ? WHERE id = ?",
		newExpiry, taskID,
	)
	return err
}

func expireStaleClaims(db *sql.DB, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}
	return tx.Commit()
}

func expireStaleClaimsInTx(tx dbtx, actor string) error {
	now := CurrentNowFunc().Unix()
	rows, err := tx.Query(
		"SELECT id, claimed_by, claim_expires_at FROM tasks WHERE status = 'claimed' AND claim_expires_at < ? AND deleted_at IS NULL",
		now,
	)
	if err != nil {
		return err
	}

	type expired struct {
		id        int64
		claimedBy *string
		expiresAt int64
	}
	var expiredClaims []expired
	for rows.Next() {
		var e expired
		var claimedBy sql.NullString
		var expiresAt sql.NullInt64
		if err := rows.Scan(&e.id, &claimedBy, &expiresAt); err != nil {
			rows.Close()
			return err
		}
		if claimedBy.Valid {
			cb := claimedBy.String
			e.claimedBy = &cb
		}
		if expiresAt.Valid {
			e.expiresAt = expiresAt.Int64
		}
		expiredClaims = append(expiredClaims, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range expiredClaims {
		if _, err := tx.Exec(
			"UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
			now, e.id,
		); err != nil {
			return err
		}
		var wasClaimedBy string
		if e.claimedBy != nil {
			wasClaimedBy = *e.claimedBy
		}
		if err := recordEvent(tx, e.id, "claim_expired", actor, map[string]any{
			"was_claimed_by": wasClaimedBy,
			"was_expires_at": e.expiresAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// openChildFilter returns the SQL fragment that selects rows counting as
// "open children" under leaf-frontier semantics: not yet done, not
// canceled, not soft-deleted. Shared by countOpenChildren,
// queryAvailableLeafFrontier, cascadeAutoCloseAncestors, and
// findOpenDescendants so the four sites can't drift.
//
// alias is the table alias the caller uses (e.g. "c" in a subtree join,
// or "" when querying the bare tasks table). The helper returns the
// fragment pre-prefixed so callers can concatenate it into a WHERE.
func openChildFilter(alias string) string {
	if alias == "" {
		return "status NOT IN ('done', 'canceled') AND deleted_at IS NULL"
	}
	return alias + ".status NOT IN ('done', 'canceled') AND " + alias + ".deleted_at IS NULL"
}

// countOpenChildren returns the number of direct children of taskID whose
// status is neither "done" nor "canceled". Used to enforce leaf-frontier
// claim semantics: a task with open children has no executable work of its
// own and should not be claimed directly.
func countOpenChildren(tx dbtx, taskID int64) (int, error) {
	var n int
	err := tx.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE parent_id = ? AND `+openChildFilter(""),
		taskID,
	).Scan(&n)
	return n, err
}

// CountOpenChildrenOfShortID returns the number of open direct children of
// the task with the given short_id. "Open" matches the leaf-frontier filter
// shared with countOpenChildren / queryAvailableLeafFrontier. Returns
// (0, false, nil) if the parent short_id doesn't resolve.
func CountOpenChildrenOfShortID(db *sql.DB, parentShortID string) (count int, found bool, err error) {
	parent, err := GetTaskByShortID(db, parentShortID)
	if err != nil {
		return 0, false, err
	}
	if parent == nil {
		return 0, false, nil
	}
	n, err := countOpenChildren(db, parent.ID)
	if err != nil {
		return 0, true, err
	}
	return n, true, nil
}

// GetTaskByID returns the task with the given internal id, or nil if no
// matching row exists. Public counterpart to getTaskByID for callers (e.g.
// cmd/job) that hold a *sql.DB rather than a dbtx.
func GetTaskByID(db *sql.DB, id int64) (*Task, error) {
	return getTaskByID(db, id)
}

// RunClaim claims the task identified by shortID for actor. If note is
// non-empty, a `noted` event is recorded in the same transaction as the
// `claimed` event and lands first in the timeline, so an agent's starting
// context anchors the work rather than trailing it. The pattern mirrors
// RunRelease's note-then-event ordering — same tx, same atomicity contract.
func RunClaim(db *sql.DB, shortID, duration, note, actor string, force bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}
	if task.Status == "done" {
		return fmt.Errorf("task %s is done", shortID)
	}
	openChildren, err := countOpenChildren(tx, task.ID)
	if err != nil {
		return err
	}
	if openChildren > 0 {
		leaves, lerr := openLeavesUnder(tx, task.ID, 5)
		if lerr == nil && len(leaves) > 0 {
			return fmt.Errorf(
				"task %s has %d open children; claim a leaf instead. Open leaves: %s. (Run 'next %s all' for the full frontier.)",
				shortID, openChildren, strings.Join(leaves, ", "), shortID,
			)
		}
		return fmt.Errorf(
			"task %s has %d open children; claim a leaf instead, or run 'next %s all' to see them",
			shortID, openChildren, shortID,
		)
	}
	if task.Status == "claimed" && !force {
		holder := ""
		if task.ClaimedBy != nil {
			holder = *task.ClaimedBy
		}
		if holder == actor {
			return fmt.Errorf("task %s is already claimed by you. Use 'heartbeat' to refresh, or 'release' to stop.", shortID)
		}
		expires := "0s"
		if task.ClaimExpiresAt != nil {
			left := *task.ClaimExpiresAt - CurrentNowFunc().Unix()
			if left > 0 {
				expires = FormatDuration(left)
			}
		}
		return fmt.Errorf("task %s is claimed by %s (expires in %s). Wait for expiry, or ask %s to release.",
			shortID, holder, expires, holder)
	}

	seconds, err := ParseDuration(duration)
	if err != nil {
		return err
	}

	now := CurrentNowFunc().Unix()
	expiresAt := now + seconds

	// Capture override breadcrumbs before mutating: when --force takes
	// over an active claim, was_claimed_by / was_expires_at let
	// consumers reverse-fold to the prior holder.
	overrode := task.Status == "claimed" && force
	var overriddenBy string
	var overriddenExpires int64
	if overrode {
		if task.ClaimedBy != nil {
			overriddenBy = *task.ClaimedBy
		}
		if task.ClaimExpiresAt != nil {
			overriddenExpires = *task.ClaimExpiresAt
		}
	}

	// Note lands BEFORE the claim event so the starting context anchors
	// the lifecycle at its head. Atomic with the claim — both or neither.
	if note != "" {
		if err := recordEvent(tx, task.ID, "noted", actor, map[string]any{"text": note}); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(
		"UPDATE tasks SET status = 'claimed', claimed_by = ?, claim_expires_at = ?, updated_at = ? WHERE id = ?",
		actor, expiresAt, now, task.ID,
	); err != nil {
		return err
	}

	detail := map[string]any{
		"duration":   duration,
		"expires_at": expiresAt,
	}
	if overrode {
		detail["was_claimed_by"] = overriddenBy
		detail["was_expires_at"] = overriddenExpires
	}
	if err := recordEvent(tx, task.ID, "claimed", actor, detail); err != nil {
		return err
	}

	return tx.Commit()
}

// RunRelease releases the caller's claim on a task. If note is non-empty, a
// noted event is recorded in the same transaction so a release-with-note is
// atomic — either both land or neither does.
func RunRelease(db *sql.DB, shortID, note, actor string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := expireStaleClaimsInTx(tx, actor); err != nil {
		return err
	}

	task, err := GetTaskByShortID(tx, shortID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("task %q not found", shortID)
	}
	if task.Status != "claimed" {
		return fmt.Errorf("task %s is not claimed (status: %s)", shortID, task.Status)
	}
	if task.ClaimedBy == nil || *task.ClaimedBy != actor {
		holder := ""
		if task.ClaimedBy != nil {
			holder = *task.ClaimedBy
		}
		return fmt.Errorf("task %s is claimed by %s, not you. 'release' operates only on your own claims.",
			shortID, holder)
	}

	if note != "" {
		if err := recordEvent(tx, task.ID, "noted", actor, map[string]any{"text": note}); err != nil {
			return err
		}
	}

	now := CurrentNowFunc().Unix()
	var wasClaimedBy string
	if task.ClaimedBy != nil {
		wasClaimedBy = *task.ClaimedBy
	}
	var wasExpiresAt int64
	if task.ClaimExpiresAt != nil {
		wasExpiresAt = *task.ClaimExpiresAt
	}
	if _, err := tx.Exec(
		"UPDATE tasks SET status = 'available', claimed_by = NULL, claim_expires_at = NULL, updated_at = ? WHERE id = ?",
		now, task.ID,
	); err != nil {
		return err
	}

	if err := recordEvent(tx, task.ID, "released", actor, map[string]any{
		"was_claimed_by": wasClaimedBy,
		"was_expires_at": wasExpiresAt,
	}); err != nil {
		return err
	}

	return tx.Commit()
}

// queryAvailableTasks returns the available, unblocked, unclaimed tasks under
// the given parent (or root tasks when parentShortID is empty), in sort order.
// Used by both `next` (single) and `next all` (frontier). When labelName is
// non-empty, only tasks carrying that label are returned.
//
// Leaf-frontier semantics: by default, tasks with open children are excluded
// (they are not claimable work themselves — their children are). The search
// descends through such parents and surfaces their leaf descendants instead.
// Passing includeParents=true restores the pre-leaf-frontier behavior of
// returning direct children of the scope only.
//
// "Open children" means status NOT IN ('done', 'canceled'). A task whose
// children are all closed is itself treated as a leaf.
func queryAvailableTasks(db *sql.DB, parentShortID string, limit int, labelName string, includeParents bool) ([]*Task, error) {
	var parentID *int64
	if parentShortID != "" {
		parent, err := GetTaskByShortID(db, parentShortID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, fmt.Errorf("task %q not found", parentShortID)
		}
		parentID = &parent.ID
	}

	if includeParents {
		return queryAvailableDirectChildren(db, parentID, limit, labelName)
	}
	return queryAvailableLeafFrontier(db, parentID, limit, labelName)
}

// queryAvailableDirectChildren implements the legacy behavior used by
// --include-parents: return direct children of the scope (or root tasks),
// regardless of whether they have open children of their own.
func queryAvailableDirectChildren(db *sql.DB, parentID *int64, limit int, labelName string) ([]*Task, error) {
	var parentFilter string
	var args []any
	if parentID != nil {
		parentFilter = "AND t.parent_id = ?"
		args = append(args, *parentID)
	} else {
		parentFilter = "AND t.parent_id IS NULL"
	}

	labelFilter := ""
	if labelName != "" {
		labelFilter = "AND EXISTS (SELECT 1 FROM task_labels tl WHERE tl.task_id = t.id AND tl.name = ?)"
		args = append(args, labelName)
	}

	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}

	query := fmt.Sprintf(`
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_order,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at
		FROM tasks t
		WHERE t.status = 'available' AND t.deleted_at IS NULL %s %s
		  AND NOT EXISTS (
		    SELECT 1 FROM blocks b
		    JOIN tasks bt ON bt.id = b.blocker_id
		    WHERE b.blocked_id = t.id AND bt.status != 'done' AND bt.deleted_at IS NULL
		  )
		ORDER BY t.sort_order%s
	`, parentFilter, labelFilter, limitClause)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// queryAvailableLeafFrontier implements leaf-frontier semantics: descend
// through the subtree rooted at scope (or all roots) and return tasks that
// are available, unblocked, and have no open children. Results are ordered
// by depth-first sort_order traversal so sibling-declaration order is
// preserved.
func queryAvailableLeafFrontier(db *sql.DB, parentID *int64, limit int, labelName string) ([]*Task, error) {
	var anchorFilter string
	var args []any
	if parentID != nil {
		anchorFilter = "t.parent_id = ?"
		args = append(args, *parentID)
	} else {
		anchorFilter = "t.parent_id IS NULL"
	}

	labelFilter := ""
	if labelName != "" {
		labelFilter = "AND EXISTS (SELECT 1 FROM task_labels tl WHERE tl.task_id = t.id AND tl.name = ?)"
		args = append(args, labelName)
	}

	limitClause := ""
	if limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", limit)
	}

	// The recursive CTE builds a depth-first walk of the subtree. sort_path
	// concatenates zero-padded sort_order values at each level so that
	// lexicographic ordering matches preorder traversal. Six digits of padding
	// accommodates sort_order values up to 999999, well above any realistic
	// sibling count.
	query := fmt.Sprintf(`
		WITH RECURSIVE subtree(id, sort_path) AS (
			SELECT t.id, printf('%%06d', t.sort_order)
			FROM tasks t
			WHERE %s AND t.deleted_at IS NULL
			UNION ALL
			SELECT t.id, s.sort_path || '/' || printf('%%06d', t.sort_order)
			FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_order,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at
		FROM tasks t JOIN subtree s ON s.id = t.id
		WHERE t.status = 'available' AND t.deleted_at IS NULL %s
		  AND NOT EXISTS (
		    SELECT 1 FROM blocks b
		    JOIN tasks bt ON bt.id = b.blocker_id
		    WHERE b.blocked_id = t.id AND bt.status != 'done' AND bt.deleted_at IS NULL
		  )
		  AND NOT EXISTS (
		    SELECT 1 FROM tasks c
		    WHERE c.parent_id = t.id
		      AND %s
		  )
		ORDER BY s.sort_path%s
	`, anchorFilter, labelFilter, openChildFilter("c"), limitClause)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func RunNext(db *sql.DB, parentShortID, actor string) (*Task, error) {
	return RunNextFiltered(db, parentShortID, actor, "", false)
}

func RunNextFiltered(db *sql.DB, parentShortID, actor, labelName string, includeParents bool) (*Task, error) {
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}
	tasks, err := queryAvailableTasks(db, parentShortID, 1, labelName, includeParents)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("No available tasks. Run 'list all' to see blocked or claimed work.")
	}
	return tasks[0], nil
}

func runNextAll(db *sql.DB, parentShortID, actor string) ([]*Task, error) {
	return RunNextAllFiltered(db, parentShortID, actor, "", false)
}

func RunNextAllFiltered(db *sql.DB, parentShortID, actor, labelName string, includeParents bool) ([]*Task, error) {
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}
	return queryAvailableTasks(db, parentShortID, 0, labelName, includeParents)
}

func RunClaimNext(db *sql.DB, parentShortID, duration, actor string, force bool) (*Task, error) {
	return RunClaimNextFiltered(db, parentShortID, duration, actor, force, false)
}

func RunClaimNextFiltered(db *sql.DB, parentShortID, duration, actor string, force, includeParents bool) (*Task, error) {
	task, err := RunNextFiltered(db, parentShortID, actor, "", includeParents)
	if err != nil {
		return nil, err
	}

	if err := RunClaim(db, task.ShortID, duration, "", actor, force); err != nil {
		return nil, err
	}

	task, err = GetTaskByShortID(db, task.ShortID)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func formatClaimExpires(claimedBy *string, claimExpiresAt *int64) string {
	by := ""
	if claimedBy != nil {
		by = " by " + *claimedBy
	}
	if claimExpiresAt != nil {
		remaining := *claimExpiresAt - CurrentNowFunc().Unix()
		if remaining > 0 {
			return fmt.Sprintf("claimed%s, expires in %s", by, FormatDuration(remaining))
		}
		return fmt.Sprintf("claimed%s", by)
	}
	return fmt.Sprintf("claimed%s", by)
}

func parseDurationFromArgs(args []string) (duration string, who string) {
	duration = ""
	who = ""
	byIdx := -1
	for i, a := range args {
		if a == "by" {
			byIdx = i
			break
		}
	}
	if byIdx >= 0 {
		if byIdx > 0 {
			duration = args[0]
		}
		if len(args) > byIdx+1 {
			who = args[byIdx+1]
		}
	} else if len(args) > 0 {
		duration = args[0]
	}
	return
}

func IsDuration(s string) bool {
	if len(s) == 0 {
		return false
	}
	last := s[len(s)-1]
	if last != 's' && last != 'm' && last != 'h' && last != 'd' {
		return false
	}
	numStr := s[:len(s)-1]
	if len(numStr) == 0 {
		return false
	}
	_, err := strconv.ParseInt(numStr, 10, 64)
	return err == nil
}

// ParseNextParentAndDuration parses up to two positional args for `claim
// --next [parent] [duration]` / `next [parent] [duration]`. The first arg may
// be a duration (no parent) or a parent short_id; if the first is a parent,
// the second (if any) is treated as a duration. Extra args are ignored.
func ParseNextParentAndDuration(args []string) (parentShortID, duration string) {
	if len(args) == 0 {
		return "", ""
	}
	if IsDuration(args[0]) {
		return "", args[0]
	}
	parentShortID = args[0]
	if len(args) > 1 {
		duration = args[1]
	}
	return parentShortID, duration
}
