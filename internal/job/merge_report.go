package job

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The merge report. It exists so a merge is reviewable before it is trusted:
// --dry-run prints exactly this and writes nothing, and the same structure
// serialises to JSON for anything that would rather not parse prose.

// MergeSide names which of the two databases a value came from.
type MergeSide string

const (
	MergeSideLocal MergeSide = "local"
	MergeSideOther MergeSide = "other"
)

// MergeReport is the whole account of one merge: what arrived, what stayed,
// what was reconciled and what was discarded.
type MergeReport struct {
	OtherPath       string           `json:"other_path"`
	DryRun          bool             `json:"dry_run"`
	Changed         bool             `json:"changed"`
	SharedEvents    int              `json:"shared_events"`
	LocalTailEvents int              `json:"local_tail_events"`
	OtherTailEvents int              `json:"other_tail_events"`
	OnlyInLocal     []MergeTaskBrief `json:"only_in_local"`
	OnlyInOther     []MergeTaskBrief `json:"only_in_other"`
	Merged          []MergedTask     `json:"merged"`
	DroppedClaims   []DroppedClaim   `json:"dropped_claims"`
	Totals          MergeTotals      `json:"totals"`
	staged          map[string]*MergedTask
	arriving        map[string]bool
}

// MergeTaskBrief describes a task that existed on one side only.
type MergeTaskBrief struct {
	ShortID  string   `json:"short_id"`
	Title    string   `json:"title"`
	Labels   []string `json:"labels,omitempty"`
	Criteria int      `json:"criteria"`
	Events   int      `json:"events"`
}

// MergedTask records a task both sides held, and which side won what.
type MergedTask struct {
	ShortID         string    `json:"short_id"`
	Title           string    `json:"title"`
	RowWinner       MergeSide `json:"row_winner"`
	LocalUpdatedAt  int64     `json:"local_updated_at"`
	OtherUpdatedAt  int64     `json:"other_updated_at"`
	LabelsAdded     []string  `json:"labels_added,omitempty"`
	BlocksAdded     []string  `json:"blocks_added,omitempty"`
	CriteriaAdded   []string  `json:"criteria_added,omitempty"`
	CriteriaUpdated []string  `json:"criteria_updated,omitempty"`
	FoundInFrom     string    `json:"found_in_from,omitempty"`
	EventsAdded     int       `json:"events_added"`
	rowsIdentical   bool
}

func (m MergedTask) touched() bool {
	return !m.rowsIdentical || len(m.LabelsAdded) > 0 || len(m.BlocksAdded) > 0 ||
		len(m.CriteriaAdded) > 0 || len(m.CriteriaUpdated) > 0 ||
		m.FoundInFrom != "" || m.EventsAdded > 0
}

// DroppedClaim is a live claim the merge did not carry into the result.
type DroppedClaim struct {
	ShortID string    `json:"short_id"`
	Actor   string    `json:"actor"`
	Side    MergeSide `json:"side"`
	Reason  string    `json:"reason"`
}

// MergeTotals counts the writes, so the closing line can say what happened
// without the reader adding up the sections.
type MergeTotals struct {
	TasksAdded      int `json:"tasks_added"`
	TasksMerged     int `json:"tasks_merged"`
	LabelsAdded     int `json:"labels_added"`
	BlocksAdded     int `json:"blocks_added"`
	CriteriaAdded   int `json:"criteria_added"`
	CriteriaUpdated int `json:"criteria_updated"`
	FoundInSet      int `json:"found_in_set"`
	EventsAdded     int `json:"events_added"`
	UsersAdded      int `json:"users_added"`
}

// ---------------------------------------------------------------------------
// accumulation, called from the planner
// ---------------------------------------------------------------------------

func (r *MergeReport) markArriving(shortID string) {
	if r.arriving == nil {
		r.arriving = map[string]bool{}
	}
	r.arriving[shortID] = true
}

func (r *MergeReport) stageMerged(shortID string, entry MergedTask) {
	if r.staged == nil {
		r.staged = map[string]*MergedTask{}
	}
	e := entry
	r.staged[shortID] = &e
}

// entryFor returns the staged record for a task both sides held. A task that
// is arriving whole from the other side reports its contents in its brief,
// not as a per-table reconciliation, so it deliberately returns nil there.
func (r *MergeReport) entryFor(shortID string) *MergedTask {
	if r.arriving[shortID] {
		return nil
	}
	return r.staged[shortID]
}

func (r *MergeReport) noteLabel(taskShortID, name string, _ *mergeSnapshot) {
	r.Totals.LabelsAdded++
	if e := r.entryFor(taskShortID); e != nil {
		e.LabelsAdded = append(e.LabelsAdded, name)
	}
}

func (r *MergeReport) noteBlock(key mergeBlockKey, _ *mergeSnapshot) {
	r.Totals.BlocksAdded++
	if e := r.entryFor(key.blocked); e != nil {
		e.BlocksAdded = append(e.BlocksAdded, key.blocker)
	}
}

func (r *MergeReport) noteCriterion(c *mergeCriterionRow, _ *mergeSnapshot, updated bool) {
	label := c.label
	if c.shortID.Valid && c.shortID.String != "" {
		label = c.shortID.String + " " + c.label
	}
	e := r.entryFor(c.taskShortID)
	if updated {
		r.Totals.CriteriaUpdated++
		if e != nil {
			e.CriteriaUpdated = append(e.CriteriaUpdated, label)
		}
		return
	}
	r.Totals.CriteriaAdded++
	if e != nil {
		e.CriteriaAdded = append(e.CriteriaAdded, label)
	}
}

func (r *MergeReport) noteFoundIn(taskShortID string, other *mergeSnapshot) {
	r.Totals.FoundInSet++
	if e := r.entryFor(taskShortID); e != nil {
		e.FoundInFrom = other.foundIn[taskShortID].sourceShortID
	}
}

func (r *MergeReport) noteEvent(e mergeEventRow) {
	r.Totals.EventsAdded++
	if entry := r.entryFor(e.taskShortID); entry != nil {
		entry.EventsAdded++
	}
}

// finish folds the staged per-task records into the report and counts the
// plan. Only tasks the two sides actually disagreed about are listed.
func (r *MergeReport) finish(plan *mergePlan) {
	for _, e := range r.staged {
		if e.touched() {
			r.Merged = append(r.Merged, *e)
		}
	}
	sort.Slice(r.Merged, func(i, j int) bool { return r.Merged[i].ShortID < r.Merged[j].ShortID })
	sort.Slice(r.DroppedClaims, func(i, j int) bool {
		if r.DroppedClaims[i].ShortID != r.DroppedClaims[j].ShortID {
			return r.DroppedClaims[i].ShortID < r.DroppedClaims[j].ShortID
		}
		return r.DroppedClaims[i].Actor < r.DroppedClaims[j].Actor
	})

	r.Totals.TasksAdded = len(plan.insertTasks)
	r.Totals.TasksMerged = len(plan.updateTasks)
	r.Totals.UsersAdded = len(plan.users)
	r.Changed = !plan.empty()
}

// ---------------------------------------------------------------------------
// rendering
// ---------------------------------------------------------------------------

func (r *MergeReport) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func (r *MergeReport) Markdown() string {
	var b strings.Builder
	b.WriteString("# Merge report\n\n")
	fmt.Fprintf(&b, "Merging `%s` into this database.\n", r.OtherPath)
	fmt.Fprintf(&b, "Shared history: %d events. This side added %d since the copy; the other added %d.\n",
		r.SharedEvents, r.LocalTailEvents, r.OtherTailEvents)

	if len(r.OnlyInOther) > 0 {
		fmt.Fprintf(&b, "\n## Arriving from the other database (%d)\n\n", len(r.OnlyInOther))
		for _, t := range r.OnlyInOther {
			fmt.Fprintf(&b, "- `%s` %s — %s\n", t.ShortID, t.Title, briefContents(t))
		}
	}
	if len(r.OnlyInLocal) > 0 {
		fmt.Fprintf(&b, "\n## Only in this database (%d)\n\n", len(r.OnlyInLocal))
		for _, t := range r.OnlyInLocal {
			fmt.Fprintf(&b, "- `%s` %s\n", t.ShortID, t.Title)
		}
	}
	if len(r.Merged) > 0 {
		fmt.Fprintf(&b, "\n## Touched on both sides (%d)\n\n", len(r.Merged))
		for _, m := range r.Merged {
			fmt.Fprintf(&b, "- `%s` %s\n", m.ShortID, m.Title)
			fmt.Fprintf(&b, "  - task row: %s wins (this side %s, other side %s)\n",
				m.RowWinner, mergeStamp(m.LocalUpdatedAt), mergeStamp(m.OtherUpdatedAt))
			if len(m.LabelsAdded) > 0 {
				fmt.Fprintf(&b, "  - labels added: %s\n", strings.Join(m.LabelsAdded, ", "))
			}
			if len(m.BlocksAdded) > 0 {
				fmt.Fprintf(&b, "  - blockers added: %s\n", strings.Join(m.BlocksAdded, ", "))
			}
			if len(m.CriteriaAdded) > 0 {
				fmt.Fprintf(&b, "  - criteria added: %s\n", strings.Join(m.CriteriaAdded, ", "))
			}
			if len(m.CriteriaUpdated) > 0 {
				fmt.Fprintf(&b, "  - criteria taken from the other side: %s\n", strings.Join(m.CriteriaUpdated, ", "))
			}
			if m.FoundInFrom != "" {
				fmt.Fprintf(&b, "  - found in: %s (from the other side)\n", m.FoundInFrom)
			}
			if m.EventsAdded > 0 {
				fmt.Fprintf(&b, "  - events added: %d\n", m.EventsAdded)
			}
		}
	}
	if len(r.DroppedClaims) > 0 {
		fmt.Fprintf(&b, "\n## Claims dropped (%d)\n\n", len(r.DroppedClaims))
		for _, c := range r.DroppedClaims {
			fmt.Fprintf(&b, "- `%s` %s's claim on the %s side — %s\n", c.ShortID, c.Actor, c.Side, c.Reason)
		}
	}

	b.WriteString("\n")
	switch {
	case !r.Changed:
		b.WriteString("Nothing changed: this database already holds everything the other one does.\n")
	case r.DryRun:
		fmt.Fprintf(&b, "This was a dry run — nothing was written. %s\n", mergeTotalsLine(r.Totals))
	default:
		fmt.Fprintf(&b, "Merged into this database. %s\n", mergeTotalsLine(r.Totals))
	}
	if r.DryRun && !r.Changed {
		b.WriteString("(dry run)\n")
	}
	return b.String()
}

func briefContents(t MergeTaskBrief) string {
	parts := []string{fmt.Sprintf("%s, %s", plural(t.Events, "event"), plural(t.Criteria, "criterion"))}
	if len(t.Labels) > 0 {
		parts = append(parts, "labels: "+strings.Join(t.Labels, ", "))
	}
	return strings.Join(parts, "; ")
}

func mergeTotalsLine(t MergeTotals) string {
	parts := []string{
		plural(t.TasksAdded, "task") + " added",
		plural(t.TasksMerged, "task") + " reconciled",
		plural(t.EventsAdded, "event") + " copied",
	}
	if t.LabelsAdded > 0 {
		parts = append(parts, plural(t.LabelsAdded, "label")+" added")
	}
	if t.BlocksAdded > 0 {
		parts = append(parts, plural(t.BlocksAdded, "blocker")+" added")
	}
	if t.CriteriaAdded+t.CriteriaUpdated > 0 {
		parts = append(parts, fmt.Sprintf("%s added and %d updated",
			plural(t.CriteriaAdded, "criterion"), t.CriteriaUpdated))
	}
	return strings.Join(parts, ", ") + "."
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	if noun == "criterion" {
		return fmt.Sprintf("%d criteria", n)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func mergeStamp(unix int64) string {
	if unix == 0 {
		return "never"
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// taskSummary describes a one-sided task for the report, counting what will
// travel with it.
func taskSummary(s *mergeSnapshot, shortID string) MergeTaskBrief {
	brief := MergeTaskBrief{ShortID: shortID}
	if t, ok := s.tasks[shortID]; ok {
		brief.Title = t.title
	}
	for name := range s.labels[shortID] {
		brief.Labels = append(brief.Labels, name)
	}
	sort.Strings(brief.Labels)
	for _, c := range s.criteria {
		if c.taskShortID == shortID {
			brief.Criteria++
		}
	}
	for _, e := range s.events {
		if e.taskShortID == shortID {
			brief.Events++
		}
	}
	return brief
}
