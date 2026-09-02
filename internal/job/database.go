package job

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/bensyverson/jobs/internal/eventlog"
	"github.com/bensyverson/jobs/internal/migrations"
	_ "modernc.org/sqlite"
)

var CurrentNowFunc = time.Now

const defaultDBName = ".jobs.db"
const base62Chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Short id widths. Tasks mint six characters because two replicas mint
// apart and a collision cannot be remapped once the id is in notes and
// commits; criteria mint three, unique within their task
// (project/2026-09-01-git-native-event-log.md, decision 1).
const (
	shortIDLen          = 6
	criterionShortIDLen = 3
)

type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func ResolveDBPath(dbFlag string) string {
	if dbFlag != "" {
		return dbFlag
	}
	if env := os.Getenv("JOBS_DB"); env != "" {
		return env
	}
	if found := findAncestorDB(); found != "" {
		return found
	}
	return defaultDBName
}

// ResolveDBPathForInit is used by `job init` to pick a destination path. It
// intentionally does NOT walk up looking for an ancestor database: running
// `job init` in a subfolder of an existing project creates a new db in cwd,
// the same way `git init` inside a git repo creates a new repo.
func ResolveDBPathForInit(dbFlag string) string {
	if dbFlag != "" {
		return dbFlag
	}
	if env := os.Getenv("JOBS_DB"); env != "" {
		return env
	}
	return defaultDBName
}

func findAncestorDB() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, defaultDBName)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
		// A fresh clone carries .jobs/log and no cache at all. The store is
		// the record, so finding it is finding the project; the cache is built
		// on the first command.
		if info, err := os.Stat(eventlog.LogDir(eventlog.StoreDir(candidate))); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// OpenDB opens the cache at path, migrates it, and reconciles it with the
// store beside it — a rebuild from .jobs/log whenever a file's size no longer
// matches the offset the cache applied.
func OpenDB(path string) (*sql.DB, error) {
	// Adoption replaces the file this function is about to open, so it runs
	// first and leaves no handle behind (adopt.go).
	if err := adoptIfLegacy(path); err != nil {
		return nil, err
	}
	db, err := openCache(path)
	if err != nil {
		return nil, err
	}
	sync, err := syncStore(db, path)
	if err != nil {
		db.Close()
		return nil, err
	}
	resolved, err := CachePathOf(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	syncRecords.Store(resolved, sync)
	return db, nil
}

// OpenDBForRecovery opens the cache without reconciling it with the store.
//
// It exists for the one verb that has to work when the rebuild is what failed:
// `job rekey` reads the raw log and appends the decision that unblocks it.
// Nothing else should use it — a cache opened this way may not reflect the
// record.
func OpenDBForRecovery(path string) (*sql.DB, error) {
	return openCache(path)
}

func openCache(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")
	if err := RunMigrations(db, migrations.FS()); err != nil {
		db.Close()
		// The migrator does not know which file it is looking at; name it
		// here so the message points at the cache the user has to fix.
		if ahead, ok := errors.AsType[*SchemaAheadError](err); ok {
			ahead.Path = path
		}
		return nil, err
	}
	// Backfill server-generated short_ids for any criterion rows that
	// pre-date migration 0005. New rows are minted at insertCriteria time;
	// this catches the snapshot existing on disk when the migration runs.
	if err := backfillCriteriaShortIDs(db); err != nil {
		db.Close()
		return nil, err
	}
	// Derive a fractional sort key for every row that still orders by the
	// integer sort_order columns, then drop them. Migration 0009 adds the
	// columns; the keys themselves need the generator in sortkey.go.
	if err := backfillSortKeys(db); err != nil {
		db.Close()
		return nil, err
	}
	// A database written before local.json existed keeps its identity and
	// strict flag in the config table; move them across once so an upgrade
	// never costs a user their default identity.
	if err := seedLocalStateFromConfig(db, path); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func CreateDB(path string) (*sql.DB, error) {
	return OpenDB(path)
}

// generateShortID mints a base62 task id unused in this database.
func generateShortID(tx dbtx) (string, error) {
	for {
		id := make([]byte, shortIDLen)
		for i := range id {
			n, err := rand.Int(rand.Reader, big.NewInt(62))
			if err != nil {
				return "", fmt.Errorf("generate ID: %w", err)
			}
			id[i] = base62Chars[n.Int64()]
		}
		sid := string(id)
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM tasks WHERE short_id = ?)", sid).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return sid, nil
		}
	}
}

// generateCriterionShortID mints a 3-char base62 short ID unique among the
// criteria of one task. Every lookup and every event carries the task, so
// the id never needs to be unique beyond it, and a per-task scope keeps
// two replicas minting apart from colliding.
func generateCriterionShortID(tx dbtx, taskID int64) (string, error) {
	for {
		id := make([]byte, criterionShortIDLen)
		for i := range id {
			n, err := rand.Int(rand.Reader, big.NewInt(62))
			if err != nil {
				return "", fmt.Errorf("generate criterion ID: %w", err)
			}
			id[i] = base62Chars[n.Int64()]
		}
		sid := string(id)
		var exists bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM task_criteria WHERE task_id = ? AND short_id = ?)", taskID, sid).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return sid, nil
		}
	}
}

// backfillCriteriaShortIDs walks any criterion rows that still carry a
// NULL short_id (i.e. were inserted before migration 0005 added the
// column) and mints one for each. New rows go through insertCriteria,
// which mints inline; this only fills the snapshot present on disk at
// migration time.
func backfillCriteriaShortIDs(db *sql.DB) error {
	rows, err := db.Query("SELECT id, task_id FROM task_criteria WHERE short_id IS NULL")
	if err != nil {
		return fmt.Errorf("backfill criteria short_ids: query: %w", err)
	}
	type row struct{ id, taskID int64 }
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.taskID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	rows.Close()
	if len(pending) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range pending {
		sid, err := generateCriterionShortID(tx, r.taskID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE task_criteria SET short_id = ? WHERE id = ?", sid, r.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func GetTaskByShortID(tx dbtx, shortID string) (*Task, error) {
	return getTaskByShortIDFilter(tx, shortID, true)
}

func getTaskByShortIDIncludeDeleted(tx dbtx, shortID string) (*Task, error) {
	return getTaskByShortIDFilter(tx, shortID, false)
}

func getTaskByShortIDFilter(tx dbtx, shortID string, excludeDeleted bool) (*Task, error) {
	q := `
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE short_id = ?`
	if excludeDeleted {
		q += " AND deleted_at IS NULL"
	}
	row := tx.QueryRow(q, shortID)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func loadAllTasks(db *sql.DB) ([]*Task, error) {
	rows, err := db.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE deleted_at IS NULL ORDER BY parent_id, sort_key
	`)
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

func buildTree(tasks []*Task) []*TaskNode {
	byID := make(map[int64]*TaskNode)
	for _, t := range tasks {
		byID[t.ID] = &TaskNode{Task: t}
	}
	var roots []*TaskNode
	for _, t := range tasks {
		node := byID[t.ID]
		if t.ParentID == nil {
			roots = append(roots, node)
		} else if parent, ok := byID[*t.ParentID]; ok {
			parent.Children = append(parent.Children, node)
		}
	}
	return roots
}

func filterTree(nodes []*TaskNode, showAll bool, blockedIDs map[int64]bool) []*TaskNode {
	if showAll {
		return nodes
	}
	var result []*TaskNode
	for _, node := range nodes {
		// Default `list` shows only actionable work — open + unblocked + unclaimed.
		// Done and canceled tasks are explicitly hidden; pass `all` to surface them.
		if node.Task.Status != "available" || blockedIDs[node.Task.ID] {
			continue
		}
		result = append(result, &TaskNode{
			Task:     node.Task,
			Children: filterTree(node.Children, false, blockedIDs),
		})
	}
	return result
}

func getBlockedTaskIDs(db *sql.DB) (map[int64]bool, error) {
	rows, err := db.Query(`
		SELECT DISTINCT b.blocked_id
		FROM blocks b
		JOIN tasks t ON t.id = b.blocker_id
		WHERE t.status != 'done' AND t.deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result[id] = true
	}
	return result, rows.Err()
}

func findNodeByShortID(nodes []*TaskNode, shortID string) *TaskNode {
	for _, node := range nodes {
		if node.Task.ShortID == shortID {
			return node
		}
		if found := findNodeByShortID(node.Children, shortID); found != nil {
			return found
		}
	}
	return nil
}

func GetLatestEventDetail(tx dbtx, taskID int64, eventType string) (map[string]any, error) {
	var detail string
	err := tx.QueryRow(
		"SELECT detail FROM events WHERE task_id = ? AND event_type = ? ORDER BY created_at DESC, id DESC LIMIT 1",
		taskID, eventType,
	).Scan(&detail)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(detail), &result); err != nil {
		return nil, err
	}
	return result, nil
}

// findClosedDescendants returns every descendant whose status is either
// "done" or "canceled". Used by `reopen --cascade` to revive a closed subtree.
func findClosedDescendants(tx dbtx, taskID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if t.Status == "done" || t.Status == "canceled" {
			result = append(result, t)
		}
		desc, err := findClosedDescendants(tx, t.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, desc...)
	}
	return result, rows.Err()
}

func findDoneDescendants(tx dbtx, taskID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		if t.Status == "done" {
			result = append(result, t)
		}
		desc, err := findDoneDescendants(tx, t.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, desc...)
	}
	return result, rows.Err()
}

// findOpenDescendants returns every descendant of taskID whose status is
// neither "done" nor "canceled". Used by `cancel --cascade` to walk the live
// subtree under a task being canceled.
func findOpenDescendants(tx dbtx, taskID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND `+openChildFilter(""), taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
		desc, err := findOpenDescendants(tx, t.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, desc...)
	}
	return result, rows.Err()
}

// findAllDescendants returns every descendant of taskID regardless of status.
// Used by `cancel --purge --cascade` which erases the entire subtree.
func findAllDescendants(tx dbtx, taskID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND deleted_at IS NULL
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
		desc, err := findAllDescendants(tx, t.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, desc...)
	}
	return result, rows.Err()
}

func findIncompleteDescendants(tx dbtx, taskID int64) ([]*Task, error) {
	rows, err := tx.Query(`
		SELECT id, short_id, parent_id, title, description, status, sort_key,
		       claimed_by, claim_expires_at, completion_note, created_at, updated_at, deleted_at, kind
		FROM tasks WHERE parent_id = ? AND status != 'done' AND deleted_at IS NULL
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
		desc, err := findIncompleteDescendants(tx, t.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, desc...)
	}
	return result, rows.Err()
}

func childShortIDs(tx dbtx, parentID int64) ([]string, error) {
	rows, err := tx.Query("SELECT short_id FROM tasks WHERE parent_id = ? AND status != 'done' AND deleted_at IS NULL", parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetEventsForTaskTree returns events for the task identified by shortID and
// all of its descendants. When shortID is empty, the anchor is every
// top-level task (parent_id IS NULL) and the query walks down every tree —
// effectively "all events in the database". This is the global-scope form
// powering `job log` and `job tail` with no positional arg.
func GetEventsForTaskTree(db *sql.DB, shortID string) ([]EventEntry, error) {
	query, args := buildTreeEventsQuery(shortID, "", nil)
	return queryEventEntries(db, query, args)
}

func getEventsForTaskTreeSince(db *sql.DB, shortID string, since int64) ([]EventEntry, error) {
	query, args := buildTreeEventsQuery(shortID, "AND e.created_at >= ?", []any{since})
	return queryEventEntries(db, query, args)
}

// EventPositionExpr is the SQL row-value tuple naming an event's log
// position, for a table aliased as `alias`. A legacy row (rep ”) has no seq
// of its own, so the cache's row id stands in for it — the same substitution
// [EventEntry.Position] makes, kept here so SQL and Go agree.
//
// Ordering by this tuple, and comparing a cursor against it, is what makes a
// cursor survive a rebuild: e.id is renumbered by a replay, and these three
// columns are not.
func EventPositionExpr(alias string) string {
	return fmt.Sprintf("(%[1]s.ts, %[1]s.rep, CASE WHEN %[1]s.rep = '' THEN %[1]s.id ELSE %[1]s.seq END)", alias)
}

// EventPositionArgs are the bind values matching EventPositionExpr, in order.
func EventPositionArgs(p eventlog.Position) []any {
	return []any{p.TS, p.Rep, int64(p.Seq)}
}

// GetEventsAfterPosition returns events ordered by log position strictly
// after `after`, for the subtree rooted at shortID (or the entire DB when
// shortID is empty). A zero Position means "from the beginning".
//
// This is the tail cursor: `job tail`, the web broadcaster's poll loop and
// the /events SSE backfill all page through the log with it.
func GetEventsAfterPosition(db *sql.DB, shortID string, after eventlog.Position) ([]EventEntry, error) {
	if after == (eventlog.Position{}) {
		return GetEventsForTaskTree(db, shortID)
	}
	where := "AND " + EventPositionExpr("e") + " > (?, ?, ?)"
	query, args := buildTreeEventsQuery(shortID, where, EventPositionArgs(after))
	return queryEventEntries(db, query, args)
}

// GetEventsUpToPosition is the mirror: events at or before `at`. It backs the
// dashboard's ?at= time-travel bound.
func GetEventsUpToPosition(db *sql.DB, shortID string, at eventlog.Position) ([]EventEntry, error) {
	if at == (eventlog.Position{}) {
		return nil, nil
	}
	where := "AND " + EventPositionExpr("e") + " <= (?, ?, ?)"
	query, args := buildTreeEventsQuery(shortID, where, EventPositionArgs(at))
	return queryEventEntries(db, query, args)
}

// GetHeadPosition returns the position of the newest event in the cache, or
// the zero Position when there are none. Used as the "stream from now" cutoff
// and as the head cursor the dashboard's scrubber hydrates from.
func GetHeadPosition(db *sql.DB) (eventlog.Position, error) {
	var ts sql.NullInt64
	var rep sql.NullString
	var seq sql.NullInt64
	err := db.QueryRow(`SELECT ts, rep, CASE WHEN rep = '' THEN id ELSE seq END
		FROM events ORDER BY ts DESC, rep DESC, CASE WHEN rep = '' THEN id ELSE seq END DESC LIMIT 1`).
		Scan(&ts, &rep, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		return eventlog.Position{}, nil
	}
	if err != nil {
		return eventlog.Position{}, err
	}
	return eventlog.Position{TS: ts.Int64, Rep: rep.String, Seq: uint64(seq.Int64)}, nil
}

// CountEvents returns how many events the readers can see — the same scope
// GetEventsForTaskTree walks. It is the head frame's ordinal for the
// dashboard's replay buffer.
func CountEvents(db *sql.DB) (int, error) {
	events, err := GetEventsForTaskTree(db, "")
	if err != nil {
		return 0, err
	}
	return len(events), nil
}

// buildTreeEventsQuery assembles the recursive-CTE query used by log/tail.
// When shortID is empty the anchor is all top-level tasks (global scope);
// otherwise it is that one task (single-subtree scope). extraWhere is an
// optional trailing predicate on e.*, and extraArgs are its bind values.
func buildTreeEventsQuery(shortID, extraWhere string, extraArgs []any) (string, []any) {
	var anchorClause string
	var args []any
	if shortID == "" {
		anchorClause = "parent_id IS NULL AND deleted_at IS NULL"
	} else {
		anchorClause = "short_id = ? AND deleted_at IS NULL"
		args = append(args, shortID)
	}
	args = append(args, extraArgs...)

	query := `
		WITH RECURSIVE tree AS (
			SELECT id FROM tasks WHERE ` + anchorClause + `
			UNION ALL
			SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id WHERE t.deleted_at IS NULL
		)
		SELECT e.id, e.task_id, t.short_id, e.event_type, e.actor, e.detail, e.created_at,
		       e.ts, e.rep, e.seq
		FROM events e
		JOIN tasks t ON t.id = e.task_id
		WHERE e.task_id IN (SELECT id FROM tree) ` + extraWhere + `
		ORDER BY e.ts, e.rep, CASE WHEN e.rep = '' THEN e.id ELSE e.seq END
	`
	return query, args
}

func queryEventEntries(db *sql.DB, query string, args []any) ([]EventEntry, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EventEntry
	for rows.Next() {
		var e EventEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.ShortID, &e.EventType, &e.Actor, &e.Detail, &e.CreatedAt,
			&e.TS, &e.Rep, &e.Seq); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
