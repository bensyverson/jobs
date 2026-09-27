package job

import (
	"time"
)

// ReportSchema is the version of the Report wire shape. `job stats
// --format=json` carries it so an external reader (timetattle) can refuse
// a shape it does not know. Bump it when a field is renamed, removed or
// changes meaning; adding a field does not bump it.
const ReportSchema = 1

// ReportQuery selects what BuildReport covers. The counting rules are in
// project/2026-09-26-reporting.md, decisions 1–4.
type ReportQuery struct {
	// Scope is a task's short id; the report covers that subtree. Empty
	// means the whole forest.
	Scope string
	// Since is the window's start; zero means the first event in scope.
	Since time.Time
	// Until is the window's end; zero means now. The dashboard passes the
	// scrubber cursor's moment when parked in history.
	Until time.Time
	// Bucket overrides the sample width; empty means the automatic choice
	// for the window's span, which agrees with BucketFor for every named
	// range key. A zero Since is RangeAll, which buckets by the history's
	// span; every window uses the same rule (BucketForSpan in bucket.go).
	//
	// Subtree membership for Scope is decided as of Until: a task moved
	// into the subtree counts over its whole history, one moved out counts
	// nowhere.
	Bucket Bucket
	// Location is the calendar that buckets align to; nil means time.Local.
	Location *time.Location
	// Trace asks for Report.Trace, the fine samples the dashboard draws
	// the burn-up from. `job stats` does not ask for it.
	Trace bool
}

// Report is the burn-up series and headline figures for one window. It is
// the single wire type of `job stats --format=json`, and the dashboard's
// chart panel renders from it.
type Report struct {
	Schema int          `json:"schema"`
	Window ReportWindow `json:"window"`
	Leaves LeafFigures  `json:"leaves"`
	Plans  PlanFigures  `json:"plans"`
	Pace   PaceFigures  `json:"pace"`
	// DoneByActor is leaves closed in the window, by the identity that
	// closed them, most first. It measures identity hygiene as much as
	// who did the work (decision 9).
	DoneByActor []ActorCount `json:"done_by_actor"`
	// Series is one sample per bucket, oldest first; the last sample's
	// End is Window.Until.
	Series []Sample `json:"series"`
	// Activity is the window's leaf transitions and leaf events per bucket,
	// aligned with Series; its created and done sum to Leaves.Created and
	// Leaves.Done.
	Activity []ActivityCount `json:"activity"`
	// Trace is the burn-up at drawing resolution, present only when the
	// query asked for it: the state as of Window.Since, then every
	// TraceStepFor(span) on the local clock, then Window.Until — a few
	// hundred samples, from the same replay as Series and with the same
	// meaning (decision 1 of project/2026-09-27-chart-panel-revision.md).
	// Because the first sample is the state at Since, a window delta is
	// last − first.
	Trace []Sample `json:"trace,omitempty"`
	// Imports marks each imported event in the window, oldest first.
	Imports []ImportMarker `json:"imports"`
}

// ReportWindow echoes what the report covers, after defaults resolved.
type ReportWindow struct {
	Scope  string    `json:"scope,omitempty"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	Bucket Bucket    `json:"bucket"`
	// Timezone is the IANA name of the calendar buckets align to.
	Timezone string `json:"timezone"`
}

// LeafFigures counts leaves (decision 1). Created, Done and Canceled are
// transitions inside the window; Open and Blocked are the state at Until.
//
// Each transition is judged by its final occurrence as of Until, over tasks
// that are leaves at Until. Created is leaves created in the window (a leaf
// split since is a parent and not counted; its children are). Done is leaves
// done at Until whose last close fell in the window, so a reopen→close cycle
// is one close and a close undone before Until is none; Canceled likewise.
// Pace, DoneByActor and PlanFigures.Closed read the same closes.
type LeafFigures struct {
	Created  int `json:"created"`
	Done     int `json:"done"`
	Canceled int `json:"canceled"`
	Open     int `json:"open"`
	Blocked  int `json:"blocked"`
}

// PlanFigures counts plans: non-issue roots, and imported top-level tasks
// for Imported and the import-to-close median (decisions 4 and 5).
type PlanFigures struct {
	// Imported is imported events in the window. Stores from before the
	// event existed have none; the figure does not guess.
	Imported int `json:"imported"`
	// FirstImportAt is the earliest imported event in scope anywhere in
	// the store up to Until, not just in the window; nil when there is
	// none. It tells "no imports ever recorded" from "none in this window".
	FirstImportAt *time.Time `json:"first_import_at"`
	// Closed is plans closed in the window; Open is plans open at Until.
	Closed int `json:"closed"`
	Open   int `json:"open"`
	// MedianImportToCloseSeconds is over imported plans closed in the
	// window; nil when there are none.
	MedianImportToCloseSeconds *int64 `json:"median_import_to_close_seconds"`
}

// PaceFigures is throughput and cycle time for leaves closed in the window.
// A median is nil when no leaf in the window qualifies.
type PaceFigures struct {
	DonePerWeek                float64 `json:"done_per_week"`
	MedianCreatedToDoneSeconds *int64  `json:"median_created_to_done_seconds"`
	MedianClaimedToDoneSeconds *int64  `json:"median_claimed_to_done_seconds"`
}

// ActorCount is one row of DoneByActor.
type ActorCount struct {
	Actor string `json:"actor"`
	Done  int    `json:"done"`
}

// Sample is the store's state as of End (decision 2): what `job status`
// would have said at that instant, counted in leaves.
type Sample struct {
	End time.Time `json:"end"`
	// Scope is leaves that exist and are not canceled; the chart's upper
	// line. Done is the lower line. Open = Scope − Done, and Blocked is
	// the part of Open with an unresolved blocker.
	Scope    int `json:"scope"`
	Done     int `json:"done"`
	Open     int `json:"open"`
	Blocked  int `json:"blocked"`
	Canceled int `json:"canceled"`
	// PlansDone is plans in the done state at End, for root velocity.
	PlansDone int `json:"plans_done"`
}

// ActivityCount is what fell in one bucket [Start, End), counted over the
// same tasks as LeafFigures: those that are leaves at Until (decision 10 of
// project/2026-09-27-chart-panel-revision.md).
//
// Created and Done are the LeafFigures transitions attributed to the bucket
// of their final occurrence: leaves created there, and leaves done at Until
// whose last close fell there. Summed over the buckets they are exactly
// Leaves.Created and Leaves.Done. Claimed and Blocked are claim and block
// events there on those leaves; a parent's never count, nor does a claim on
// a task split since.
//
// The first bucket starts at Window.Since rather than its calendar floor,
// and the last is closed at Window.Until, so the buckets cover exactly the
// window.
type ActivityCount struct {
	Start   time.Time `json:"start"`
	End     time.Time `json:"end"`
	Created int       `json:"created"`
	Claimed int       `json:"claimed"`
	Done    int       `json:"done"`
	Blocked int       `json:"blocked"`
}

// ImportMarker is one imported event, for the chart's timeline ticks.
type ImportMarker struct {
	At     time.Time `json:"at"`
	TaskID string    `json:"task_id"`
	Title  string    `json:"title"`
	Source string    `json:"source"`
}
