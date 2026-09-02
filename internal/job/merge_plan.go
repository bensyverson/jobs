package job

import (
	"database/sql"
	"sort"
)

// Deciding what the merge would write. Nothing here touches a database, so
// --dry-run is this same code path with the apply step left off.

// ---------------------------------------------------------------------------
// plan
// ---------------------------------------------------------------------------

type mergeLabelWrite struct {
	taskShortID, name string
	createdAt         int64
}

type mergeBlockWrite struct {
	key       mergeBlockKey
	createdAt int64
}

type mergeFoundInWrite struct {
	taskShortID string
	mergeFoundIn
}

type mergeUserWrite struct {
	name      string
	createdAt int64
}

type mergePlan struct {
	insertTasks    []*mergeTaskRow
	updateTasks    []*mergeTaskRow
	labels         []mergeLabelWrite
	blocks         []mergeBlockWrite
	insertCriteria []*mergeCriterionRow
	updateCriteria []*mergeCriterionRow
	foundIn        []mergeFoundInWrite
	events         []mergeEventRow
	users          []mergeUserWrite
}

func (p *mergePlan) empty() bool {
	return len(p.insertTasks) == 0 && len(p.updateTasks) == 0 && len(p.labels) == 0 &&
		len(p.blocks) == 0 && len(p.insertCriteria) == 0 && len(p.updateCriteria) == 0 &&
		len(p.foundIn) == 0 && len(p.events) == 0 && len(p.users) == 0
}

// planMerge computes every write the merge would make and, alongside it, the
// report that explains them. Nothing here touches the database, so --dry-run
// is the same code path minus the apply step.
func planMerge(local, other *mergeSnapshot, now int64, report *MergeReport) *mergePlan {
	plan := &mergePlan{}

	// Tasks present on one side only.
	var onlyLocal, onlyOther []string
	for id := range local.tasks {
		if _, ok := other.tasks[id]; !ok {
			onlyLocal = append(onlyLocal, id)
		}
	}
	for id := range other.tasks {
		if _, ok := local.tasks[id]; !ok {
			onlyOther = append(onlyOther, id)
		}
	}
	sort.Strings(onlyLocal)
	sort.Strings(onlyOther)

	for _, id := range onlyLocal {
		report.OnlyInLocal = append(report.OnlyInLocal, taskSummary(local, id))
	}
	for _, id := range onlyOther {
		plan.insertTasks = append(plan.insertTasks, other.tasks[id])
		report.OnlyInOther = append(report.OnlyInOther, taskSummary(other, id))
		report.markArriving(id)
	}

	// Tasks on both sides.
	var shared []string
	for id := range local.tasks {
		if _, ok := other.tasks[id]; ok {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)

	for _, id := range shared {
		l, o := local.tasks[id], other.tasks[id]
		if *l == *o {
			// The same row on both sides has nothing to reconcile. Running
			// the claim rules over it would still normalise an expired
			// lease away and count the task as merged.
			report.stageMerged(id, MergedTask{ShortID: id, Title: l.title,
				RowWinner: MergeSideLocal, LocalUpdatedAt: l.updatedAt,
				OtherUpdatedAt: o.updatedAt, rowsIdentical: true})
			continue
		}
		merged, winner, drops := mergeTaskRows(l, o, now)
		entry := MergedTask{
			ShortID:        id,
			Title:          merged.title,
			RowWinner:      winner,
			LocalUpdatedAt: l.updatedAt,
			OtherUpdatedAt: o.updatedAt,
			rowsIdentical:  *l == *o,
		}
		if merged != *l {
			row := merged
			plan.updateTasks = append(plan.updateTasks, &row)
			for _, d := range drops {
				d.ShortID = id
				report.DroppedClaims = append(report.DroppedClaims, d)
			}
		} else {
			entry.RowWinner = MergeSideLocal
		}
		report.stageMerged(id, entry)
	}

	planLabels(local, other, plan, report)
	planBlocks(local, other, plan, report)
	planCriteria(local, other, plan, report)
	planFoundIn(local, other, plan, report)
	planUsers(local, other, plan)
	planEvents(local, other, plan, report)

	report.finish(plan)
	return plan
}

// mergeTaskRows folds one task's two rows into the row the local database
// should hold. The later `updated_at` wins the row wholesale; the claim rules
// then override the claim columns, because a close is a decision about the
// work and a claim is only a lease on it.
func mergeTaskRows(local, other *mergeTaskRow, now int64) (mergeTaskRow, MergeSide, []DroppedClaim) {
	winner := MergeSideLocal
	merged := *local
	if other.updatedAt > local.updatedAt {
		winner = MergeSideOther
		merged = *other
	}

	localClosed, otherClosed := isClosedStatus(local.status), isClosedStatus(other.status)
	if localClosed || otherClosed {
		if !isClosedStatus(merged.status) {
			closer := local
			if otherClosed {
				closer = other
			}
			merged.status = closer.status
			merged.completionNote = closer.completionNote
		}
		merged.claimedBy = sql.NullString{}
		merged.claimExpiresAt = sql.NullInt64{}
	} else if !merged.hasLiveClaim(now) {
		// A live claim on either side survives when neither side closed.
		claimant := (*mergeTaskRow)(nil)
		switch {
		case local.hasLiveClaim(now):
			claimant = local
		case other.hasLiveClaim(now):
			claimant = other
		}
		if claimant != nil {
			merged.claimedBy = claimant.claimedBy
			merged.claimExpiresAt = claimant.claimExpiresAt
			merged.status = claimant.status
		}
	}

	var dropped []DroppedClaim
	for side, row := range map[MergeSide]*mergeTaskRow{MergeSideLocal: local, MergeSideOther: other} {
		if !row.hasLiveClaim(now) {
			continue
		}
		if merged.claimedBy.Valid && merged.claimedBy.String == row.claimedBy.String {
			continue
		}
		reason := "the other side closed the task as " + merged.status
		if !isClosedStatus(merged.status) {
			reason = "a later claim on the other side won"
		}
		dropped = append(dropped, DroppedClaim{Actor: row.claimedBy.String, Side: side, Reason: reason})
	}
	sort.Slice(dropped, func(i, j int) bool { return dropped[i].Actor < dropped[j].Actor })

	return merged, winner, dropped
}

func planLabels(local, other *mergeSnapshot, plan *mergePlan, report *MergeReport) {
	tasks := make([]string, 0, len(other.labels))
	for task := range other.labels {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	for _, task := range tasks {
		names := make([]string, 0, len(other.labels[task]))
		for name := range other.labels[task] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, ok := local.labels[task][name]; ok {
				continue
			}
			plan.labels = append(plan.labels, mergeLabelWrite{task, name, other.labels[task][name]})
			report.noteLabel(task, name, other)
		}
	}
}

func planBlocks(local, other *mergeSnapshot, plan *mergePlan, report *MergeReport) {
	keys := make([]mergeBlockKey, 0, len(other.blocks))
	for k := range other.blocks {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].blocked != keys[j].blocked {
			return keys[i].blocked < keys[j].blocked
		}
		return keys[i].blocker < keys[j].blocker
	})
	for _, k := range keys {
		if _, ok := local.blocks[k]; ok {
			continue
		}
		plan.blocks = append(plan.blocks, mergeBlockWrite{k, other.blocks[k]})
		report.noteBlock(k, other)
	}
}

func planCriteria(local, other *mergeSnapshot, plan *mergePlan, report *MergeReport) {
	keys := make([]string, 0, len(other.criteria))
	for k := range other.criteria {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		oc := other.criteria[k]
		lc, ok := local.criteria[k]
		if !ok {
			plan.insertCriteria = append(plan.insertCriteria, oc)
			report.noteCriterion(oc, other, false)
			continue
		}
		if oc.updatedAt > lc.updatedAt && !oc.sameAs(*lc) {
			plan.updateCriteria = append(plan.updateCriteria, oc)
			report.noteCriterion(oc, other, true)
		}
	}
}

func planFoundIn(local, other *mergeSnapshot, plan *mergePlan, report *MergeReport) {
	tasks := make([]string, 0, len(other.foundIn))
	for task := range other.foundIn {
		tasks = append(tasks, task)
	}
	sort.Strings(tasks)
	for _, task := range tasks {
		of := other.foundIn[task]
		lf, ok := local.foundIn[task]
		if ok && (lf == of || lf.createdAt >= of.createdAt) {
			continue
		}
		plan.foundIn = append(plan.foundIn, mergeFoundInWrite{task, of})
		report.noteFoundIn(task, other)
	}
}

func planUsers(local, other *mergeSnapshot, plan *mergePlan) {
	names := make([]string, 0, len(other.users))
	for name := range other.users {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := local.users[name]; ok {
			continue
		}
		plan.users = append(plan.users, mergeUserWrite{name, other.users[name]})
	}
}

// planEvents unions the two event streams as multisets: a tuple the other
// side holds three times and this side twice contributes one more row. That
// keeps a re-merge a no-op, which a set union of distinct tuples would not.
func planEvents(local, other *mergeSnapshot, plan *mergePlan, report *MergeReport) {
	have := map[string]int{}
	for _, e := range local.events {
		have[e.key()]++
	}
	for _, e := range other.events {
		// A snapshot is one replica's compaction of state it already holds
		// and a replica event names one checkout. Neither is shared history,
		// and transcribed here adoption would carry them into this replica's
		// log as history that never happened on this side.
		switch EventType(e.eventType) {
		case EventSnapshot, EventReplica:
			continue
		}
		k := e.key()
		if have[k] > 0 {
			have[k]--
			continue
		}
		plan.events = append(plan.events, e)
		report.noteEvent(e)
	}
}
