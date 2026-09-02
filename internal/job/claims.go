package job

import (
	"database/sql"
	"fmt"
	"os"
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
		ORDER BY t.sort_key
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
//   - The TTL bump is a state change, so it is an event: a `heartbeat`
//     carrying the new absolute expiry, emitted through the caller's batch.
//     It used to move the column silently, which made it a change no rebuild
//     could reproduce.
func maybeExtendClaim(tx dbtx, b *eventBatch, shortID, actor string) error {
	var claimedBy sql.NullString
	var claimExpiresAt sql.NullInt64
	err := tx.QueryRow(
		"SELECT claimed_by, claim_expires_at FROM tasks WHERE short_id = ?",
		shortID,
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
	return b.emit(tx, EventHeartbeat, shortID, actor, HeartbeatPayload{NewExpiresAt: newExpiry})
}

// expireStaleClaims sweeps expired claims from a read verb.
//
// A read that writes is a wart, but it is the behavior the tracker has: there
// is no daemon, so a claim only lapses when someone looks. What is new is
// that the sweep is a real batch of `claim_expired` events, so a rebuild
// reproduces it — which is why the common case is guarded by a cheap
// existence check first. With nothing stale, a read verb still writes
// nothing at all.
func expireStaleClaims(db *sql.DB, actor string) error {
	var stale bool
	if err := db.QueryRow(
		`SELECT EXISTS(
			SELECT 1 FROM tasks
			WHERE status = 'claimed' AND claim_expires_at < ? AND deleted_at IS NULL)`,
		CurrentNowFunc().Unix(),
	).Scan(&stale); err != nil {
		return err
	}
	if !stale {
		return nil
	}
	return commit(db, func(tx dbtx, b *eventBatch) error {
		return expireStaleClaimsInTx(tx, b, actor)
	})
}

// expireStaleClaimsInTx records a claim_expired for every claim whose
// deadline has passed. The actor on the event is whoever's command noticed —
// the claim's holder is in the payload.
func expireStaleClaimsInTx(tx dbtx, b *eventBatch, actor string) error {
	rows, err := tx.Query(
		"SELECT short_id, claimed_by, claim_expires_at FROM tasks WHERE status = 'claimed' AND claim_expires_at < ? AND deleted_at IS NULL",
		CurrentNowFunc().Unix(),
	)
	if err != nil {
		return err
	}

	type expired struct {
		shortID   string
		claimedBy string
		expiresAt int64
	}
	var expiredClaims []expired
	for rows.Next() {
		var e expired
		var claimedBy sql.NullString
		var expiresAt sql.NullInt64
		if err := rows.Scan(&e.shortID, &claimedBy, &expiresAt); err != nil {
			rows.Close()
			return err
		}
		e.claimedBy = claimedBy.String
		e.expiresAt = expiresAt.Int64
		expiredClaims = append(expiredClaims, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range expiredClaims {
		if err := b.emit(tx, EventClaimExpired, e.shortID, actor, ClaimExpiredPayload{
			WasClaimedBy: e.claimedBy,
			WasExpiresAt: e.expiresAt,
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
	var task *Task
	err := commit(db, func(tx dbtx, b *eventBatch) error {
		if err := expireStaleClaimsInTx(tx, b, actor); err != nil {
			return err
		}

		var err error
		task, err = GetTaskByShortID(tx, shortID)
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

		// The deadline is resolved here, once, and travels in the payload as
		// an absolute second. Apply must never re-derive it from a duration:
		// a replay would then move the deadline to whenever the replay ran.
		expiresAt := CurrentNowFunc().Unix() + seconds

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
			if err := b.emit(tx, EventNoted, shortID, actor, NotedPayload{Text: note}); err != nil {
				return err
			}
		}

		payload := ClaimedPayload{
			Duration:  duration,
			ExpiresAt: expiresAt,
		}
		if overrode {
			payload.WasClaimedBy = overriddenBy
			payload.WasExpiresAt = overriddenExpires
		}
		return b.emit(tx, EventClaimed, shortID, actor, payload)
	})
	if err != nil {
		return err
	}

	// Claiming is the focus setter (last-claim-wins): a claim outside the
	// actor's focused root flips their focus to the new root. Focus is
	// machine-local state in a file, so this happens after the commit — and
	// a failure to write it is a warning, never a failed claim: the claim is
	// the shared fact, the focus is a convenience.
	if err := flipFocusOnClaim(db, task, actor); err != nil {
		fmt.Fprintf(os.Stderr, "warning: claimed %s but could not update focus: %v\n", shortID, err)
	}

	return nil
}

// RunRelease releases the caller's claim on a task. If note is non-empty, a
// noted event is recorded in the same transaction so a release-with-note is
// atomic — either both land or neither does.
func RunRelease(db *sql.DB, shortID, note, actor string) error {
	return commit(db, func(tx dbtx, b *eventBatch) error {
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
			if err := b.emit(tx, EventNoted, shortID, actor, NotedPayload{Text: note}); err != nil {
				return err
			}
		}

		var wasExpiresAt int64
		if task.ClaimExpiresAt != nil {
			wasExpiresAt = *task.ClaimExpiresAt
		}
		return b.emit(tx, EventReleased, shortID, actor, ReleasedPayload{
			WasClaimedBy: *task.ClaimedBy,
			WasExpiresAt: wasExpiresAt,
		})
	})
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
func queryAvailableTasks(db *sql.DB, parentShortID string, limit int, labelName string, includeParents bool, kind TreeKind) ([]*Task, error) {
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
		// An explicit scope is the operator naming the tree they want, so
		// the root-kind default does not apply inside it.
		kind = ""
	}

	if includeParents {
		return queryAvailableDirectChildren(db, parentID, limit, labelName, kind)
	}
	return queryAvailableLeafFrontier(db, parentID, limit, labelName, kind)
}

// queryAvailableDirectChildren implements the legacy behavior used by
// --include-parents: return direct children of the scope (or root tasks),
// regardless of whether they have open children of their own.
func queryAvailableDirectChildren(db *sql.DB, parentID *int64, limit int, labelName string, kind TreeKind) ([]*Task, error) {
	var parentFilter string
	var args []any
	if parentID != nil {
		parentFilter = "AND t.parent_id = ?"
		args = append(args, *parentID)
	} else {
		parentFilter = "AND t.parent_id IS NULL"
		if kind != "" {
			parentFilter += " AND t.kind = ?"
			args = append(args, string(kind))
		}
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
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_key,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM tasks t
		WHERE t.status = 'available' AND t.deleted_at IS NULL %s %s
		  AND NOT (t.parent_id IS NULL AND t.kind = 'issue')
		  AND NOT EXISTS (
		    SELECT 1 FROM blocks b
		    JOIN tasks bt ON bt.id = b.blocker_id
		    WHERE b.blocked_id = t.id AND bt.status != 'done' AND bt.deleted_at IS NULL
		  )
		ORDER BY t.sort_key%s
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
// by depth-first sort_key traversal so sibling-declaration order is
// preserved.
// An issue root is never a candidate in either query: it stays open by
// design once its children close, but it is a container, not work.
func queryAvailableLeafFrontier(db *sql.DB, parentID *int64, limit int, labelName string, kind TreeKind) ([]*Task, error) {
	var anchorFilter string
	var args []any
	if parentID != nil {
		anchorFilter = "t.parent_id = ?"
		args = append(args, *parentID)
	} else {
		anchorFilter = "t.parent_id IS NULL"
		if kind != "" {
			anchorFilter += " AND t.kind = ?"
			args = append(args, string(kind))
		}
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
	// joins each level's sort key with '/', whose byte value is below every
	// character in the sort-key alphabet, so lexicographic ordering of the
	// path matches preorder traversal even where one key is a prefix of
	// another's.
	query := fmt.Sprintf(`
		WITH RECURSIVE subtree(id, sort_path) AS (
			SELECT t.id, t.sort_key
			FROM tasks t
			WHERE %s AND t.deleted_at IS NULL
			UNION ALL
			SELECT t.id, s.sort_path || '/' || t.sort_key
			FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT t.id, t.short_id, t.parent_id, t.title, t.description, t.status, t.sort_key,
		       t.claimed_by, t.claim_expires_at, t.completion_note, t.created_at, t.updated_at, t.deleted_at, t.kind
		FROM tasks t JOIN subtree s ON s.id = t.id
		WHERE t.status = 'available' AND t.deleted_at IS NULL %s
		  AND NOT (t.parent_id IS NULL AND t.kind = 'issue')
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
	return RunNextFiltered(db, parentShortID, actor, "", false, false)
}

// RunNextFiltered walks the claimable frontier. With no explicit parent it
// answers "what is next in my plan": only task-trees are considered. Pass
// issues=true to ask the opposite question — the frontier across issue-trees.
func RunNextFiltered(db *sql.DB, parentShortID, actor, labelName string, includeParents, issues bool) (*Task, error) {
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}

	// With no explicit parent, the actor's focus *of the kind being walked*
	// scopes it: the frontier stays inside the focused root, and an
	// exhausted focused root fails loudly instead of silently crossing into
	// another tree. `--issues` reads the issue focus and falls back to
	// forest-wide when none is set. An explicit parent (or no focus) leaves
	// behavior untouched.
	kind := defaultKindScope(issues)
	scope := parentShortID
	var focus *Task
	if parentShortID == "" {
		var err error
		focus, err = GetFocusKind(db, actor, kind)
		if err != nil {
			return nil, err
		}
		if focus != nil {
			scope = focus.ShortID
		}
	}

	tasks, err := queryAvailableTasks(db, scope, 1, labelName, includeParents, kind)
	if err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		if focus != nil {
			return nil, newErrNoAvailableTasks(fmt.Sprintf(
				"No available tasks in focused root %s %q. Claim in another tree ('claim --next <id>' or 'claim <id>') to shift focus, or release it with 'job focus --release'.",
				focus.ShortID, focus.Title,
			))
		}
		if issues {
			return nil, newErrNoAvailableTasks("No available tasks in any issue tree. Run 'list all' to see blocked or claimed work.")
		}
		return nil, newErrNoAvailableTasks("No available tasks. Run 'list all' to see blocked or claimed work.")
	}
	return tasks[0], nil
}

func runNextAll(db *sql.DB, parentShortID, actor string) ([]*Task, error) {
	return RunNextAllFiltered(db, parentShortID, actor, "", false, false)
}

func RunNextAllFiltered(db *sql.DB, parentShortID, actor, labelName string, includeParents, issues bool) ([]*Task, error) {
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}
	return queryAvailableTasks(db, parentShortID, 0, labelName, includeParents, defaultKindScope(issues))
}

// defaultKindScope maps the --issues switch onto the root kind the unscoped
// frontier filters by.
func defaultKindScope(issues bool) TreeKind {
	if issues {
		return KindIssue
	}
	return KindTask
}

func RunClaimNext(db *sql.DB, parentShortID, duration, actor string, force bool) (*Task, error) {
	return RunClaimNextFiltered(db, parentShortID, duration, "", actor, force, false, false)
}

// RunClaimNextUnderRootOf scopes a follow-on claim to the root subtree of the
// just-closed task: it resolves closedShortID's top ancestor and claims the
// next available leaf within that root. This is the default for
// `done --claim-next` so closing a leaf in one root never hands the worker an
// unrelated leaf in a different root. Pass the resolved root directly to
// RunClaimNextFiltered for an explicit `--under <id>`, or "" for `--any`.
func RunClaimNextUnderRootOf(db *sql.DB, closedShortID, duration, actor string, force bool) (*Task, error) {
	closed, err := GetTaskByShortID(db, closedShortID)
	if err != nil {
		return nil, err
	}
	if closed == nil {
		return nil, fmt.Errorf("task %q not found", closedShortID)
	}
	root, err := findTopAncestor(db, closed)
	if err != nil {
		return nil, err
	}
	return RunClaimNextFiltered(db, root.ShortID, duration, "", actor, force, false, false)
}

// RunClaimNextFiltered picks the next available leaf in scope and claims it.
// note, when non-empty, is recorded on the leaf that was actually picked —
// `claim --next -m` has the same meaning as `claim <id> -m`, and lands in the
// same transaction as the claim itself, so a failed claim leaves no orphan
// note behind.
func RunClaimNextFiltered(db *sql.DB, parentShortID, duration, note, actor string, force, includeParents, issues bool) (*Task, error) {
	task, err := RunNextFiltered(db, parentShortID, actor, "", includeParents, issues)
	if err != nil {
		return nil, err
	}

	if err := RunClaim(db, task.ShortID, duration, note, actor, force); err != nil {
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
