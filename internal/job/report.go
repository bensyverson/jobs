package job

import (
	"database/sql"
	"fmt"
	"time"
)

// BuildReport computes the report q selects by replaying the event log. It
// reads the cache and writes nothing.
//
// The counting rules are decisions 1–5 of project/2026-09-26-reporting.md:
// the unit is a leaf as of each instant, every sample is the store's state as
// of its End, canceled leaves leave scope, and plans are non-issue roots.
// Tasks purged since are excluded from every instant.
func BuildReport(db *sql.DB, q ReportQuery) (Report, error) {
	loc := q.Location
	if loc == nil {
		loc = time.Local
	}
	until := q.Until
	if until.IsZero() {
		until = CurrentNowFunc()
	}
	until = until.In(loc)

	tasks, err := loadReportTasks(db)
	if err != nil {
		return Report{}, err
	}
	existing := make(map[string]bool, len(tasks))
	for id := range tasks {
		existing[id] = true
	}
	if q.Scope != "" && !existing[q.Scope] {
		return Report{}, fmt.Errorf("task %q not found", q.Scope)
	}
	events, err := loadReportEvents(db, until.UnixMilli())
	if err != nil {
		return Report{}, err
	}

	universe, err := reportUniverseFor(q.Scope, existing, events)
	if err != nil {
		return Report{}, err
	}
	since := q.Since
	if since.IsZero() {
		since = firstEventIn(universe, events, until)
	}
	since = since.In(loc)
	bucket := q.Bucket
	if bucket == "" {
		bucket = autoBucket(!q.Since.IsZero(), until.Sub(since))
	}
	w, err := resolveBuckets(since, until, bucket)
	if err != nil {
		return Report{}, err
	}

	fold := newReportFold(existing)
	series, activity, err := replaySeries(fold, universe, w, events)
	if err != nil {
		return Report{}, err
	}
	r := Report{
		Schema: ReportSchema,
		Window: ReportWindow{
			Scope: q.Scope, Since: w.since, Until: w.until,
			Bucket: w.bucket, Timezone: locationName(loc),
		},
		Series:   series,
		Activity: activity,
	}
	fillFigures(&r, fold, universe, w, events, tasks)
	return r, nil
}

// reportUniverseFor decides what a scoped report counts: the scope task's
// subtree as of Until.
//
// Membership is fixed at Until rather than re-evaluated at each instant. A
// task moved into a plan is that plan's work over its whole life, so its
// history belongs to the plan's burn-up; judged per instant, a reparent would
// read as scope appearing from nowhere on one side and vanishing on the other,
// though no work was added or dropped. It also keeps the series, the activity
// histogram and the headline figures over one set of tasks.
func reportUniverseFor(scope string, existing map[string]bool, events []reportEvent) (reportUniverse, error) {
	if scope == "" {
		return forestUniverse(), nil
	}
	fold := newReportFold(existing)
	for _, e := range events {
		if err := fold.apply(e); err != nil {
			return reportUniverse{}, err
		}
	}
	members := map[string]bool{}
	for _, t := range fold.order {
		for id, hops := t.id, 0; id != "" && hops <= len(fold.order); hops++ {
			if id == scope {
				members[t.id] = true
				break
			}
			p := fold.tasks[id]
			if p == nil {
				break
			}
			id = p.parent
		}
	}
	return reportUniverse{scope: scope, members: members}, nil
}

// firstEventIn is the moment of the first event on a task in the universe, or
// until when there is none. Across the forest an adoption snapshot counts: it
// may be the first record of the tasks it carries.
func firstEventIn(u reportUniverse, events []reportEvent, until time.Time) time.Time {
	for _, e := range events {
		if (e.typ == EventSnapshot && u.members == nil) || (e.task != "" && u.has(e.task)) {
			return time.UnixMilli(e.ts)
		}
	}
	return until
}

// replaySeries folds events in order, sampling the state as of each bucket's
// End (events at End included) and counting the histogram's event kinds per
// bucket [Start, End), the last bucket closed at Until.
func replaySeries(fold *reportFold, u reportUniverse, w reportWindow, events []reportEvent) ([]Sample, []ActivityCount, error) {
	n := len(w.ends)
	series := make([]Sample, 0, n)
	activity := make([]ActivityCount, n)
	for i := range activity {
		activity[i].Start, activity[i].End = w.starts[i], w.ends[i]
	}

	// A sample only changes when an event was folded since the last one, and
	// nothing in the state depends on the clock, so a quiet bucket copies its
	// predecessor rather than walking every task again.
	dirty := true
	var last Sample
	emit := func() {
		if dirty {
			last = fold.sample(u)
			dirty = false
		}
		s := last
		s.End = w.ends[len(series)]
		series = append(series, s)
	}

	bucket := 0
	for _, e := range events {
		for len(series) < n && e.ts > w.endsMS[len(series)] {
			emit()
		}
		if err := fold.apply(e); err != nil {
			return nil, nil, err
		}
		dirty = true
		if e.task == "" || !u.has(e.task) || !w.inWindow(e.ts) {
			continue
		}
		for bucket < n-1 && e.ts >= w.endsMS[bucket] {
			bucket++
		}
		countActivity(&activity[bucket], e.typ)
	}
	for len(series) < n {
		emit()
	}
	return series, activity, nil
}

func countActivity(a *ActivityCount, typ EventType) {
	switch typ {
	case EventCreated:
		a.Created++
	case EventClaimed:
		a.Claimed++
	case EventDone:
		a.Done++
	case EventBlocked:
		a.Blocked++
	}
}
