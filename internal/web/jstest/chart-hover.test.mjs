// Tests for internal/web/assets/js/chart-hover.mjs — what the chart
// panel's crosshair shows at a moment: the nearest trace sample, the
// histogram bucket it falls in, the window's figures up to it, and the
// tooltip's words. Everything is read off the island; nothing is
// re-counted (reporting decision 12).

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  nearestSample,
  momentAt,
  bucketAt,
  cumulativeThrough,
  formatCount,
  hoverModel,
  tooltipText,
  formatMoment,
  formatSpan,
  stepBucket,
} from "../assets/js/chart-hover.mjs";

const H = 3600 * 1000;
const T0 = Date.UTC(2026, 8, 26, 0, 0); // Sep 26 2026 00:00 UTC

// A four-hour window: trace every 30 minutes, hourly buckets whose
// created/done sum to the window's figures (decision 10).
function island() {
  const trace = [];
  for (let i = 0; i <= 8; i++) {
    trace.push({
      t: T0 + (i * H) / 2,
      scope: 20 + i,
      done: 10 + i,
      open: 10,
      blocked: i % 3,
      canceledInWindow: i >= 6 ? 2 : 0,
    });
  }
  const buckets = [
    { start: T0, end: T0 + H, created: 2, claimed: 1, done: 2, blocked: 0 },
    { start: T0 + H, end: T0 + 2 * H, created: 2, claimed: 3, done: 2, blocked: 1 },
    { start: T0 + 2 * H, end: T0 + 3 * H, created: 3, claimed: 0, done: 2, blocked: 0 },
    { start: T0 + 3 * H, end: T0 + 4 * H, created: 3, claimed: 2, done: 2, blocked: 2 },
  ];
  return {
    since: T0,
    until: T0 + 4 * H,
    y: { lo: 10, hi: 30 },
    trace,
    buckets,
    peak: 8,
    imports: [],
    window: { created: 10, done: 8, canceled: 2 },
  };
}

test("nearestSample snaps to the closest sample, not the one before", () => {
  const { trace } = island();
  assert.equal(nearestSample(trace, T0), 0);
  assert.equal(nearestSample(trace, T0 + 0.2 * H), 0);
  assert.equal(nearestSample(trace, T0 + 0.3 * H), 1);
  assert.equal(nearestSample(trace, T0 + 4 * H), 8);
});

test("nearestSample clamps moments outside the trace to its ends", () => {
  const { trace } = island();
  assert.equal(nearestSample(trace, T0 - H), 0);
  assert.equal(nearestSample(trace, T0 + 9 * H), 8);
});

test("nearestSample of an empty trace is -1", () => {
  assert.equal(nearestSample([], T0), -1);
});

test("momentAt maps a fraction of the plot onto the window, clamped", () => {
  const d = island();
  assert.equal(momentAt(d, 0), T0);
  assert.equal(momentAt(d, 1), T0 + 4 * H);
  assert.equal(momentAt(d, 0.5), T0 + 2 * H);
  assert.equal(momentAt(d, -0.2), T0);
  assert.equal(momentAt(d, 1.3), T0 + 4 * H);
});

test("bucketAt is the bucket whose events the state at t has seen last", () => {
  const { buckets } = island();
  // A moment inside a bucket belongs to it.
  assert.equal(bucketAt(buckets, T0 + 1.5 * H), 1);
  // A bucket boundary belongs to the bucket that ends there.
  assert.equal(bucketAt(buckets, T0 + 2 * H), 1);
  // Since belongs to the first bucket; Until to the last.
  assert.equal(bucketAt(buckets, T0), 0);
  assert.equal(bucketAt(buckets, T0 + 4 * H), 3);
  assert.equal(bucketAt([], T0), -1);
});

test("cumulativeThrough sums each kind through the bucket", () => {
  const { buckets } = island();
  assert.deepEqual(cumulativeThrough(buckets, 1), { created: 4, claimed: 4, done: 4, blocked: 1 });
  assert.deepEqual(cumulativeThrough(buckets, -1), { created: 0, claimed: 0, done: 0, blocked: 0 });
});

test("cumulativeThrough the last bucket is the window's figures (decision 10)", () => {
  const d = island();
  const all = cumulativeThrough(d.buckets, d.buckets.length - 1);
  assert.equal(all.created, d.window.created);
  assert.equal(all.done, d.window.done);
});

test("formatCount takes thousands separators, like chart.Count", () => {
  assert.equal(formatCount(0), "0");
  assert.equal(formatCount(999), "999");
  assert.equal(formatCount(1155), "1,155");
  assert.equal(formatCount(1234567), "1,234,567");
});

test("hoverModel at the right edge reads the server's end labels", () => {
  const d = island();
  const m = hoverModel(d, 8);
  assert.equal(m.created, "+10");
  assert.equal(m.done, "+8");
  // created sits on the band's top: scope 28 + 2 canceled in the window.
  assert.equal(m.createdTotal, "of 30");
  assert.equal(m.doneTotal, "of 18");
  assert.equal(m.x, "100%");
});

test("hoverModel mid-window reads the figures up to that moment", () => {
  const d = island();
  const m = hoverModel(d, 3); // 01:30, inside the second bucket
  assert.equal(m.bucket, 1);
  assert.equal(m.created, "+4");
  assert.equal(m.done, "+4");
  assert.equal(m.createdTotal, "of 23");
  assert.equal(m.doneTotal, "of 13");
  assert.deepEqual(m.legend, { created: "4", claimed: "4", done: "4", blocked: "1" });
  assert.equal(m.x, "37.5%");
  assert.equal(m.activityX, "375");
  assert.equal(m.t, T0 + 1.5 * H);
});

test("hoverModel carries the dots and end labels for its sample", () => {
  const d = island();
  const m = hoverModel(d, 0);
  assert.equal(m.markers.scopeDot.x, "0%");
  // scope 20 on a 10..30 domain is half way up.
  assert.equal(m.markers.scopeDot.y, "50%");
});

test("tooltipText names the bucket, its events and the state at the sample", () => {
  const d = island();
  const tip = tooltipText(hoverModel(d, 3), d, "UTC");
  assert.equal(tip.when, "01:00–02:00");
  assert.equal(tip.events, "2 created · 3 claimed · 2 done · 1 blocked");
  assert.equal(tip.state, "At 01:30 · 23 scope · 13 done · 10 open · 0 blocked");
  assert.equal(tip.spoken, "01:00–02:00: 2 created, 3 claimed, 2 done, 1 blocked. At 01:30: 23 scope, 13 done, 10 open, 0 blocked.");
});

test("tooltipText reads times in the island's zone, as the axis does", () => {
  const d = island();
  d.zone = "UTC";
  assert.equal(tooltipText(hoverModel(d, 3), d).when, "01:00–02:00");
  d.zone = "Asia/Tokyo"; // UTC+9, no DST
  assert.equal(tooltipText(hoverModel(d, 3), d).when, "10:00–11:00");
});

test("an unknown zone falls back to the browser's rather than throwing", () => {
  const d = island();
  assert.doesNotThrow(() => tooltipText(hoverModel(d, 3), d, "Not/AZone"));
  assert.doesNotThrow(() => formatMoment(T0, H, "Not/AZone"));
});

test("tooltipText without buckets still names the state", () => {
  const d = island();
  d.buckets = [];
  const tip = tooltipText(hoverModel(d, 2), d, "UTC");
  assert.equal(tip.when, "");
  assert.equal(tip.events, "");
  assert.equal(tip.state, "At 01:00 · 22 scope · 12 done · 10 open · 2 blocked");
  assert.equal(tip.spoken, "At 01:00: 22 scope, 12 done, 10 open, 2 blocked.");
});

test("formatMoment reads clock time on short windows and dates on long ones", () => {
  const t = Date.UTC(2026, 8, 25, 14, 5);
  assert.equal(formatMoment(t, 24 * H, "UTC"), "14:05");
  assert.equal(formatMoment(t, 7 * 24 * H, "UTC"), "Sep 25 14:05");
  assert.equal(formatMoment(t, 400 * 24 * H, "UTC"), "Sep 25, 2026");
});

test("formatSpan reads a bucket's span at its grain", () => {
  const s = Date.UTC(2026, 8, 25, 14, 0);
  assert.equal(formatSpan(s, s + H, 24 * H, "UTC"), "14:00–15:00");
  assert.equal(formatSpan(s, s + 6 * H, 7 * 24 * H, "UTC"), "Sep 25 14:00–20:00");
  const day = Date.UTC(2026, 8, 25);
  assert.equal(formatSpan(day, day + 24 * H, 30 * 24 * H, "UTC"), "Sep 25");
  assert.equal(formatSpan(day, day + 7 * 24 * H, 150 * 24 * H, "UTC"), "Sep 25 – Oct 1");
});

test("stepBucket moves the keyboard crosshair one bucket and clamps", () => {
  const d = island();
  // From nothing, a step lands on the last bucket's end (the right edge).
  assert.equal(stepBucket(d, -1, "ArrowLeft"), 8);
  assert.equal(stepBucket(d, 8, "ArrowLeft"), 6); // 03:00, the third bucket's end
  assert.equal(stepBucket(d, 6, "ArrowRight"), 8);
  assert.equal(stepBucket(d, 8, "ArrowRight"), 8);
  assert.equal(stepBucket(d, 5, "Home"), 0);
  assert.equal(stepBucket(d, 2, "End"), 8);
  assert.equal(stepBucket(d, 2, "x"), 2);
});
