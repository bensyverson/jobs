package job

import "time"

// traceSteps is the round steps a trace samples at, finest first.
var traceSteps = []time.Duration{
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 48 * time.Hour, 7 * 24 * time.Hour,
}

// maxTraceSamples bounds a trace. The finest step within it gives each named
// range key the step decision 1 of project/2026-09-27-chart-panel-revision.md
// names — 1h every minute, 1d every 5 minutes, 7d every 30, 14d hourly, 30d
// every 2 hours — since the next step down would take 720, 672 or 720
// samples, and lands any other span a few hundred.
const maxTraceSamples = 400

// TraceStepFor is the step between a trace's samples over a window of span:
// the finest round step that keeps it to a few hundred samples. It is to the
// trace what BucketForSpan is to the series.
func TraceStepFor(span time.Duration) time.Duration {
	for _, s := range traceSteps {
		if span <= maxTraceSamples*s {
			return s
		}
	}
	return traceSteps[len(traceSteps)-1]
}

// withTrace lays the trace's instants over w: Since, then every step on the
// local clock of Since's zone strictly inside the window, then Until. Steps
// are fixed lengths, so across a DST change the later ones sit an hour off
// the round local time; a trace is for drawing, and that shift draws nothing.
func (w *reportWindow) withTrace() {
	step := TraceStepFor(w.until.Sub(w.since))
	w.traceAt = []time.Time{w.since}
	t := floorOnLocalClock(w.since, step)
	if !t.After(w.since) {
		t = t.Add(step)
	}
	for ; t.Before(w.until); t = t.Add(step) {
		w.traceAt = append(w.traceAt, t)
	}
	if w.until.After(w.since) {
		w.traceAt = append(w.traceAt, w.until)
	}
	w.traceMS = make([]int64, len(w.traceAt))
	for i, at := range w.traceAt {
		w.traceMS[i] = at.UnixMilli()
	}
}
