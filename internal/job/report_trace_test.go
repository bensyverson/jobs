package job

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Decision 1 of project/2026-09-27-chart-panel-revision.md, one case per key.
func TestTraceStepFor_PerKey(t *testing.T) {
	cases := []struct {
		key  RangeKey
		want time.Duration
	}{
		{RangeHour, time.Minute},
		{RangeDay, 5 * time.Minute},
		{Range7D, 30 * time.Minute},
		{Range14D, time.Hour},
		{Range30D, 2 * time.Hour},
	}
	for _, c := range cases {
		d, _ := c.key.Duration()
		if got := TraceStepFor(d); got != c.want {
			t.Errorf("%s: TraceStepFor(%v) = %v, want %v", c.key, d, got, c.want)
		}
	}
}

// All-history and arbitrary spans land a few hundred samples on a round step.
func TestTraceStepFor_ArbitrarySpansLandAFewHundredSamples(t *testing.T) {
	const day = 24 * time.Hour
	round := map[time.Duration]bool{}
	for _, s := range traceSteps {
		round[s] = true
	}
	for _, span := range []time.Duration{36 * time.Hour, 3 * day, 10 * day, 60 * day, 90 * day, 365 * day, 700 * day} {
		step := TraceStepFor(span)
		if !round[step] {
			t.Errorf("span %v: step %v is not one of the round steps", span, step)
		}
		if n := int(span / step); n < 150 || n > 400 {
			t.Errorf("span %v: %d samples at %v, want a few hundred", span, n, step)
		}
	}
	if got := TraceStepFor(0); got != time.Minute {
		t.Errorf("TraceStepFor(0) = %v, want the finest step", got)
	}
}

func TestReport_TraceIsAbsentUnlessRequested(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour)).add("", "Plan")
	r := f.report(daily(2))
	if r.Trace != nil {
		t.Errorf("Trace = %d samples, want none when not requested", len(r.Trace))
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"trace"`) {
		t.Error(`JSON carries "trace" though none was requested`)
	}
}

// The trace's first sample is at Since and its last at Until; between them
// it steps on the local clock at the span's step.
func TestReport_TraceRunsFromSinceToUntil(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0)).add("", "Plan")
	since := day(1, 7*time.Minute+30*time.Second)
	until := since.Add(time.Hour)
	r := f.report(ReportQuery{Since: since, Until: until, Trace: true})
	if len(r.Trace) < 2 {
		t.Fatalf("Trace = %d samples, want a trace", len(r.Trace))
	}
	if first := r.Trace[0].End; !first.Equal(since) {
		t.Errorf("first trace sample at %s, want Since %s", first, since)
	}
	if last := r.Trace[len(r.Trace)-1].End; !last.Equal(until) {
		t.Errorf("last trace sample at %s, want Until %s", last, until)
	}
	for i := 1; i < len(r.Trace)-1; i++ {
		at := r.Trace[i].End
		if !at.Equal(at.Truncate(time.Minute)) {
			t.Errorf("interior sample %d at %s is not on the minute", i, at)
		}
		if !at.After(r.Trace[i-1].End) {
			t.Errorf("sample %d at %s does not follow %s", i, at, r.Trace[i-1].End)
		}
	}
	// :07:30 → :08 … 1:07 on the minute → 1:07:30.
	if len(r.Trace) != 62 {
		t.Errorf("Trace = %d samples, want 62", len(r.Trace))
	}
}

// On a step-aligned window each key's trace is span/step + 1 samples.
func TestReport_TraceSampleCountPerKey(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0)).add("", "Plan")
	until := day(40)
	cases := []struct {
		key  RangeKey
		want int
	}{
		{RangeHour, 61},
		{RangeDay, 289},
		{Range7D, 337},
		{Range14D, 337},
		{Range30D, 361},
	}
	for _, c := range cases {
		d, _ := c.key.Duration()
		r := f.report(ReportQuery{Since: until.Add(-d), Until: until, Trace: true})
		if len(r.Trace) != c.want {
			t.Errorf("%s: Trace = %d samples, want %d", c.key, len(r.Trace), c.want)
		}
	}
}

// All history starts the trace at the first event.
func TestReport_TraceOverAllHistoryStartsAtTheFirstEvent(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, 5*time.Hour)).add("", "Plan")
	f.at(day(3))
	r := f.report(ReportQuery{Trace: true})
	if len(r.Trace) == 0 || !r.Trace[0].End.Equal(r.Window.Since) {
		t.Fatalf("trace should start at Window.Since %s", r.Window.Since)
	}
	if last := r.Trace[len(r.Trace)-1].End; !last.Equal(r.Window.Until) {
		t.Errorf("last trace sample at %s, want Until %s", last, r.Window.Until)
	}
}

// A trace sample and a series sample at the same instant are the same state,
// and the trace resolves the moment a transition happened.
func TestReport_TraceAgreesWithSeries(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, 10*time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	c := f.add(root, "C")
	f.at(day(1, 10*time.Hour)).done(a)
	f.at(day(1, 14*time.Hour)).block(b, c)
	f.at(day(2, 9*time.Hour)).reopen(a)
	f.at(day(2, 11*time.Hour)).cancel(c)
	f.at(day(3, 10*time.Hour)).done(a)

	q := daily(4)
	q.Trace = true
	r := f.report(q)
	byEnd := map[int64]Sample{}
	for _, s := range r.Trace {
		byEnd[s.End.UnixMilli()] = s
	}
	shared := 0
	for _, s := range r.Series {
		ts, ok := byEnd[s.End.UnixMilli()]
		if !ok {
			continue
		}
		shared++
		if ts != s {
			t.Errorf("at %s: trace %+v, series %+v", s.End, ts, s)
		}
	}
	if shared != len(r.Series) {
		t.Errorf("%d of %d series instants are in the trace, want all of them on a day-aligned window", shared, len(r.Series))
	}
	// Events at an instant are included in it, as in the series.
	if s := traceAt(t, r, day(1, 10*time.Hour)); s.Done != 1 {
		t.Errorf("at the close: Done = %d, want 1", s.Done)
	}
	if s := traceAt(t, r, day(1, 9*time.Hour+45*time.Minute)); s.Done != 0 {
		t.Errorf("before the close: Done = %d, want 0", s.Done)
	}
}

// The first sample is the state as of Since, so a window's cancels are the
// last sample's Canceled less the first's.
func TestReport_TraceStartsFromTheStateAtSince(t *testing.T) {
	f := newReportFixture(t)
	f.at(day(0, time.Hour))
	root := f.add("", "Plan")
	a := f.add(root, "A")
	b := f.add(root, "B")
	f.add(root, "C")
	f.at(day(0, 2*time.Hour)).cancel(a)
	f.at(day(1, 2*time.Hour)).cancel(b)

	r := f.report(ReportQuery{Since: day(1), Until: day(2), Trace: true})
	first, last := r.Trace[0], r.Trace[len(r.Trace)-1]
	wantSample(t, "at Since", first, Sample{Scope: 2, Open: 2, Canceled: 1})
	if got := last.Canceled - first.Canceled; got != r.Leaves.Canceled {
		t.Errorf("canceled in window from the trace = %d, Leaves.Canceled = %d", got, r.Leaves.Canceled)
	}
}

func traceAt(t *testing.T, r Report, at time.Time) Sample {
	t.Helper()
	for _, s := range r.Trace {
		if s.End.Equal(at) {
			return s
		}
	}
	t.Fatalf("no trace sample at %s", at)
	return Sample{}
}
