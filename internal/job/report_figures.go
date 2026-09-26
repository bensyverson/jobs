package job

import (
	"cmp"
	"slices"
	"time"
)

// fillFigures computes the headline figures from the fold as it stands at
// Until. A transition counts "in the window" by its final occurrence as of
// Until: a leaf closed, reopened and closed again is one close, at the last
// one, and a leaf closed in the window but reopened before Until was not
// closed in it. That is what keeps Leaves.Done, Pace and DoneByActor agreeing
// with the done line.
func fillFigures(r *Report, fold *reportFold, u reportUniverse, w reportWindow, events []reportEvent, tasks map[string]reportStoreTask) {
	final := r.Series[len(r.Series)-1]
	r.Leaves.Open, r.Leaves.Blocked = final.Open, final.Blocked

	var createdToDone, claimedToDone, importToClose []int64
	actors := map[string]int{}
	for _, t := range fold.order {
		if !u.has(t.id) {
			continue
		}
		closedInWindow := t.state == stateDone && t.lastDone > 0 && w.inWindow(t.lastDone)
		if u.isPlan(t) {
			switch {
			case closedInWindow:
				r.Plans.Closed++
			case t.state == stateOpen:
				r.Plans.Open++
			}
		}
		if t.imported > 0 && closedInWindow {
			importToClose = append(importToClose, t.lastDone-t.imported)
		}
		if !isLeaf(t) {
			continue
		}
		if w.inWindow(t.created) {
			r.Leaves.Created++
		}
		if t.state == stateCanceled && w.inWindow(t.lastCancel) {
			r.Leaves.Canceled++
		}
		if !closedInWindow {
			continue
		}
		r.Leaves.Done++
		actors[t.lastDoneActor]++
		createdToDone = append(createdToDone, t.lastDone-t.created)
		if t.claimBeforeDone > 0 {
			claimedToDone = append(claimedToDone, t.lastDone-t.claimBeforeDone)
		}
	}

	if weeks := w.weeks(); weeks > 0 {
		r.Pace.DonePerWeek = float64(r.Leaves.Done) / weeks
	}
	r.Pace.MedianCreatedToDoneSeconds = medianSeconds(createdToDone)
	r.Pace.MedianClaimedToDoneSeconds = medianSeconds(claimedToDone)
	r.Plans.MedianImportToCloseSeconds = medianSeconds(importToClose)
	r.DoneByActor = rankActors(actors)
	r.Imports = importMarkers(u, w, events, tasks)
	r.Plans.Imported = len(r.Imports)
	r.Plans.FirstImportAt = firstImport(u, w, events)
}

// firstImport is the earliest imported event in the universe up to Until,
// inside the window or before it; nil when the store has none.
func firstImport(u reportUniverse, w reportWindow, events []reportEvent) *time.Time {
	for _, e := range events {
		if e.typ == EventImported && u.has(e.task) {
			at := time.UnixMilli(e.ts).In(w.until.Location())
			return &at
		}
	}
	return nil
}

// medianSeconds is the median of millisecond durations, rounded to whole
// seconds; the mean of the middle two for an even count, and nil for none.
// Rounding rather than truncating matters: the log's clock ticks a
// millisecond per event within one write, so a close an hour after its
// create is often 3,599,999ms after it.
func medianSeconds(ms []int64) *int64 {
	if len(ms) == 0 {
		return nil
	}
	slices.Sort(ms)
	mid := len(ms) / 2
	m := ms[mid]
	if len(ms)%2 == 0 {
		m = (ms[mid-1] + ms[mid]) / 2
	}
	s := (m + 500) / 1000
	return &s
}

// rankActors orders done counts most first, ties by name, so the output is
// stable.
func rankActors(counts map[string]int) []ActorCount {
	out := make([]ActorCount, 0, len(counts))
	for actor, n := range counts {
		out = append(out, ActorCount{Actor: actor, Done: n})
	}
	slices.SortFunc(out, func(a, b ActorCount) int {
		if c := cmp.Compare(b.Done, a.Done); c != 0 {
			return c
		}
		return cmp.Compare(a.Actor, b.Actor)
	})
	return out
}

// importMarkers is one marker per imported event in the window, oldest first.
// The title is the task's title now, which is what a reader recognizes it by.
func importMarkers(u reportUniverse, w reportWindow, events []reportEvent, tasks map[string]reportStoreTask) []ImportMarker {
	out := []ImportMarker{}
	for _, e := range events {
		if e.typ != EventImported || !u.has(e.task) || !w.inWindow(e.ts) {
			continue
		}
		var p ImportedPayload
		// A payload the fold already decoded cannot fail here; an undecodable
		// one would have stopped the replay.
		_ = e.decode(&p)
		out = append(out, ImportMarker{
			At:     time.UnixMilli(e.ts).In(w.until.Location()),
			TaskID: e.task,
			Title:  tasks[e.task].title,
			Source: p.Source,
		})
	}
	return out
}
