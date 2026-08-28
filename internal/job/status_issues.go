package job

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// IssuesStatus summarizes work across every issue-tree root, for the
// `status` command's Issues: line (project/2026-08-28-issues-ux.md,
// decision 4). Open counts every non-closed descendant task under an
// issue root (available and claimed both count as open work — Claimed is
// the claimed subset of Open, not a disjoint bucket). Next names the
// leaf `job next --issues` would hand out, nil when nothing is claimable.
type IssuesStatus struct {
	Open    int
	Claimed int
	Next    *Task
}

// BuildIssuesStatus computes the Issues: line's contents, or nil when the
// database has no issue-tree root — the caller omits the line entirely in
// that case.
//
// claimedActor scopes Claimed exactly the way the status preamble's own
// claimed tally scopes its count (internal/job/status.go RenderStatus):
// empty counts every live claim under an issue root, a specific actor
// narrows to that actor's own claims. Pass the raw --as value, not a
// softly-resolved default identity — the preamble tally doesn't either.
//
// nextActor resolves Next the same way `job next --issues` resolves its
// answer: pass the softly-resolved identity (job.ResolveIdentity).
func BuildIssuesStatus(db *sql.DB, claimedActor, nextActor string) (*IssuesStatus, error) {
	roots, err := getRootTasks(db)
	if err != nil {
		return nil, err
	}
	var issueRootIDs []int64
	for _, r := range roots {
		if r.Kind.IsIssue() {
			issueRootIDs = append(issueRootIDs, r.ID)
		}
	}
	if len(issueRootIDs) == 0 {
		return nil, nil
	}

	open, claimed, err := issueDescendantCounts(db, issueRootIDs, claimedActor)
	if err != nil {
		return nil, err
	}

	next, err := RunNextFiltered(db, "", nextActor, "", false, true)
	if err != nil {
		if !errors.Is(err, ErrNoAvailableTasks) {
			return nil, err
		}
		next = nil
	}

	return &IssuesStatus{Open: open, Claimed: claimed, Next: next}, nil
}

// issueDescendantCounts returns (open, claimed) across every descendant of
// rootIDs (the roots themselves excluded — an issue root is a container,
// not a task to complete). open counts every status other than done/
// canceled; claimed counts the subset with status='claimed', narrowed to
// claimedActor when non-empty.
func issueDescendantCounts(db *sql.DB, rootIDs []int64, claimedActor string) (open, claimed int, err error) {
	if len(rootIDs) == 0 {
		return 0, 0, nil
	}
	placeholders := strings.Repeat("?,", len(rootIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(rootIDs))
	for _, id := range rootIDs {
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		WITH RECURSIVE subtree(id) AS (
			SELECT id FROM tasks WHERE id IN (%s) AND deleted_at IS NULL
			UNION ALL
			SELECT t.id FROM tasks t JOIN subtree s ON t.parent_id = s.id
			WHERE t.deleted_at IS NULL
		)
		SELECT t.status, t.claimed_by, COUNT(*)
		FROM tasks t
		JOIN subtree s ON s.id = t.id
		WHERE t.parent_id IS NOT NULL
		  AND t.deleted_at IS NULL
		  AND t.status NOT IN ('done', 'canceled')
		GROUP BY t.status, t.claimed_by
	`, placeholders)

	rows, err := db.Query(query, args...)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var claimedBy sql.NullString
		var n int
		if err := rows.Scan(&status, &claimedBy, &n); err != nil {
			return 0, 0, err
		}
		open += n
		if status == "claimed" && (claimedActor == "" || (claimedBy.Valid && claimedBy.String == claimedActor)) {
			claimed += n
		}
	}
	return open, claimed, rows.Err()
}
