// Tests for internal/web/assets/js/chart-motion.mjs — the range
// switch's slide: when it runs at all, its timing from the motion
// tokens, and the frames between the old island and the new.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  SwapCause,
  swapMode,
  parseDuration,
  parseEase,
  frameAt,
  mergedTrace,
  frameIsland,
  WATCHDOG_SLACK_MS,
  runSlide,
} from "../assets/js/chart-motion.mjs";

const H = 3600 * 1000;
const U = Date.UTC(2026, 8, 26, 17, 0);

function islandOver(since, until, step, lo, hi) {
  const trace = [];
  for (let t = since; t <= until; t += step) {
    trace.push({ t, scope: 10, done: 5, open: 5, blocked: 0, canceledInWindow: 0 });
  }
  return { since, until, y: { lo, hi }, trace, buckets: [], peak: 0, imports: [], window: { created: 0, done: 0, canceled: 0 } };
}

const week = () => islandOver(U - 7 * 24 * H, U, 6 * H, 0, 100);
const day = () => islandOver(U - 24 * H, U, H, 40, 60);

test("swapMode: a range click slides", () => {
  assert.equal(swapMode({ cause: SwapCause.Range, reduced: false, from: week(), to: day() }), "animate");
});

test("swapMode: reduced motion swaps instantly", () => {
  assert.equal(swapMode({ cause: SwapCause.Range, reduced: true, from: week(), to: day() }), "instant");
});

test("swapMode: live refreshes, scrubber moves and history swap instantly", () => {
  for (const cause of [SwapCause.Live, SwapCause.Scrub, SwapCause.History]) {
    assert.equal(swapMode({ cause, reduced: false, from: week(), to: day() }), "instant", cause);
  }
});

test("swapMode: without both islands (an empty or error face) there is nothing to slide", () => {
  assert.equal(swapMode({ cause: SwapCause.Range, reduced: false, from: null, to: day() }), "instant");
  assert.equal(swapMode({ cause: SwapCause.Range, reduced: false, from: week(), to: null }), "instant");
  const bare = week();
  bare.trace = [];
  assert.equal(swapMode({ cause: SwapCause.Range, reduced: false, from: bare, to: day() }), "instant");
});

test("parseDuration reads a motion token in ms or s, with a fallback", () => {
  assert.equal(parseDuration("320ms", 1), 320);
  assert.equal(parseDuration(" 0.32s ", 1), 320);
  assert.equal(parseDuration("", 450), 450);
  assert.equal(parseDuration("fast", 450), 450);
});

test("parseEase reads the ease-out token as a cubic-bezier", () => {
  const ease = parseEase("cubic-bezier(0.2, 0.8, 0.2, 1)");
  assert.equal(ease(0), 0);
  assert.equal(ease(1), 1);
  // An ease-out is well past half way at the half-way time.
  assert.ok(ease(0.5) > 0.8, `ease(0.5) = ${ease(0.5)}`);
  // Monotonic.
  let prev = 0;
  for (let p = 0.05; p <= 1; p += 0.05) {
    const v = ease(p);
    assert.ok(v >= prev);
    prev = v;
  }
});

test("parseEase falls back to a cubic ease-out when the token is unreadable", () => {
  const ease = parseEase("");
  assert.equal(ease(0), 0);
  assert.equal(ease(1), 1);
  assert.equal(ease(0.5), 0.875);
});

test("frameAt interpolates the window and the y-domain", () => {
  const from = week();
  const to = day();
  assert.deepEqual(frameAt(from, to, 0), { since: from.since, until: from.until, lo: 0, hi: 100 });
  assert.deepEqual(frameAt(from, to, 1), { since: to.since, until: to.until, lo: 40, hi: 60 });
  const mid = frameAt(from, to, 0.5);
  assert.equal(mid.since, (from.since + to.since) / 2);
  assert.equal(mid.lo, 20);
  assert.equal(mid.hi, 80);
});

test("mergedTrace draws each moment from the island that covers it, the new one first", () => {
  const from = week();
  const to = day();
  const m = mergedTrace(from, to);
  // Every new sample, plus the old ones before the new window.
  const older = from.trace.filter((s) => s.t < to.since);
  assert.equal(m.length, to.trace.length + older.length);
  for (let i = 1; i < m.length; i++) assert.ok(m[i].t > m[i - 1].t, "sorted, no duplicates");
  assert.equal(m[m.length - 1], to.trace[to.trace.length - 1]);
});

test("mergedTrace widening: the new island covers everything", () => {
  const m = mergedTrace(day(), week());
  assert.equal(m.length, week().trace.length);
});

test("frameIsland is an island burnupPaths can draw", () => {
  const f = frameIsland(week(), day(), 0.5);
  assert.equal(typeof f.since, "number");
  assert.deepEqual(Object.keys(f.y).sort(), ["hi", "lo"]);
  assert.ok(Array.isArray(f.trace) && f.trace.length > 0);
});

test("the watchdog leaves slack past the slide", () => {
  assert.ok(WATCHDOG_SLACK_MS >= 200);
});

// A fake clock for runSlide: frames run when the test says, timers when
// it advances.
function fakeClock() {
  let now = 0;
  let frames = [];
  const timers = [];
  return {
    now: () => now,
    raf: (fn) => {
      frames.push(fn);
      return frames.length;
    },
    caf: () => {
      frames = [];
    },
    setTimer: (fn, ms) => {
      const t = { fn, at: now + ms, live: true };
      timers.push(t);
      return t;
    },
    clearTimer: (t) => {
      if (t) t.live = false;
    },
    frame(at) {
      now = at;
      const run = frames;
      frames = [];
      for (const fn of run) fn(at);
    },
    advance(ms) {
      now += ms;
      for (const t of timers) if (t.live && t.at <= now) { t.live = false; t.fn(); }
    },
  };
}

test("runSlide draws eased frames and lands once at the end", () => {
  const c = fakeClock();
  const drawn = [];
  let landed = 0;
  runSlide({ duration: 100, ease: (p) => p, draw: (p) => drawn.push(p), land: () => landed++, ...c });
  c.frame(0);
  c.frame(50);
  c.frame(100);
  assert.deepEqual(drawn, [0, 0.5, 1]);
  assert.equal(landed, 1);
  c.advance(1000); // the watchdog was cleared
  assert.equal(landed, 1);
});

test("runSlide lands from the watchdog when frames never come (a hidden tab)", () => {
  const c = fakeClock();
  let landed = 0;
  runSlide({ duration: 100, ease: (p) => p, draw: () => {}, land: () => landed++, ...c });
  c.advance(100 + WATCHDOG_SLACK_MS);
  assert.equal(landed, 1);
  c.frame(200); // a late frame after landing does nothing more
  assert.equal(landed, 1);
});

test("runSlide's cancel stops it without landing", () => {
  const c = fakeClock();
  let landed = 0;
  const cancel = runSlide({ duration: 100, ease: (p) => p, draw: () => {}, land: () => landed++, ...c });
  c.frame(0);
  cancel();
  c.frame(100);
  c.advance(1000);
  assert.equal(landed, 0);
});
