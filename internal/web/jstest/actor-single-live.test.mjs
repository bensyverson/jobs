// Tests for the pure pieces of
// internal/web/assets/js/actor-single-live.mjs.
//
// The module's DOM wiring (prepending rows, appending marks) needs a
// browser and is exercised by hand; what is worth pinning here is the
// arithmetic that decides *where* a live mark lands, because it has to
// agree with handlers.loadActorTimeline for every window the segmented
// control offers.

import { test } from "node:test";
import assert from "node:assert/strict";

import { TIMELINE_VERBS, isTimelineVerb, markPercent } from "../assets/js/actor-single-live.mjs";

const HOUR = 3600;
const DAY = 24 * HOUR;

test("TIMELINE_VERBS mirrors the server's five lanes, in order", () => {
  assert.deepStrictEqual(TIMELINE_VERBS, ["created", "claimed", "done", "blocked", "noted"]);
});

test("isTimelineVerb: only the five lanes get a mark", () => {
  for (const v of TIMELINE_VERBS) assert.equal(isTimelineVerb(v), true);
  for (const v of ["released", "canceled", "claim_expired", "found_in_set", "", undefined]) {
    assert.equal(isTimelineVerb(v), false);
  }
});

test("markPercent: an event at 'now' sits at the right edge", () => {
  assert.equal(markPercent(1000, 1000, DAY), "100.0");
});

test("markPercent: an event at the window's start sits at the left edge", () => {
  assert.equal(markPercent(1000 - DAY, 1000, DAY), "0.0");
});

test("markPercent: the same age reads differently in different windows", () => {
  const now = 10 * DAY;
  const threeDaysAgo = now - 3 * DAY;
  // 3 of 7 days back → 4/7 along the axis, matching the Go formatter.
  assert.equal(markPercent(threeDaysAgo, now, 7 * DAY), "57.1");
  // The same event in a 30d window sits much closer to now.
  assert.equal(markPercent(threeDaysAgo, now, 30 * DAY), "90.0");
});

test("markPercent: an event older than the window is out of range", () => {
  assert.equal(markPercent(0, 10 * DAY, DAY), null);
});

test("markPercent: a clock-skewed future event clamps to the right edge", () => {
  assert.equal(markPercent(1000 + HOUR, 1000, DAY), "100.0");
});

test("markPercent: a non-finite window yields no mark rather than NaN", () => {
  assert.equal(markPercent(1000, 1000, 0), null);
  assert.equal(markPercent(1000, 1000, NaN), null);
});
