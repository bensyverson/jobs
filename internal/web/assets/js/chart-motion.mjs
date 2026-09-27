/*
  The chart panel's range switch as motion (decision 9 of
  project/2026-09-27-chart-panel-revision.md): when it runs, how long
  it takes, and the frames between the old island and the new one.

  Only a range click slides. A live refresh, a scrubber move and
  back/forward swap instantly — they are the same window a moment
  later, or a jump, not a change of view the eye should follow — and
  prefers-reduced-motion swaps instantly always.

  Pure apart from runSlide, which takes its clock as arguments so a
  test can hold frames back the way a hidden tab does.
*/

// SwapCause is why the panel is swapping in a new fragment.
export const SwapCause = Object.freeze({
  Range: "range",
  Live: "live",
  Scrub: "scrub",
  History: "history",
});

// SwapMode is how the new fragment arrives.
export const SwapMode = Object.freeze({ Animate: "animate", Instant: "instant" });

// WATCHDOG_SLACK_MS is how long past the slide's end the watchdog waits
// for a frame before landing without one. requestAnimationFrame is
// throttled to nothing in a background tab; without a deadline the
// slide would stay mid-flight until the tab came back.
export const WATCHDOG_SLACK_MS = 400;

const drawable = (d) => Boolean(d && Array.isArray(d.trace) && d.trace.length);

// swapMode decides whether a swap slides: a range click between two
// drawn panels, when the reader has not asked for reduced motion.
export function swapMode({ cause, reduced, from, to }) {
  if (cause !== SwapCause.Range || reduced) return SwapMode.Instant;
  return drawable(from) && drawable(to) ? SwapMode.Animate : SwapMode.Instant;
}

// parseDuration reads a CSS time ("320ms", "0.32s") in milliseconds.
export function parseDuration(s, fallback) {
  const m = /^\s*([\d.]+)\s*(ms|s)\s*$/.exec(s || "");
  if (!m) return fallback;
  const v = Number(m[1]) * (m[2] === "s" ? 1000 : 1);
  return Number.isFinite(v) ? Math.round(v) : fallback;
}

const easeOutCubic = (p) => 1 - Math.pow(1 - p, 3);

// cubicBezier is the CSS timing function through (x1, y1) and (x2, y2):
// solve x(t) = p for t, then return y(t).
function cubicBezier(x1, y1, x2, y2) {
  const coord = (a, b, t) => 3 * (1 - t) * (1 - t) * t * a + 3 * (1 - t) * t * t * b + t * t * t;
  const slope = (a, b, t) => 3 * (1 - t) * (1 - t) * a + 6 * (1 - t) * t * (b - a) + 3 * t * t * (1 - b);
  return (p) => {
    if (p <= 0) return 0;
    if (p >= 1) return 1;
    let t = p;
    for (let i = 0; i < 8; i++) {
      const err = coord(x1, x2, t) - p;
      const d = slope(x1, x2, t);
      if (Math.abs(err) < 1e-6) return coord(y1, y2, t);
      if (Math.abs(d) < 1e-6) break;
      t -= err / d;
    }
    let lo = 0;
    let hi = 1;
    t = p;
    for (let i = 0; i < 40; i++) {
      const x = coord(x1, x2, t);
      if (Math.abs(x - p) < 1e-6) break;
      if (x < p) lo = t;
      else hi = t;
      t = (lo + hi) / 2;
    }
    return coord(y1, y2, t);
  };
}

// parseEase reads a motion token's cubic-bezier(); anything else falls
// back to a cubic ease-out.
export function parseEase(s) {
  const m = /cubic-bezier\(\s*([-\d.]+)\s*,\s*([-\d.]+)\s*,\s*([-\d.]+)\s*,\s*([-\d.]+)\s*\)/.exec(s || "");
  if (!m) return easeOutCubic;
  const [x1, y1, x2, y2] = m.slice(1).map(Number);
  if ([x1, y1, x2, y2].some((v) => !Number.isFinite(v))) return easeOutCubic;
  return cubicBezier(x1, y1, x2, y2);
}

// frameAt is the window and the y-domain p of the way from one island
// to the other (p already eased).
export function frameAt(from, to, p) {
  const lerp = (a, b) => a + (b - a) * p;
  return {
    since: lerp(from.since, to.since),
    until: lerp(from.until, to.until),
    lo: lerp(from.y.lo, to.y.lo),
    hi: lerp(from.y.hi, to.y.hi),
  };
}

// mergedTrace is the samples a frame draws: the new island's wherever
// it covers the moment, the old island's outside it. Both are the
// server's samples; the canceled band of an old sample is measured
// from the old window's start, so the band can step at the seam for
// the length of the slide.
export function mergedTrace(from, to) {
  return [
    ...from.trace.filter((s) => s.t < to.since),
    ...to.trace,
    ...from.trace.filter((s) => s.t > to.until),
  ];
}

// frameIsland is one frame as an island burnupPaths can draw. Pass the
// merged trace in to avoid rebuilding it every frame.
export function frameIsland(from, to, p, merged = mergedTrace(from, to)) {
  const f = frameAt(from, to, p);
  return { since: f.since, until: f.until, y: { lo: f.lo, hi: f.hi }, trace: merged };
}

// runSlide calls draw(eased p) on each animation frame for duration
// ms, then land() exactly once — from the last frame, or from the
// watchdog if frames stop coming. It returns a cancel function that
// stops it without landing.
export function runSlide({ duration, ease, draw, land, raf, caf, setTimer, clearTimer, now }) {
  const t0 = now();
  let frame = null;
  let done = false;
  const finish = (andLand) => {
    if (done) return;
    done = true;
    if (frame !== null) caf(frame);
    clearTimer(watchdog);
    if (andLand) land();
  };
  const watchdog = setTimer(() => finish(true), duration + WATCHDOG_SLACK_MS);
  const step = (ts) => {
    if (done) return;
    const p = Math.max(0, Math.min(1, (ts - t0) / duration));
    draw(ease(p));
    if (p < 1) frame = raf(step);
    else finish(true);
  };
  frame = raf(step);
  return () => finish(false);
}
