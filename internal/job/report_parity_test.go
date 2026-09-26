package job

import "testing"

// TestReport_FinalSampleMatchesTheCache guards the report's second fold
// (report_fold.go) against drifting from apply: after a history that
// exercises every event type the fold reads, the last sample must equal the
// same counts taken straight from the cache apply built.
func TestReport_FinalSampleMatchesTheCache(t *testing.T) {
	f := newReportFixture(t)
	plan := f.add("", "Plan")
	a := f.add(plan, "a")
	b := f.add(plan, "b")
	c := f.add(plan, "c")
	d := f.add(plan, "d")
	issues := f.issueRoot("Issues")
	bug := f.add(issues, "bug")
	other := f.add("", "Other plan")
	moved := f.add(other, "moved")
	gone := f.add(plan, "gone")

	f.at(day(1)).claim(a)
	f.done(a)
	f.at(day(2)).reopen(a)
	f.block(b, a)
	f.block(c, d)
	f.at(day(3)).cancel(d)
	parts := f.split(c, "c1", "c2")
	f.done(parts[0])
	f.at(day(4)).reparent(moved, plan)
	f.purge(gone)
	f.done(bug)
	f.importPlan("plan.md", "", "tasks:\n  - title: Imported\n    children:\n      - title: i1\n      - title: i2\n")

	r := f.at(day(5)).report(ReportQuery{})
	got := r.Series[len(r.Series)-1]

	var want Sample
	row := f.db.QueryRow(`
		WITH leaves AS (
			SELECT t.id, t.status FROM tasks t
			WHERE t.deleted_at IS NULL
			  AND NOT (t.parent_id IS NULL AND t.kind = 'issue')
			  AND NOT EXISTS (SELECT 1 FROM tasks c WHERE c.parent_id = t.id AND c.deleted_at IS NULL)
		)
		SELECT
			(SELECT COUNT(*) FROM leaves WHERE status != 'canceled'),
			(SELECT COUNT(*) FROM leaves WHERE status = 'done'),
			(SELECT COUNT(*) FROM leaves WHERE status IN ('available', 'claimed')),
			(SELECT COUNT(*) FROM leaves l WHERE l.status IN ('available', 'claimed') AND EXISTS (
				SELECT 1 FROM blocks b JOIN tasks bt ON bt.id = b.blocker_id
				WHERE b.blocked_id = l.id AND bt.status != 'done' AND bt.deleted_at IS NULL)),
			(SELECT COUNT(*) FROM leaves WHERE status = 'canceled'),
			(SELECT COUNT(*) FROM tasks WHERE parent_id IS NULL AND kind != 'issue'
				AND status = 'done' AND deleted_at IS NULL)`)
	if err := row.Scan(&want.Scope, &want.Done, &want.Open, &want.Blocked, &want.Canceled, &want.PlansDone); err != nil {
		t.Fatalf("count the cache: %v", err)
	}
	want.End = got.End
	if got != want {
		t.Errorf("final sample drifted from the cache:\n got  %+v\n want %+v", got, want)
	}
	if want.Blocked == 0 || want.Canceled == 0 || want.Done == 0 {
		t.Errorf("the history no longer exercises blocked/canceled/done: %+v", want)
	}
}
