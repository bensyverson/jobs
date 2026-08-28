package job

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Filing an issue should not require knowing where the bug pile lives. The
// resolver below answers "which issue-tree root is this caller's?" the way
// `job issue` needs it (project/2026-08-28-issues-ux.md, decisions 1–3):
// the caller's focused issue root first — claiming inside an issue tree sets
// it — then the sole issue root if the database has exactly one, and
// otherwise an error that names the way out rather than guessing.

// IssueRoots returns every open issue-tree root in forest order. Closed
// roots are excluded: they take no new work, which is also how GetFocusKind
// already reads a done or canceled root.
func IssueRoots(db *sql.DB) ([]*Task, error) {
	roots, err := getRootTasks(db)
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, r := range roots {
		if !r.Kind.IsIssue() || r.Status == "done" || r.Status == "canceled" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ResolveIssueRoot returns the issue-tree root a new issue from actor
// belongs under. The error is caller-facing text: with several candidates it
// names each one and the command that picks between them, and with none it
// names the command that creates one.
func ResolveIssueRoot(db *sql.DB, actor string) (*Task, error) {
	if actor != "" {
		// A root that has since converted to a task-tree is no longer a
		// candidate, even though the focus_set event still says "issue".
		focused, err := GetFocusKind(db, actor, KindIssue)
		if err != nil {
			return nil, err
		}
		if focused != nil && focused.Kind.IsIssue() {
			return focused, nil
		}
	}

	roots, err := IssueRoots(db)
	if err != nil {
		return nil, err
	}
	switch len(roots) {
	case 0:
		return nil, errors.New(
			"no issue-tree root in this database. Create one with `job add <title> --kind issue`, then file against it.")
	case 1:
		return roots[0], nil
	}

	var b strings.Builder
	b.WriteString("several issue-tree roots; say which one with `job focus <id>`:")
	for _, r := range roots {
		fmt.Fprintf(&b, "\n  %s  %s", r.ShortID, r.Title)
	}
	return nil, errors.New(b.String())
}

// LiveClaims returns the tasks actor currently holds, soonest expiry first.
// Stale claims are swept before the read, exactly as `status` sweeps them,
// so an expired lock never stands in for live work.
func LiveClaims(db *sql.DB, actor string) ([]*Task, error) {
	if actor == "" {
		return nil, nil
	}
	if err := expireStaleClaims(db, actor); err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT `+taskSelectColumns+`
		FROM tasks
		WHERE claimed_by = ? AND status = 'claimed' AND deleted_at IS NULL
		ORDER BY claim_expires_at, short_id
	`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
