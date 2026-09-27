/*
  What the chart panel's crosshair shows at a moment (decision 9 of
  project/2026-09-27-chart-panel-revision.md): the trace sample nearest
  the pointer, the histogram bucket that sample has seen last, the
  window's figures up to that bucket, and the tooltip's words.

  Every number is read off the island (chart.PanelData) — samples and
  bucket counts the server made. The figures "up to a moment" are the
  buckets' own counts summed, which decision 10 makes equal to the end
  labels at the right edge; nothing here re-derives a count.

  Pure: no DOM. Times are Unix milliseconds, read in the island's zone
  (the report window's calendar, which the axis labels use too).
*/

import { VIEW_W, fmtNum, fmtPct, markersAt, xOf } from "./chart-geometry.mjs";

const DAY_MS = 24 * 3600 * 1000;
// A window up to this long reads its moments as clock times, as the
// axis's edge labels do (axis.go's minorScales).
const CLOCK_SPAN_MS = 2 * DAY_MS;
// Up to this long, a moment reads as a date and a time; beyond, a date
// and a year.
const DATED_SPAN_MS = 92 * DAY_MS;

// nearestSample is the index of the trace sample closest to t — not the
// one before it, so the crosshair tracks the data rather than the
// pixels. Moments off either end clamp to it; a tie goes to the later
// sample. -1 for an empty trace.
export function nearestSample(trace, t) {
  if (!trace.length) return -1;
  let lo = 0;
  let hi = trace.length - 1;
  if (t <= trace[lo].t) return lo;
  if (t >= trace[hi].t) return hi;
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1;
    if (trace[mid].t < t) lo = mid;
    else hi = mid;
  }
  return t - trace[lo].t < trace[hi].t - t ? lo : hi;
}

// momentAt maps a fraction of the plot's width onto the window.
export function momentAt(d, frac) {
  const f = Math.max(0, Math.min(1, frac));
  return d.since + f * (d.until - d.since);
}

// bucketAt is the bucket whose events the state at t has seen last: the
// first one ending at or after t. A boundary belongs to the bucket that
// ends there; Since, to the first. -1 when there are none.
export function bucketAt(buckets, t) {
  if (!buckets.length) return -1;
  for (let i = 0; i < buckets.length; i++) {
    if (buckets[i].end >= t) return i;
  }
  return buckets.length - 1;
}

// cumulativeThrough sums each kind over buckets 0..i.
export function cumulativeThrough(buckets, i) {
  const out = { created: 0, claimed: 0, done: 0, blocked: 0 };
  for (let k = 0; k <= i && k < buckets.length; k++) {
    out.created += buckets[k].created;
    out.claimed += buckets[k].claimed;
    out.done += buckets[k].done;
    out.blocked += buckets[k].blocked;
  }
  return out;
}

// formatCount spells a count with thousands separators, as chart.Count.
export function formatCount(n) {
  return String(n).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// hoverModel is everything the panel shows with the crosshair on trace
// sample i: the end labels' figures and totals (the total beneath each
// is the value where its label sits — created on the band's top), the
// legend's running totals, where the crosshair, dots and labels go.
export function hoverModel(d, i) {
  const s = d.trace[i];
  const b = bucketAt(d.buckets, s.t);
  const c = cumulativeThrough(d.buckets, b);
  const x = xOf(s.t, d.since, d.until);
  return {
    index: i,
    t: s.t,
    sample: s,
    bucket: b,
    slice: b >= 0 ? d.buckets[b] : null,
    created: "+" + formatCount(c.created),
    done: "+" + formatCount(c.done),
    createdTotal: "of " + formatCount(s.scope + s.canceledInWindow),
    doneTotal: "of " + formatCount(s.done),
    legend: {
      created: formatCount(c.created),
      claimed: formatCount(c.claimed),
      done: formatCount(c.done),
      blocked: formatCount(c.blocked),
    },
    x: fmtPct((x / VIEW_W) * 100),
    activityX: fmtNum(x),
    markers: markersAt(d, i),
  };
}

const formatters = new Map();

// dateParts reads t in zone (local when empty) as English calendar
// parts, the way the server's axis labels read ("Sep 25", "14:05").
function dateParts(t, zone) {
  const key = zone || "";
  let f = formatters.get(key);
  if (!f) {
    const opts = {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    };
    try {
      f = new Intl.DateTimeFormat("en-US", { ...opts, timeZone: zone || undefined });
    } catch (_) {
      // A zone this browser does not know: its own is the best guess.
      f = new Intl.DateTimeFormat("en-US", opts);
    }
    formatters.set(key, f);
  }
  const out = {};
  for (const p of f.formatToParts(new Date(t))) out[p.type] = p.value;
  return out;
}

const clock = (p) => `${p.hour}:${p.minute}`;
const date = (p) => `${p.month} ${p.day}`;

// formatMoment names a trace sample's moment at the window's grain.
export function formatMoment(t, span, zone) {
  const p = dateParts(t, zone);
  if (span <= CLOCK_SPAN_MS) return clock(p);
  if (span <= DATED_SPAN_MS) return `${date(p)} ${clock(p)}`;
  return `${date(p)}, ${p.year}`;
}

// formatSpan names a bucket [start, end) at its own grain: a clock
// range inside a day (dated on windows longer than two days), the day
// itself, or the first and last day it covers.
export function formatSpan(start, end, span, zone) {
  const a = dateParts(start, zone);
  const len = end - start;
  if (len < DAY_MS - 3600 * 1000) {
    const range = `${clock(a)}–${clock(dateParts(end, zone))}`;
    return span > CLOCK_SPAN_MS ? `${date(a)} ${range}` : range;
  }
  if (len <= DAY_MS + 3600 * 1000) return date(a);
  return `${date(a)} – ${date(dateParts(end - 1, zone))}`;
}

// tooltipText is the tooltip's three lines — the bucket's span, its
// events, the state at the sample — and the same said as sentences for
// the live region.
export function tooltipText(m, d, zone = d.zone) {
  const span = d.until - d.since;
  const s = m.sample;
  const moment = formatMoment(m.t, span, zone);
  const state = [
    `${formatCount(s.scope)} scope`,
    `${formatCount(s.done)} done`,
    `${formatCount(s.open)} open`,
    `${formatCount(s.blocked)} blocked`,
  ];
  let when = "";
  let events = [];
  if (m.slice) {
    const b = m.slice;
    when = formatSpan(b.start, b.end, span, zone);
    events = [
      `${formatCount(b.created)} created`,
      `${formatCount(b.claimed)} claimed`,
      `${formatCount(b.done)} done`,
      `${formatCount(b.blocked)} blocked`,
    ];
  }
  const said = `At ${moment}: ${state.join(", ")}.`;
  return {
    when,
    events: events.join(" · "),
    state: `At ${moment} · ${state.join(" · ")}`,
    spoken: m.slice ? `${when}: ${events.join(", ")}. ${said}` : said,
  };
}

// stepBucket moves the keyboard crosshair from trace sample i by one
// histogram bucket (to the sample nearest that bucket's end), or to
// either edge. From no crosshair (-1), any step starts at the right
// edge — the moment the server's labels describe.
export function stepBucket(d, i, key) {
  const last = d.trace.length - 1;
  if (last < 0) return -1;
  if (key === "Home") return 0;
  if (key === "End") return last;
  if (key !== "ArrowLeft" && key !== "ArrowRight") return i;
  if (i < 0) return last;
  const dir = key === "ArrowRight" ? 1 : -1;
  const clampIdx = (k) => Math.max(0, Math.min(last, k));
  if (!d.buckets.length) return clampIdx(i + dir);
  const target = bucketAt(d.buckets, d.trace[i].t) + dir;
  if (target < 0) return 0;
  if (target >= d.buckets.length) return last;
  const next = nearestSample(d.trace, Math.min(d.buckets[target].end, d.until));
  // A bucket narrower than the trace step can snap back to where the
  // crosshair already is; never leave a key press without a move.
  return next === i ? clampIdx(i + dir) : next;
}
