package handlers

import (
	"fmt"
	"net/http"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
)

// metaWindow is the period the Issues meta line reports closures over.
// Seven days is what the CLI's own summaries use; long enough that a
// quiet afternoon doesn't read as a dead repo.
const metaWindow = 7 * 24 * time.Hour

// planIssueView is /issues: the same page skeleton as /plan, driven by
// the issue-tree kind instead (project/2026-08-28-issues-ux.md,
// decision 7). A plan and a bug pile are different shapes; the tab is
// the shape a reader already knows how to read.
var planIssueView = planView{
	Kind:        job.KindIssue,
	ActiveTab:   "issues",
	BasePath:    "/issues",
	SectionName: "Issues",
	FilterName:  "Issues filter",
	EmptyText:   "No open issues.",
}

// Issues renders the Plan view over issue-tree roots. Scoped at
// /issues/{root-id}; ?label= and ?show= behave exactly as on /plan.
func Issues(deps Deps) http.Handler { return planHandler(deps, planIssueView) }

// viewMeta renders the view's period stat, or "" when the view has
// none. Only the Issues view carries one today: "N open · M closed in
// 7d", where open is every unclosed task under an issue root and
// closed counts the ones closed inside the window. Both are computed
// across every issue root, not just the scoped one — the line answers
// "how is the bug pile doing", which a scoped URL doesn't narrow.
func viewMeta(deps Deps, view planView, now time.Time) (string, error) {
	if !view.Kind.IsIssue() {
		return "", nil
	}
	counts, err := job.CountIssueTasks(deps.DB, now.Add(-metaWindow))
	if err != nil {
		return "", err
	}
	if !counts.HasRoots {
		return "", nil
	}
	return fmt.Sprintf("%d open · %d closed in 7d", counts.Open, counts.ClosedSince), nil
}
