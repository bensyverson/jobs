package handlers

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"time"

	job "github.com/bensyverson/jobs/internal/job"
	"github.com/bensyverson/jobs/internal/web/chart"
)

// The chart panel's catalog entry. Every state is a job.Report — the
// wire type `job stats --format=json` also emits — shaped by
// buildChartPanel exactly as Home shapes a live one, trace included.
// The reports are constructed rather than replayed so the catalog
// needs no store; each is one a real store produces.

// previewUntil pins every state's window end, so the axis dates in a
// contact sheet never drift between runs.
var previewUntil = time.Date(2026, 9, 26, 17, 0, 0, 0, time.UTC)

// previewCursor is the log position the parked state is rendered for.
// Only its presence matters to the panel: the report's window already
// ends at the cursor's moment, previewUntil.
const previewCursor = "1790442000000-r1-42"

func chartPanelPreview() previewComponent {
	return previewComponent{
		Slug:   "chart-panel",
		Title:  "Chart panel",
		Source: "internal/web/templates/html/partials/chart_panel.html.tmpl",
		Block:  "chart_panel",
		States: []previewState{
			previewPanelState("empty", "Empty store", job.Range7D, emptyStoreReport(), nil, "", false,
				"A store with nothing in it yet: the panel keeps its selector and says so in one quiet line rather than drawing two flat lines."),
			previewPanelState("hour", "The last hour", job.RangeHour, hourReport(), nil, "", false,
				"1H: minor marks every 5 minutes, the start labelled with its clock time and the end reading Now; interior labels never touch the edge labels."),
			previewPanelState("single-day", "A single day", job.RangeDay, singleDayReport(), nil, "", false,
				"1D (the Home default) over a store that began this morning: the fitted axis puts the empty morning on the baseline, and the end labels read what the day created and closed, over their totals."),
			previewPanelState("imports", "Imports in the day", job.RangeDay, importsReport(), nil, "", false,
				"Three plans imported in the day, two of them three minutes apart: each tick is a link with its own hover and focus ring, and the pair must not swallow each other."),
			previewPanelState("one-sample", "One sample", job.RangeAll, oneSampleReport(), nil, "", false,
				"All over a store a few hours old buckets by day, so the series (the data table, the histogram) is a single sample; the burn-up draws the minute trace — a step at the first import, then flat — not a lone dot."),
			previewPanelState("reopen-dip", "A reopen dip", job.Range14D, reopenDipReport(), nil, "", false,
				"Each point is the state as of t (decision 2), so a reopen dips the done line and the next close restores it — the dip is honest, not a bug."),
			previewPanelState("fitted-week", "A week near the top", job.Range7D, fittedWeekReport(), nil, "", false,
				"A long-lived store whose week moved from 390 to 402: the axis spans exactly that, and the end labels say what the week added while the totals sit beneath in small type."),
			previewPanelState("flat", "A flat window", job.Range7D, flatReport(), nil, "", false,
				"Everything done and nothing moved all week (min == max): the line sits mid-chart between round gridlines, and both end labels read plus zero."),
			previewPanelState("crowded", "Crowded history", job.RangeAll, crowdedReport(), nil, "", false,
				"Five months in weekly buckets with two dozen imports: four-digit end labels and legend counts size their columns, gridlines stay sparse, import ticks stay quiet."),
			previewPanelState("mostly-canceled", "Mostly canceled", job.Range30D, mostlyCanceledReport(), nil, "", false,
				"Canceling starts mid-window: scope falls, and the canceled band (decision 6) grows from zero on top of it, so what was cut stays visible; the created figure sits at the top of the band with that value beneath."),
			previewPanelState("parked", "Parked under the scrubber", job.RangeDay, singleDayReport(), nil, previewCursor, false,
				"Rendered for ?at=: the right edge of the axis reads the time at the cursor instead of Now, and the range tabs keep the cursor."),
			previewPanelState("fetching", "Fetching", job.Range14D, reopenDipReport(), nil, "", true,
				"The script sets aria-busy while it fetches the panel for a new range or scrubber position; the old panel dims instead of blanking."),
			hoverPanelState("hover", "Hovering the burn-up", job.RangeDay, singleDayReport(), previewUntil.Add(-5*time.Hour), ChartBurnup,
				"The crosshair at 12:00 on 1D, declared with the attributes the script sets: both charts carry the line, the dots and end labels move to that sample and read the figures of the day up to it, the legend counts to that hour, and the tooltip docks away from the pointer."),
			hoverPanelState("hover-activity", "Hovering the histogram", job.Range14D, reopenDipReport(), previewUntil.Add(-108*time.Hour), ChartActivity,
				"The pointer over the histogram late in 14D, during the reopen dip: the tooltip moves over the histogram and docks left, and the done figure up to that day sits below the window figure."),
			slidingPanelState("sliding", "Switching range", job.Range7D, fittedWeekReport(),
				"Mid-flight between two ranges (data-chart-anim, as the script sets it): the histogram, the end labels and the axis and gridline labels fade while the burn-up slides, and come back with the new fragment. The in-between frames of the burn-up need both islands, so this state shows the lines of the destination."),
			previewPanelState("error", "Report unavailable", job.Range7D, job.Report{Schema: job.ReportSchema}, errPreviewReport, "", false,
				"BuildReport failed (here: the store could not be read). The selector stays usable and the message names the cause."),
		},
	}
}

// errPreviewReport stands in for a failed BuildReport in the error state.
var errPreviewReport = errors.New("database is locked")

// previewPanelState builds one state the way loadChartPanel builds
// Home's: at, when set, is the ?at= cursor, which parks the axis's end.
func previewPanelState(slug, name string, key job.RangeKey, rep job.Report, err error, at string, pending bool, note string) previewState {
	q := url.Values{}
	if key != homeRanges.Default {
		q.Set("range", string(key))
	}
	end := chart.EndsNow
	if at != "" {
		q.Set("at", at)
		end = chart.EndsAtCursor
	}
	p := buildChartPanel("preview-"+slug, rep, err, homePanelNav(q), end, time.UTC)
	p.Pending = pending
	return previewState{Slug: slug, Name: name, Note: note, Payload: p}
}

// hoverPanelState is a drawn state with the crosshair declared at a
// moment over one chart — the attributes the script sets on a hover.
func hoverPanelState(slug, name string, key job.RangeKey, rep job.Report, at time.Time, over ChartKind, note string) previewState {
	s := previewPanelState(slug, name, key, rep, nil, "", false, note)
	p := s.Payload.(ChartPanel)
	p.HoverAt, p.HoverChart = at.UnixMilli(), over
	s.Payload = p
	return s
}

// slidingPanelState is a drawn state declared mid-slide.
func slidingPanelState(slug, name string, key job.RangeKey, rep job.Report, note string) previewState {
	s := previewPanelState(slug, name, key, rep, nil, "", false, note)
	p := s.Payload.(ChartPanel)
	p.Sliding = true
	s.Payload = p
	return s
}

// bucketPoint is one bucket's sample and activity, as a generator
// returns them. Its activity carries claimed and blocked only:
// withWindowFigures derives created and done from the series.
type bucketPoint struct {
	sample   job.Sample
	activity job.ActivityCount
}

// previewReport steps bucket across [since, until] and asks gen for
// each bucket's state and activity, the way BuildReport aligns them:
// sample i ends where bucket i ends, the last at until.
func previewReport(since, until time.Time, bucket job.Bucket, gen func(i, n int) bucketPoint) job.Report {
	var starts []time.Time
	for t := bucket.Floor(since); t.Before(until); t = bucket.Next(t) {
		starts = append(starts, t)
	}
	rep := job.Report{
		Schema: job.ReportSchema,
		Window: job.ReportWindow{Since: since, Until: until, Bucket: bucket, Timezone: "UTC"},
	}
	for i, start := range starts {
		end := until
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		pt := gen(i, len(starts))
		pt.sample.End = end
		pt.sample.Open = pt.sample.Scope - pt.sample.Done
		if start.Before(since) {
			start = since
		}
		pt.activity.Start, pt.activity.End = start, end
		rep.Series = append(rep.Series, pt.sample)
		rep.Activity = append(rep.Activity, pt.activity)
	}
	return withWindowFigures(rep, job.Sample{})
}

// withWindowFigures derives from the series — base being the state at
// Since, zero for a store born inside the window — everything a real
// report counts from the same events, so no state can disagree with
// itself:
//
//   - each bucket's created and done: the leaves the series gained in
//     it (created counts leaves later canceled too), each counted once,
//     where it last rose — a reopen takes back an earlier close;
//   - LeafFigures: created and done as those buckets' sums (decision 10
//     of the chart panel revision: the histogram counts what the labels
//     count), canceled as the change since base, open and blocked as
//     the state at the window's end;
//   - the Trace, as BuildReport lays it when the dashboard asks: base
//     at Since, then the series interpolated at TraceStepFor(span).
//
// Generators supply the rest of the activity (claimed, blocked).
func withWindowFigures(rep job.Report, base job.Sample) job.Report {
	created := make([]int, len(rep.Series))
	done := make([]int, len(rep.Series))
	for i, s := range rep.Series {
		created[i], done[i] = s.Scope+s.Canceled, s.Done
	}
	created = risesPerBucket(base.Scope+base.Canceled, created)
	done = risesPerBucket(base.Done, done)
	last := rep.Series[len(rep.Series)-1]
	rep.Leaves = job.LeafFigures{
		Canceled: max(0, last.Canceled-base.Canceled),
		Open:     last.Open,
		Blocked:  last.Blocked,
	}
	for i := range rep.Activity {
		rep.Activity[i].Created, rep.Activity[i].Done = created[i], done[i]
		rep.Leaves.Created += created[i]
		rep.Leaves.Done += done[i]
	}
	base.End = rep.Window.Since
	rep.Trace = previewTrace(rep.Window, interpolate(append([]job.Sample{base}, rep.Series...)))
	return rep
}

// risesPerBucket spreads a cumulative count's rise from base over the
// buckets it rose in. A fall takes back the latest rises before it, so
// each leaf is counted once, at its last rise: a leaf closed, reopened
// and closed again counts in the bucket of the second close.
func risesPerBucket(base int, vals []int) []int {
	out := make([]int, len(vals))
	prev := base
	for i, v := range vals {
		d := v - prev
		prev = v
		if d >= 0 {
			out[i] = d
			continue
		}
		for j := i - 1; j >= 0 && d < 0; j-- {
			take := min(out[j], -d)
			out[j] -= take
			d += take
		}
	}
	return out
}

// previewTrace samples state at the instants BuildReport's trace
// takes: Since, every TraceStepFor(span) strictly inside the window,
// then Until.
func previewTrace(w job.ReportWindow, state func(time.Time) job.Sample) []job.Sample {
	step := job.TraceStepFor(w.Until.Sub(w.Since))
	at := []time.Time{w.Since}
	for t := w.Since.Truncate(step).Add(step); t.Before(w.Until); t = t.Add(step) {
		at = append(at, t)
	}
	if w.Until.After(w.Since) {
		at = append(at, w.Until)
	}
	out := make([]job.Sample, len(at))
	for i, t := range at {
		s := state(t)
		s.End, s.Open = t, s.Scope-s.Done
		out[i] = s
	}
	return out
}

// interpolate is the state at any instant between samples (oldest
// first), each count eased linearly from one sample to the next and
// rounded to a whole leaf: a real store's changes land through a
// bucket, not all at its end.
func interpolate(samples []job.Sample) func(time.Time) job.Sample {
	return func(t time.Time) job.Sample {
		i := 1
		for i < len(samples)-1 && samples[i].End.Before(t) {
			i++
		}
		a, b := samples[i-1], samples[min(i, len(samples)-1)]
		f := 1.0
		if span := b.End.Sub(a.End); span > 0 {
			f = math.Min(1, math.Max(0, float64(t.Sub(a.End))/float64(span)))
		}
		lerp := func(x, y int) int { return int(math.Round(float64(x) + f*float64(y-x))) }
		return job.Sample{
			Scope: lerp(a.Scope, b.Scope), Done: lerp(a.Done, b.Done), Blocked: lerp(a.Blocked, b.Blocked),
			Canceled: lerp(a.Canceled, b.Canceled), PlansDone: lerp(a.PlansDone, b.PlansDone),
		}
	}
}

// wobble is a deterministic 0..1 texture so generated activity is not
// a ruler-straight ramp.
func wobble(i int) float64 { return (math.Sin(float64(i)*1.7)+math.Sin(float64(i)*0.63))/4 + 0.5 }

// startedBefore is rep for a store that already held work at Since:
// its window figures count from the first sample, not from zero.
func startedBefore(rep job.Report) job.Report {
	return withWindowFigures(rep, rep.Series[0])
}

func emptyStoreReport() job.Report {
	return previewReport(previewUntil.Add(-7*24*time.Hour), previewUntil, job.BucketSixHours,
		func(i, n int) bucketPoint { return bucketPoint{} })
}

// hourReport is a busy hour in a store with a few dozen leaves, in the
// bucket the core picks for 1H.
func hourReport() job.Report {
	since := previewUntil.Add(-time.Hour)
	return startedBefore(previewReport(since, previewUntil, job.BucketFor(job.RangeHour, 0), func(i, n int) bucketPoint {
		m := i * 60 / n // minutes into the hour
		scope := 30 + min(3, m/15)
		done := min(scope-2, 20+m/9)
		blocked := 0
		if m >= 20 && m < 40 {
			blocked = 1
		}
		act := job.ActivityCount{Claimed: int(2 * wobble(i))}
		return bucketPoint{sample: job.Sample{Scope: scope, Done: done, Blocked: blocked}, activity: act}
	}))
}

// importsReport is the single day with three plans imported: two in
// the morning three minutes apart, one after lunch.
func importsReport() job.Report {
	rep := singleDayReport()
	since := rep.Window.Since
	rep.Imports = []job.ImportMarker{
		{At: since.Add(16*time.Hour + 2*time.Minute), TaskID: "Pq3xT", Title: "Onboarding flow", Source: "onboarding.md"},
		{At: since.Add(16*time.Hour + 5*time.Minute), TaskID: "Hn4wQ", Title: "Billing fixes", Source: "billing.md"},
		{At: since.Add(20*time.Hour + 40*time.Minute), TaskID: "Ty6vB", Title: "Search tuning", Source: "search-tuning.md"},
	}
	return rep
}

func singleDayReport() job.Report {
	since := previewUntil.Add(-24 * time.Hour)
	rep := previewReport(since, previewUntil, job.BucketHour, func(i, n int) bucketPoint {
		// Hour 16 of the window is 09:00, when the day's plan landed.
		h := i - 16
		if h < 0 {
			return bucketPoint{}
		}
		done := min(14, 2*h)
		blocked := 0
		if h >= 2 && h <= 5 {
			blocked = 2
		}
		pt := bucketPoint{sample: job.Sample{Scope: 14 + min(h, 3), Done: done, Blocked: blocked}}
		pt.activity = job.ActivityCount{Claimed: 2 + h%2, Blocked: blocked / 2}
		return pt
	})
	rep.Imports = []job.ImportMarker{{At: since.Add(16*time.Hour + 2*time.Minute), TaskID: "Pq3xT", Title: "Onboarding flow", Source: "onboarding.md"}}
	return rep
}

func oneSampleReport() job.Report {
	since := previewUntil.Add(-4*time.Hour - 50*time.Minute)
	rep := previewReport(since, previewUntil, job.BucketDay, func(i, n int) bucketPoint {
		return bucketPoint{
			sample:   job.Sample{Scope: 9, Done: 3, Blocked: 1},
			activity: job.ActivityCount{Claimed: 4, Blocked: 1},
		}
	})
	// The store began with one import a minute in: nothing before it,
	// its state after — a step, not the ramp interpolation would draw.
	imported := since.Add(time.Minute)
	rep.Trace = previewTrace(rep.Window, func(t time.Time) job.Sample {
		if t.Before(imported) {
			return job.Sample{}
		}
		return rep.Series[0]
	})
	rep.Imports = []job.ImportMarker{{At: imported, TaskID: "Zr8Kd", Title: "First plan", Source: "plan.md"}}
	return rep
}

func reopenDipReport() job.Report {
	return startedBefore(previewReport(previewUntil.Add(-14*24*time.Hour), previewUntil, job.BucketDay, func(i, n int) bucketPoint {
		scope := 40 + min(i, 10)*2
		done := min(scope, 3+i*4)
		act := job.ActivityCount{Claimed: 4 + i%3}
		switch i {
		case 9: // leaves reopened
			done -= 7
		case 10:
			done -= 3
		}
		blocked := 0
		if i >= 4 && i <= 9 {
			blocked = 3
		}
		act.Blocked = blocked / 3
		return bucketPoint{sample: job.Sample{Scope: scope, Done: done, Blocked: blocked}, activity: act}
	}))
}

// fittedWeekReport is a long-lived store's week: hundreds of leaves in
// scope, a dozen added and closed — the shape that drew two flat lines
// under the old zero-based axis.
func fittedWeekReport() job.Report {
	return startedBefore(previewReport(previewUntil.Add(-7*24*time.Hour), previewUntil, job.BucketSixHours, func(i, n int) bucketPoint {
		scope := 392 + min(10, i*10/n+i%3/2)
		done := min(scope-2, 388+i*14/n)
		blocked := 0
		if i >= n/3 && i < n/2 {
			blocked = 1
		}
		act := job.ActivityCount{Claimed: 1 + i%2}
		return bucketPoint{sample: job.Sample{Scope: scope, Done: done, Blocked: blocked}, activity: act}
	}))
}

// flatReport is a week in which nothing changed: every sample equal.
func flatReport() job.Report {
	return startedBefore(previewReport(previewUntil.Add(-7*24*time.Hour), previewUntil, job.BucketSixHours, func(i, n int) bucketPoint {
		return bucketPoint{sample: job.Sample{Scope: 402, Done: 402}}
	}))
}

func crowdedReport() job.Report {
	since := time.Date(2026, 4, 20, 9, 30, 0, 0, time.UTC)
	rep := previewReport(since, previewUntil, job.BucketWeek, func(i, n int) bucketPoint {
		f := float64(i+1) / float64(n)
		scope := int(math.Round(1155 * math.Pow(f, 0.8)))
		done := int(math.Round(1080 * math.Pow(f, 1.15)))
		if i == n-1 {
			scope, done = 1155, 1080
		}
		done = min(done, scope)
		blocked := int(float64(scope-done) * 0.2)
		return bucketPoint{
			sample:   job.Sample{Scope: scope, Done: done, Blocked: blocked, Canceled: 38 * (i + 1) / n},
			activity: job.ActivityCount{Claimed: 40 + int(50*wobble(i+3)), Blocked: 3 + int(8*wobble(i+7))},
		}
	})
	for k := range 24 {
		at := since.Add(time.Duration(float64(previewUntil.Sub(since)) * (float64(k) + 0.3*wobble(k)) / 24))
		rep.Imports = append(rep.Imports, job.ImportMarker{
			At: at, TaskID: fmt.Sprintf("Im%03d", k), Title: fmt.Sprintf("Plan %d", k+1), Source: fmt.Sprintf("plan-%02d.md", k+1),
		})
	}
	return rep
}

func mostlyCanceledReport() job.Report {
	return previewReport(previewUntil.Add(-30*24*time.Hour), previewUntil, job.BucketDay, func(i, n int) bucketPoint {
		canceled := 0
		if i >= 8 {
			canceled = min(48, (i-7)*4)
		}
		scope := 60 - canceled
		done := min(scope, i/3)
		act := job.ActivityCount{Claimed: i % 2}
		return bucketPoint{sample: job.Sample{Scope: scope, Done: done, Canceled: canceled}, activity: act}
	})
}
