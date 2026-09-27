/*
  The burn-up's geometry, in the browser: the time and value scales and
  the path builders of internal/web/chart (chart.go, burnup.go), so the
  chart panel's script can redraw the burn-up mid-slide and place the
  hover's dots and end labels exactly where the server would.

  This file is written twice and allowed to drift zero times. The Go
  test TestBurnupGeometryFixture writes internal/web/jstest/fixtures/
  burnup-geometry.json — islands and the paths LayoutBurnup drew for
  them — and fails when that file is stale; chart-geometry.test.mjs
  fails when these builders stop reproducing it byte for byte. Change
  one side and the other suite says so.

  Pure: no DOM, no clock, no locale. Input is the panel's island
  (chart.PanelData); times are Unix milliseconds.
*/

// VIEW_W and VIEW_H are the burn-up viewBox (chart.ViewW, chart.BurnupViewH).
export const VIEW_W = 1000;
export const VIEW_H = 1000;

// The end-label layout constants (burnup.go): the least gap between the
// two label blocks and the band both keep inside, in percent of the plot.
const MIN_END_LABEL_GAP_PCT = 40;
const END_LABEL_TOP_PCT = 6;
const END_LABEL_BOTTOM_PCT = 74;

// fmtNum formats a viewBox coordinate as Go's fmtNum does: two decimals
// at most, trailing zeros dropped, never "-0". Go's strconv rounds an
// exact half-way value to even where toFixed rounds it up, so those
// (the only ones: an odd multiple of 1/8) are rounded here by hand.
export function fmtNum(f) {
  const eighths = f * 8; // exact: a power-of-two multiple
  let s;
  if (Number.isInteger(eighths) && eighths % 2 !== 0) {
    let n = Math.floor(eighths * 12.5); // f × 100 is n + 0.5 exactly
    if (n % 2 !== 0) n += 1;
    const a = Math.abs(n);
    s = (n < 0 ? "-" : "") + Math.floor(a / 100) + "." + String(a % 100).padStart(2, "0");
  } else {
    s = f.toFixed(2);
  }
  s = s.replace(/0+$/, "").replace(/\.$/, "");
  return s === "-0" ? "0" : s;
}

// fmtPct formats a 0..100 value as an SVG percentage attribute.
export function fmtPct(p) {
  return fmtNum(p) + "%";
}

// seconds converts a millisecond duration the way Go's
// Duration.Seconds does — whole seconds plus the remainder over 1e9 —
// so the float operations, and so the result, are Go's.
function seconds(ms) {
  const whole = Math.trunc(ms / 1000);
  const nsec = (ms - whole * 1000) * 1e6;
  return whole + nsec / 1e9;
}

// xOf is t's position on the since → until axis in viewBox units
// (chart.timeScale.x). A window of no length has a span of one second.
export function xOf(t, since, until) {
  let span = seconds(until - since);
  if (span <= 0) span = 1;
  return (seconds(t - since) / span) * VIEW_W;
}

// yOf maps a count onto the viewBox: lo on the baseline, hi at the top
// (Burnup.yf).
export function yOf(v, lo, hi) {
  return VIEW_H - ((v - lo) / (hi - lo)) * VIEW_H;
}

function linePath(xs, ys) {
  let out = "";
  for (let i = 0; i < xs.length; i++) {
    out += (i === 0 ? "M" : "L") + fmtNum(xs[i]) + " " + fmtNum(ys[i]);
  }
  if (xs.length === 1) out += "L" + fmtNum(xs[0]) + " " + fmtNum(ys[0]);
  return out;
}

function areaPath(xs, upper, lower) {
  let out = "";
  for (let i = 0; i < xs.length; i++) {
    out += (i === 0 ? "M" : "L") + fmtNum(xs[i]) + " " + fmtNum(upper[i]);
  }
  for (let i = xs.length - 1; i >= 0; i--) {
    out += "L" + fmtNum(xs[i]) + " " + fmtNum(lower[i]);
  }
  return out + "Z";
}

// bandTop is the canceled band's upper edge: scope plus what the window
// has canceled so far.
function bandTop(s) {
  return s.scope + s.canceledInWindow;
}

// blockedTop is the blocked share's upper edge, stacked on done and
// never above scope.
function blockedTop(s) {
  return Math.min(s.done + s.blocked, s.scope);
}

// burnupPaths draws the island's trace on its window and y-domain: the
// same five paths LayoutBurnup draws. canceled is "" when the window
// canceled nothing, as the server leaves it out.
export function burnupPaths(d) {
  const { since, until, trace } = d;
  const { lo, hi } = d.y;
  if (!trace.length) return { scope: "", done: "", gap: "", blocked: "", canceled: "" };
  const xs = [];
  const scope = [];
  const done = [];
  const blocked = [];
  const band = [];
  let anyCanceled = false;
  for (const s of trace) {
    xs.push(xOf(s.t, since, until));
    scope.push(yOf(s.scope, lo, hi));
    done.push(yOf(s.done, lo, hi));
    blocked.push(yOf(blockedTop(s), lo, hi));
    band.push(yOf(bandTop(s), lo, hi));
    anyCanceled = anyCanceled || s.canceledInWindow > 0;
  }
  return {
    scope: linePath(xs, scope),
    done: linePath(xs, done),
    gap: areaPath(xs, scope, done),
    blocked: areaPath(xs, blocked, done),
    canceled: anyCanceled ? areaPath(xs, band, scope) : "",
  };
}

// endLabelPositions nudges the two end labels apart around their
// midpoint when they sit closer than the least gap, then keeps both
// inside the plot (burnup.go's endLabelPositions). Created's label is
// the upper one: scope is never below done.
export function endLabelPositions(createdY, doneY) {
  let c = createdY;
  let d = doneY;
  if (d - c < MIN_END_LABEL_GAP_PCT) {
    const mid = (c + d) / 2;
    c = mid - MIN_END_LABEL_GAP_PCT / 2;
    d = mid + MIN_END_LABEL_GAP_PCT / 2;
  }
  if (c < END_LABEL_TOP_PCT) {
    d += END_LABEL_TOP_PCT - c;
    c = END_LABEL_TOP_PCT;
  }
  if (d > END_LABEL_BOTTOM_PCT) {
    c -= d - END_LABEL_BOTTOM_PCT;
    d = END_LABEL_BOTTOM_PCT;
  }
  const clamp = (v) => Math.max(END_LABEL_TOP_PCT, Math.min(END_LABEL_BOTTOM_PCT, v));
  return [clamp(c), clamp(d)];
}

// markersAt places the two dots on sample i and the end labels beside
// them, in percent of the plot — at the last sample, exactly the
// server's ScopeDot, DoneDot, Created.Y and Done.Y.
export function markersAt(d, i) {
  const s = d.trace[i];
  const { lo, hi } = d.y;
  const x = fmtPct((xOf(s.t, d.since, d.until) / VIEW_W) * 100);
  const [createdY, doneY] = endLabelPositions(
    (yOf(bandTop(s), lo, hi) / VIEW_H) * 100,
    (yOf(s.done, lo, hi) / VIEW_H) * 100,
  );
  return {
    scopeDot: { x, y: fmtPct((yOf(s.scope, lo, hi) / VIEW_H) * 100) },
    doneDot: { x, y: fmtPct((yOf(s.done, lo, hi) / VIEW_H) * 100) },
    createdY: fmtPct(createdY),
    doneY: fmtPct(doneY),
  };
}
