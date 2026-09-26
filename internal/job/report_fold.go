package job

import (
	"encoding/json"
	"fmt"
)

// The report's fold: an in-memory projection of just what the counting rules
// read — parent, root kind, open/done/canceled, blocker edges — replayed from
// the cache's events so any instant's state can be sampled.
//
// It is a second fold rather than a reuse of apply because apply is a writer:
// it can only project into SQLite tables, so sampling a hundred instants would
// mean a scratch database and a query per sample. This fold mirrors apply's
// semantics for the fields it keeps (apply_tasks.go, apply_claims.go,
// apply_relations.go, apply_snapshot.go) and nothing else; a change to what one
// of those event types means must change both.
//
// Unlike apply it also folds history-only lines (adopted legacy events, which
// the cache cannot tell apart from the rest). Their payloads carry the same
// short ids, and the adoption snapshot that follows them overwrites whatever
// they got wrong.

// reportEvent is one cache event row, reduced to what the fold and the figures
// read. data is loaded only for the types whose payload matters.
type reportEvent struct {
	ts    int64 // milliseconds
	actor string
	typ   EventType
	task  string
	data  []byte
}

// taskState is where a task stands for counting: the cache's four statuses
// collapse to three, because available and claimed are both open work.
type taskState int

const (
	stateOpen taskState = iota
	stateDone
	stateCanceled
)

func stateFromStatus(status string) taskState {
	switch status {
	case "done":
		return stateDone
	case "canceled":
		return stateCanceled
	default:
		return stateOpen
	}
}

// foldTask is one task as the fold holds it. The timestamps (milliseconds)
// feed the headline figures: each close records the last claim before it.
type foldTask struct {
	id       string
	parent   string
	kind     TreeKind
	state    taskState
	children int

	created         int64
	lastClaim       int64
	lastDone        int64
	lastDoneActor   string
	claimBeforeDone int64
	lastCancel      int64
	imported        int64
	importSource    string
}

// isIssueRoot reports the one kind of childless task that is never a leaf.
func (t *foldTask) isIssueRoot() bool { return t.parent == "" && t.kind == KindIssue }

// reportFold is the replayed state. order keeps tasks in first-seen order so a
// sample walks a slice, not a map.
type reportFold struct {
	existing map[string]bool
	tasks    map[string]*foldTask
	order    []*foldTask
	// blockers maps a blocked task to its blockers' ids.
	blockers map[string]map[string]bool
}

// newReportFold starts an empty fold. existing is every task the store holds
// now; events on any other task — purged since — are ignored, so a purged task
// counts at no instant and never makes its parent a parent.
func newReportFold(existing map[string]bool) *reportFold {
	return &reportFold{
		existing: existing,
		tasks:    map[string]*foldTask{},
		blockers: map[string]map[string]bool{},
	}
}

func (e reportEvent) decode(into any) error {
	if len(e.data) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.data, into); err != nil {
		return fmt.Errorf("report: decode %s payload on %q: %w", e.typ, e.task, err)
	}
	return nil
}

// apply folds one event. Types that change nothing the report counts are
// ignored.
func (f *reportFold) apply(e reportEvent) error {
	if e.typ == EventSnapshot {
		return f.applySnapshot(e)
	}
	if e.typ == EventCreated {
		return f.applyCreated(e)
	}
	t := f.tasks[e.task]
	if t == nil {
		return nil
	}
	switch e.typ {
	case EventDone:
		t.state = stateDone
		t.lastDone, t.lastDoneActor, t.claimBeforeDone = e.ts, e.actor, t.lastClaim
	case EventCanceled:
		t.state = stateCanceled
		t.lastCancel = e.ts
	case EventReopened, EventReleased, EventClaimExpired:
		// apply writes 'available' unconditionally for all three.
		t.state = stateOpen
	case EventClaimed:
		t.state = stateOpen
		t.lastClaim = e.ts
	case EventReparented:
		var p ReparentedPayload
		if err := e.decode(&p); err != nil {
			return err
		}
		f.setParent(t, p.NewParentID)
	case EventKindChanged:
		var p KindChangedPayload
		if err := e.decode(&p); err != nil {
			return err
		}
		t.kind = TreeKind(p.To)
	case EventBlocked, EventUnblocked:
		var p BlockedPayload
		if err := e.decode(&p); err != nil {
			return err
		}
		f.setBlock(p.BlockedID, p.BlockerID, e.typ == EventBlocked)
	case EventImported:
		var p ImportedPayload
		if err := e.decode(&p); err != nil {
			return err
		}
		t.imported, t.importSource = e.ts, p.Source
	}
	return nil
}

func (f *reportFold) applyCreated(e reportEvent) error {
	if !f.existing[e.task] || f.tasks[e.task] != nil {
		return nil
	}
	var p CreatedPayload
	if err := e.decode(&p); err != nil {
		return err
	}
	kind := TreeKind(p.Kind)
	if kind == "" {
		kind = KindTask
	}
	t := &foldTask{id: e.task, kind: kind, created: e.ts}
	f.tasks[e.task] = t
	f.order = append(f.order, t)
	f.setParent(t, p.ParentID)
	return nil
}

// setParent moves t under parent, keeping child counts. A parent the fold does
// not hold is kept as a name only: t is still not a root.
func (f *reportFold) setParent(t *foldTask, parent string) {
	if p := f.tasks[t.parent]; p != nil {
		p.children--
	}
	t.parent = parent
	if p := f.tasks[parent]; p != nil {
		p.children++
	}
}

func (f *reportFold) setBlock(blocked, blocker string, on bool) {
	if !on {
		delete(f.blockers[blocked], blocker)
		return
	}
	if f.blockers[blocked] == nil {
		f.blockers[blocked] = map[string]bool{}
	}
	f.blockers[blocked][blocker] = true
}

// applySnapshot overwrites every task the payload carries — parent, kind,
// status and blockers — as apply_snapshot.go does. A task first seen here
// takes the snapshot's recorded creation time.
//
// Unlike apply it prunes nothing. The fold only ever holds tasks the store
// still holds, so a snapshot that omits one is not a removal: it is a replica
// adopting without having seen another replica's events, which sort before
// its snapshot. A later snapshot or event puts the task back in the cache, and
// pruning it here would draw a dip that never reflected work, then re-admit
// the task stripped of its history. (Measured on a client store: its first
// adoption snapshot omitted 66 tasks that the second, twenty minutes later,
// restored.)
func (f *reportFold) applySnapshot(e reportEvent) error {
	var p SnapshotPayload
	if err := e.decode(&p); err != nil {
		return err
	}
	for _, st := range p.Tasks {
		if !f.existing[st.ShortID] {
			continue
		}
		t := f.tasks[st.ShortID]
		if t == nil {
			t = &foldTask{id: st.ShortID, created: st.CreatedAt * 1000}
			f.tasks[t.id] = t
			f.order = append(f.order, t)
		}
		t.parent, t.kind, t.state = st.ParentID, TreeKind(st.Kind), stateFromStatus(st.Status)
		if t.kind == "" {
			t.kind = KindTask
		}
		delete(f.blockers, t.id)
	}
	for _, t := range f.order {
		t.children = 0
	}
	for _, t := range f.order {
		if parent := f.tasks[t.parent]; parent != nil {
			parent.children++
		}
	}
	for _, b := range p.Blocks {
		f.setBlock(b.BlockedID, b.BlockerID, true)
	}
	return nil
}

// isBlocked reports whether an open task has a blocker that is not done, as
// the live views read `blocks`. A blocker the fold does not hold is ignored.
func (f *reportFold) isBlocked(id string) bool {
	for b := range f.blockers[id] {
		if bt := f.tasks[b]; bt != nil && bt.state != stateDone {
			return true
		}
	}
	return false
}

// reportUniverse is the set of tasks a report counts. members nil means the
// whole forest; otherwise it is the scope task's subtree.
type reportUniverse struct {
	scope   string
	members map[string]bool
}

func forestUniverse() reportUniverse { return reportUniverse{} }

func (u reportUniverse) has(id string) bool { return u.members == nil || u.members[id] }

// isPlan: with a scope the scope task is the only plan; across the forest a
// plan is a root that is not an issue root.
func (u reportUniverse) isPlan(t *foldTask) bool {
	if u.scope != "" {
		return t.id == u.scope
	}
	return t.parent == "" && t.kind != KindIssue
}

// isLeaf is decision 1: no children at this instant, and not an issue root.
func isLeaf(t *foldTask) bool { return t.children == 0 && !t.isIssueRoot() }

// sample counts the fold's current state (decisions 1–3). End is the caller's.
func (f *reportFold) sample(u reportUniverse) Sample {
	var s Sample
	for _, t := range f.order {
		if !u.has(t.id) {
			continue
		}
		if u.isPlan(t) && t.state == stateDone {
			s.PlansDone++
		}
		if !isLeaf(t) {
			continue
		}
		switch t.state {
		case stateCanceled:
			s.Canceled++
		case stateDone:
			s.Scope++
			s.Done++
		default:
			s.Scope++
			s.Open++
			if f.isBlocked(t.id) {
				s.Blocked++
			}
		}
	}
	return s
}
