// Tests for internal/web/assets/js/scrubber-cursor.mjs.
//
// Pure math the scrubber UI needs: convert between cursor x-fraction
// (0..1 across the strip) and log position, bin events into density
// buckets for the histogram bars, format the history banner text.
// All side-effect-free; the DOM-binding layer lives in
// scrubber-pill.mjs and isn't covered here.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  xToPosition,
  positionToX,
  computeDensityBars,
  formatHistoryBannerText,
  formatAge,
  parseAtFromQuery,
  composeURLWithAt,
  composeURLWithoutAt,
  panWindowStartMs,
  zoomWindow,
  formatAxisLabels,
  clampWindowEndToNow,
  clampWindowStartToFloor,
  classifyWheelAxis,
  windowForPosition,
} from "../assets/js/scrubber-cursor.mjs";

// Helper: build an events array with a fixed offset back from `now`.
// Each entry's created_at is an integer second; the array is sorted
// ascending by created_at, matching how the server returns events.
function eventsAt(now, ageSecondsList) {
  return ageSecondsList.map((ageSec, i) => ({
    id: i + 1,
    position: POS(i + 1),
    created_at: Math.floor((now - ageSec * 1000) / 1000),
  }));
}

// POS encodes a synthetic cursor for sequence number n.
function POS(n) {
  return `1000-aaaaaa-${n}`;
}

const NOW = 1735000000_000; // arbitrary fixed millis-since-epoch

// --- xToPosition ---

test("xToPosition: empty events returns null", () => {
  assert.equal(xToPosition(0.5, [], NOW), null);
});

test("xToPosition: x=1 returns the most recent event's cursor", () => {
  // Three events at 23h, 12h, 1h ago.
  const events = eventsAt(NOW, [23 * 3600, 12 * 3600, 1 * 3600]);
  assert.equal(xToPosition(1, events, NOW), POS(3));
});

test("xToPosition: x=0 returns the oldest event in the window (or before)", () => {
  // Three events at 23h, 12h, 1h ago. x=0 means "24h ago" — first
  // event is the closest match.
  const events = eventsAt(NOW, [23 * 3600, 12 * 3600, 1 * 3600]);
  assert.equal(xToPosition(0, events, NOW), POS(1));
});

test("xToPosition: midpoint resolves to the event just before that time", () => {
  // 24h window: x=0.5 means "12h ago". With events at 23h, 12h, 1h,
  // the largest event id with created_at <= (now - 12h) is event 2.
  const events = eventsAt(NOW, [23 * 3600, 12 * 3600, 1 * 3600]);
  assert.equal(xToPosition(0.5, events, NOW), POS(2));
});

test("xToPosition: x past the most-recent event clamps to head", () => {
  const events = eventsAt(NOW, [10 * 3600, 5 * 3600]);
  assert.equal(xToPosition(1, events, NOW), POS(2));
});

// --- positionToX ---

test("positionToX: event at now returns 1", () => {
  const events = eventsAt(NOW, [0]);
  assert.equal(positionToX(POS(1), events, NOW), 1);
});

test("positionToX: event 24h+ ago returns 0", () => {
  const events = eventsAt(NOW, [25 * 3600]);
  assert.equal(positionToX(POS(1), events, NOW), 0);
});

test("positionToX: event mid-window returns mid-x", () => {
  const events = eventsAt(NOW, [12 * 3600]);
  // 12h ago in a 24h window = age 12h / 24h = 0.5; x = 1 - 0.5 = 0.5.
  assert.equal(positionToX(POS(1), events, NOW), 0.5);
});

test("positionToX: an unknown cursor returns 1 (clamps to head)", () => {
  const events = eventsAt(NOW, [1 * 3600]);
  assert.equal(positionToX(POS(999), events, NOW), 1);
});

// --- computeDensityBars ---

test("computeDensityBars: empty events returns all-zero bars", () => {
  const bars = computeDensityBars([], NOW, 60);
  assert.equal(bars.length, 60);
  for (const v of bars) assert.equal(v, 0);
});

test("computeDensityBars: all events in last bucket peak that bucket", () => {
  // Three events all within the last minute. With 60 buckets over
  // 24h, that's the last bucket (index 59).
  const events = eventsAt(NOW, [10, 20, 30]); // seconds ago
  const bars = computeDensityBars(events, NOW, 60);
  assert.equal(bars[59], 100, "last bucket should peak at 100%");
  for (let i = 0; i < 59; i++) {
    assert.equal(bars[i], 0, `bucket ${i} should be 0`);
  }
});

test("computeDensityBars: events outside the 24h window are excluded", () => {
  const events = eventsAt(NOW, [25 * 3600, 30 * 3600, 1 * 3600]);
  const bars = computeDensityBars(events, NOW, 24);
  // Only the 1h-ago event lands in a bucket.
  const total = bars.reduce((a, b) => a + (b > 0 ? 1 : 0), 0);
  assert.equal(total, 1, "exactly one non-empty bucket expected");
});

// --- formatAge / formatHistoryBannerText ---

test("formatAge: covers seconds, minutes, hours, days", () => {
  assert.equal(formatAge(45_000), "45s ago");
  assert.equal(formatAge(5 * 60_000), "5m ago");
  assert.equal(formatAge(3 * 3600_000), "3h ago");
  assert.equal(formatAge(2 * 86400_000), "2d ago");
});

test("formatHistoryBannerText: composes cursor, age, and ISO timestamp", () => {
  const event = { id: 1234, position: POS(1234), created_at: Math.floor((NOW - 6 * 3600_000) / 1000) };
  const text = formatHistoryBannerText(event, NOW);
  assert.match(text, /\?at=1000-aaaaaa-1234/);
  assert.match(text, /6h ago/);
  assert.match(text, /\d{4}-\d{2}-\d{2}/);
});

// --- URL helpers ---

test("parseAtFromQuery: a log position comes back verbatim", () => {
  assert.equal(parseAtFromQuery("?at=" + POS(42)), POS(42));
  assert.equal(parseAtFromQuery("?at=1756742400000--991"), "1756742400000--991");
});

test("parseAtFromQuery: anything that is not a position returns null", () => {
  assert.equal(parseAtFromQuery(""), null);
  assert.equal(parseAtFromQuery("?other=42"), null);
  assert.equal(parseAtFromQuery("?at="), null);
  assert.equal(parseAtFromQuery("?at=foo"), null);
  assert.equal(parseAtFromQuery("?at=0"), null);
  assert.equal(parseAtFromQuery("?at=-1"), null);
  assert.equal(parseAtFromQuery("?at=1.5"), null);
  // A bare row id is not a cursor — a rebuild renumbers those, and the
  // server rejects it too.
  assert.equal(parseAtFromQuery("?at=42"), null);
});

test("parseAtFromQuery: composes with other query params", () => {
  assert.equal(parseAtFromQuery("?actor=alice&at=" + POS(99) + "&type=done"), POS(99));
});

test("composeURLWithAt: sets ?at on a clean URL", () => {
  const got = composeURLWithAt("/plan", 42);
  assert.equal(got, "/plan?at=42");
});

test("composeURLWithAt: replaces an existing ?at, preserves others", () => {
  const got = composeURLWithAt("/log?actor=alice&at=10&type=done", 42);
  // URLSearchParams ordering: existing keys are preserved, replaced
  // value updates in place. New keys append.
  assert.equal(got, "/log?actor=alice&at=42&type=done");
});

test("composeURLWithoutAt: removes ?at, preserves others", () => {
  const got = composeURLWithoutAt("/log?actor=alice&at=10&type=done");
  assert.equal(got, "/log?actor=alice&type=done");
});

test("composeURLWithoutAt: returns clean path when ?at was the only param", () => {
  const got = composeURLWithoutAt("/plan?at=42");
  assert.equal(got, "/plan");
});

test("composeURL helpers preserve hash fragments", () => {
  assert.equal(composeURLWithAt("/log#bottom", 42), "/log?at=42#bottom");
  assert.equal(composeURLWithoutAt("/log?at=42#bottom"), "/log#bottom");
});

// --- windowStartMs override (pan support) ---

test("xToPosition: explicit windowStartMs frames the visible range independently of nowMs", () => {
  // Window starts 48h ago and lasts 1h: only events in that hour are
  // navigable. Events outside the window clamp to its closest edge.
  const events = eventsAt(NOW, [49 * 3600, 47.5 * 3600, 1 * 3600]);
  const windowMs = 60 * 60 * 1000;
  const windowStartMs = NOW - 48 * 60 * 60 * 1000;
  // Mid-window points to the only event inside (at 47.5h ago).
  assert.equal(xToPosition(0.5, events, NOW, windowMs, windowStartMs), POS(2));
});

test("positionToX: explicit windowStartMs projects relative to the panned window", () => {
  const events = eventsAt(NOW, [12 * 3600]);
  // Window starts 13h ago and lasts 2h: the event sits at the +1h
  // mark, i.e. midpoint of the visible window.
  const windowMs = 2 * 60 * 60 * 1000;
  const windowStartMs = NOW - 13 * 60 * 60 * 1000;
  assert.equal(positionToX(POS(1), events, NOW, windowMs, windowStartMs), 0.5);
});

test("computeDensityBars: explicit windowStartMs only counts events inside the panned window", () => {
  const events = eventsAt(NOW, [25 * 3600, 26 * 3600, 1 * 3600]);
  // Window: [27h ago, 24h ago] — should pick up the two older events
  // and exclude the recent one.
  const windowMs = 3 * 60 * 60 * 1000;
  const windowStartMs = NOW - 27 * 60 * 60 * 1000;
  const bars = computeDensityBars(events, NOW, 4, windowMs, windowStartMs);
  const total = bars.reduce((a, b) => a + (b > 0 ? 1 : 0), 0);
  assert.ok(total >= 1, "at least one bucket should be non-zero with two events in the panned window");
});

// --- panWindowStartMs ---

test("panWindowStartMs: dragging right shifts windowStart earlier (older content into view)", () => {
  // 100px drag right on a 1000px track viewing a 24h window =
  // 0.1 * 24h = 2.4h shift earlier.
  const windowMs = 24 * 3600 * 1000;
  const before = 1000;
  const after = panWindowStartMs(before, 100, 1000, windowMs);
  assert.ok(before > after, "drag right should shift start earlier");
  assert.ok(Math.abs((before - after) - 0.1 * windowMs) < 1, "magnitude is 10% of windowMs");
});

test("panWindowStartMs: dragging left shifts windowStart later (newer content into view)", () => {
  const windowMs = 60 * 60 * 1000;
  const before = 1000;
  const after = panWindowStartMs(before, -50, 1000, windowMs);
  assert.ok(after > before, "drag left should shift start later");
  assert.ok(Math.abs((after - before) - 0.05 * windowMs) < 1, "magnitude is 5% of windowMs");
});

test("panWindowStartMs: trackWidthPx <= 0 returns the input unchanged", () => {
  // Detaching the track or before-layout reads return width 0; pan
  // should be a no-op rather than NaN.
  assert.equal(panWindowStartMs(1000, 50, 0, 1000), 1000);
  assert.equal(panWindowStartMs(1000, 50, -1, 1000), 1000);
});

// --- zoomWindow ---

test("zoomWindow: anchored at 0.5 keeps the midpoint time fixed", () => {
  const start = 0;
  const win = 60 * 60 * 1000; // 1h
  // The midpoint time before zoom is start + 0.5 * win.
  const midpointBefore = start + 0.5 * win;
  const { windowStartMs, windowMs } = zoomWindow(start, win, 0.5, 0.5);
  assert.equal(windowMs, win * 0.5);
  // Midpoint of the new window: windowStart + 0.5 * windowMs.
  assert.equal(windowStartMs + 0.5 * windowMs, midpointBefore);
});

test("zoomWindow: anchored at 1.0 keeps the right edge time fixed", () => {
  // Scrolling on the rightmost edge ("now") should keep "now" pinned.
  const start = 0;
  const win = 60 * 60 * 1000; // 1h — well inside the clamp range
  const rightEdgeBefore = start + win;
  const { windowStartMs, windowMs } = zoomWindow(start, win, 1, 0.5);
  assert.equal(windowStartMs + windowMs, rightEdgeBefore);
});

test("zoomWindow: clamps below MIN_WINDOW_MS (1 minute)", () => {
  // 1ms × 0.1 = 0.1ms — way under floor; should clamp to 60_000.
  const { windowMs } = zoomWindow(0, 1, 0.5, 0.1);
  assert.equal(windowMs, 60 * 1000);
});

test("zoomWindow: clamps above MAX_WINDOW_MS (10 years)", () => {
  // The hard cap is just a NaN/infinity guard — the real "you can't
  // zoom further" stop is the history-floor clamp in the UI. This test
  // pins the cap value so a regression to a tighter ceiling (30d, 1y)
  // gets caught.
  const TEN_YEARS = 10 * 365 * 24 * 60 * 60 * 1000;
  const TWENTY_YEARS = 2 * TEN_YEARS;
  const { windowMs } = zoomWindow(0, TWENTY_YEARS, 0.5, 1.1);
  assert.equal(windowMs, TEN_YEARS);
});

test("zoomWindow: degenerate factor falls back to current window", () => {
  // factor = 0 or NaN would otherwise zero / NaN the window.
  const win = 60 * 60 * 1000; // 1 hour — comfortably inside the clamp range
  assert.equal(zoomWindow(0, win, 0.5, 0).windowMs, win);
  assert.equal(zoomWindow(0, win, 0.5, NaN).windowMs, win);
});

// --- formatAxisLabels ---

test("formatAxisLabels: trailing 24h window emits day/hour markers ending at 'now'", () => {
  // formatAge promotes 24h to "1d" (and 12h..6h stay in hours), so
  // the trailing 24h labels read "1d / 18h / 12h / 6h / now".
  // The exact labels are a function of formatAge's thresholds; this
  // test pins the contract that the rightmost label is "now".
  const win = 24 * 60 * 60 * 1000;
  const labels = formatAxisLabels(NOW - win, win, NOW);
  assert.deepStrictEqual(labels, ["1d", "18h", "12h", "6h", "now"]);
});

test("formatAxisLabels: gridlines past `now` clamp to 'now'", () => {
  // Window extends 1h into the future (future-dated panning) — every
  // gridline beyond nowMs should read 'now', not negative ages.
  const win = 2 * 60 * 60 * 1000;
  const labels = formatAxisLabels(NOW - 60 * 60 * 1000, win, NOW);
  // x=0.5 corresponds to nowMs; x>0.5 are 'now'.
  assert.equal(labels[2], "now");
  assert.equal(labels[3], "now");
  assert.equal(labels[4], "now");
});

// --- clampWindowEndToNow ---

test("clampWindowEndToNow: leaves a fully-past window untouched", () => {
  const start = NOW - 24 * 3600 * 1000;
  const win = 12 * 3600 * 1000;
  const out = clampWindowEndToNow(start, win, NOW);
  assert.equal(out.windowStartMs, start);
  assert.equal(out.windowMs, win);
});

test("clampWindowEndToNow: shifts a future-overhanging window so the right edge hits now", () => {
  // Window extends 1h past now; clamp should pull the start back by 1h
  // (windowMs unchanged — clamping is a translation, not a resize).
  const win = 2 * 3600 * 1000;
  const start = NOW - 1 * 3600 * 1000; // ends NOW + 1h
  const out = clampWindowEndToNow(start, win, NOW);
  assert.equal(out.windowMs, win);
  assert.equal(out.windowStartMs + out.windowMs, NOW);
});

test("clampWindowEndToNow: a window wholly in the future snaps to end-at-now", () => {
  // Pan/zoom can drag a window completely past now; the contract is
  // that the right edge always pins to now, never beyond.
  const win = 60 * 60 * 1000;
  const start = NOW + 5 * 3600 * 1000;
  const out = clampWindowEndToNow(start, win, NOW);
  assert.equal(out.windowStartMs, NOW - win);
  assert.equal(out.windowMs, win);
});

// --- clampWindowStartToFloor ---

test("clampWindowStartToFloor: leaves a window inside [floor, ∞) untouched", () => {
  const floor = NOW - 30 * 24 * 3600 * 1000;
  const start = NOW - 24 * 3600 * 1000;
  const win = 12 * 3600 * 1000;
  const out = clampWindowStartToFloor(start, win, floor);
  assert.equal(out.windowStartMs, start);
  assert.equal(out.windowMs, win);
});

test("clampWindowStartToFloor: shifts a window starting before floor forward to the floor", () => {
  // Pan or zoom can drag windowStart earlier than the oldest event;
  // we snap windowStart to the floor without resizing.
  const floor = 1_000_000;
  const win = 60 * 60 * 1000;
  const start = floor - 5 * 60 * 60 * 1000;
  const out = clampWindowStartToFloor(start, win, floor);
  assert.equal(out.windowStartMs, floor);
  assert.equal(out.windowMs, win);
});

test("clampWindowStartToFloor: null floor (no events yet) is a no-op", () => {
  // Before events load, there's no meaningful floor — don't synthesize
  // one or panning gets pinned to whatever happens to be in `start`.
  const out = clampWindowStartToFloor(123, 456, null);
  assert.equal(out.windowStartMs, 123);
  assert.equal(out.windowMs, 456);
});

// --- windowForPosition (cold-load with ?at=<position>) ---

test("windowForPosition: event inside the default trailing window keeps the default", () => {
  // Cold-loading ?at=N where N is recent should preserve the standard
  // 24h view — no point widening the strip.
  const events = eventsAt(NOW, [3 * 3600]); // 3h ago
  const out = windowForPosition(POS(1), events, NOW);
  const ONE_DAY = 24 * 60 * 60 * 1000;
  assert.equal(out.windowMs, ONE_DAY);
  assert.equal(out.windowStartMs, NOW - ONE_DAY);
});

test("windowForPosition: event older than the default window widens to include it", () => {
  // Regression for ?at=1050 cold-load when the event is older than
  // 24h: the default trailing window leaves the cursor pinned at the
  // left edge and applyCursor's xFrac→eventId resolution snaps to the
  // wrong event. Fix is to widen windowMs so the target event lands
  // inside the visible window with room to scrub forward.
  const events = eventsAt(NOW, [48 * 3600]); // 48h ago — outside default 24h
  const out = windowForPosition(POS(1), events, NOW);
  const eventMs = events[0].created_at * 1000;
  // Event must lie inside [windowStart, windowStart + windowMs].
  assert.ok(out.windowStartMs <= eventMs, "windowStart must be at or before the event");
  assert.ok(out.windowStartMs + out.windowMs >= eventMs, "windowEnd must be at or after the event");
  // Right edge stays pinned to nowMs so the user can still scrub forward.
  assert.equal(out.windowStartMs + out.windowMs, NOW);
  // Window is wider than the default 24h.
  assert.ok(out.windowMs > 24 * 60 * 60 * 1000);
});

test("windowForPosition: a cursor not in the events list returns the default window", () => {
  // Race: the URL specifies an id we haven't loaded (filter, deletion).
  // Don't synthesize a window from a non-existent timestamp.
  const events = eventsAt(NOW, [3 * 3600]);
  const out = windowForPosition(POS(999), events, NOW);
  const ONE_DAY = 24 * 60 * 60 * 1000;
  assert.equal(out.windowMs, ONE_DAY);
  assert.equal(out.windowStartMs, NOW - ONE_DAY);
});

test("windowForPosition: empty events returns the default window", () => {
  const out = windowForPosition(POS(1), [], NOW);
  const ONE_DAY = 24 * 60 * 60 * 1000;
  assert.equal(out.windowMs, ONE_DAY);
  assert.equal(out.windowStartMs, NOW - ONE_DAY);
});

test("windowForPosition: combined with positionToX, target event lands inside the visible window", () => {
  // The end-to-end contract that the cold-load fix relies on: after
  // we've widened the window for an old event, positionToX should map
  // the target cursor to a strictly-interior xFrac (not clamped to 0 or 1).
  const events = eventsAt(NOW, [72 * 3600]); // 72h ago
  const w = windowForPosition(POS(1), events, NOW);
  const x = positionToX(POS(1), events, NOW, w.windowMs, w.windowStartMs);
  assert.ok(x > 0 && x < 1, `expected interior xFrac, got ${x}`);
});

// --- classifyWheelAxis ---

test("classifyWheelAxis: pinch (ctrlKey) is always zoom regardless of axis", () => {
  // macOS reports trackpad pinch as a wheel event with ctrlKey set.
  // We treat that as zoom even when deltaX dominates, so pinch on a
  // strongly-horizontal trackpad still works.
  const r = classifyWheelAxis({
    deltaX: 50,
    deltaY: 1,
    ctrlKey: true,
    timeStamp: 1000,
    lastTimeStamp: null,
    lastAxis: null,
  });
  assert.equal(r.axis, "y");
  assert.equal(r.locked, false, "pinch doesn't lock subsequent gestures");
});

test("classifyWheelAxis: first event picks dominant axis and locks it", () => {
  const r = classifyWheelAxis({
    deltaX: 30,
    deltaY: 4,
    ctrlKey: false,
    timeStamp: 1000,
    lastTimeStamp: null,
    lastAxis: null,
  });
  assert.equal(r.axis, "x");
  assert.equal(r.locked, true);
});

test("classifyWheelAxis: subsequent events within the idle window keep the locked axis", () => {
  // Even if deltaY momentarily dominates mid-gesture, we stay on the
  // axis we picked at the start. Otherwise a slightly noisy trackpad
  // gesture flips between zoom and pan partway through.
  const r = classifyWheelAxis({
    deltaX: 1,
    deltaY: 30,
    ctrlKey: false,
    timeStamp: 1050,
    lastTimeStamp: 1000,
    lastAxis: "x",
  });
  assert.equal(r.axis, "x");
  assert.equal(r.locked, true);
});

test("classifyWheelAxis: idle gap longer than the threshold restarts the lock", () => {
  // 200ms of quiet ends the gesture; the next wheel event picks fresh.
  const r = classifyWheelAxis({
    deltaX: 1,
    deltaY: 30,
    ctrlKey: false,
    timeStamp: 5000,
    lastTimeStamp: 4000,
    lastAxis: "x",
  });
  assert.equal(r.axis, "y");
  assert.equal(r.locked, true);
});

test("classifyWheelAxis: zero delta passes through with no axis change", () => {
  // Some browsers fire a deltaX=deltaY=0 wheel at the very start of a
  // gesture; classifying that would lock to a meaningless axis.
  const r = classifyWheelAxis({
    deltaX: 0,
    deltaY: 0,
    ctrlKey: false,
    timeStamp: 1000,
    lastTimeStamp: null,
    lastAxis: null,
  });
  assert.equal(r.axis, null);
  assert.equal(r.locked, false);
});
