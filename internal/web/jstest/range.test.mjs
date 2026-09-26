// Tests for internal/web/assets/js/range.mjs — the client mirror of
// internal/job/timerange.go (the key vocabulary) and
// internal/web/handlers/range.go (which keys a view offers). The
// scrubber rebuilds a view from the in-memory event log, so it needs
// the same ?range= parsing and the same cutoff arithmetic the server
// used to render the first frame.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  RANGE_1H,
  RANGE_1D,
  RANGE_7D,
  RANGE_14D,
  RANGE_30D,
  RANGE_ALL,
  RANGE_KEYS,
  BOUNDED_VIEW_RANGES,
  DEFAULT_RANGE,
  parseRangeKey,
  rangeSeconds,
  rangeCutoff,
  rangeFromSearch,
} from "../assets/js/range.mjs";

const HOUR = 3600;
const DAY = 86400;

test("RANGE_KEYS: every core key, shortest first, 'all' last", () => {
  assert.deepEqual(RANGE_KEYS, [RANGE_1H, RANGE_1D, RANGE_7D, RANGE_14D, RANGE_30D, RANGE_ALL]);
  assert.equal(RANGE_1H, "1h");
  assert.equal(RANGE_1D, "1d");
});

test("BOUNDED_VIEW_RANGES: the Actors board and Log keep their four keys", () => {
  assert.deepEqual(BOUNDED_VIEW_RANGES, [RANGE_7D, RANGE_14D, RANGE_30D, RANGE_ALL]);
});

test("parseRangeKey: known keys pass through, anything else is the 7d default", () => {
  assert.equal(parseRangeKey("1h"), RANGE_1H);
  assert.equal(parseRangeKey("1d"), RANGE_1D);
  assert.equal(parseRangeKey("7d"), RANGE_7D);
  assert.equal(parseRangeKey("14d"), RANGE_14D);
  assert.equal(parseRangeKey("30d"), RANGE_30D);
  assert.equal(parseRangeKey("all"), RANGE_ALL);
  assert.equal(DEFAULT_RANGE, RANGE_7D);

  assert.equal(parseRangeKey(""), RANGE_7D);
  assert.equal(parseRangeKey(null), RANGE_7D);
  assert.equal(parseRangeKey(undefined), RANGE_7D);
  assert.equal(parseRangeKey("90d"), RANGE_7D);
  assert.equal(parseRangeKey("nonsense"), RANGE_7D);
});

test("parseRangeKey: trims and lowercases, matching ParseRangeKey in Go", () => {
  assert.equal(parseRangeKey("  30d  "), RANGE_30D);
  assert.equal(parseRangeKey("30D"), RANGE_30D);
  assert.equal(parseRangeKey("ALL"), RANGE_ALL);
  assert.equal(parseRangeKey("1H"), RANGE_1H);
});

test("parseRangeKey: a key the view does not offer falls back to the default", () => {
  assert.equal(parseRangeKey("1h", BOUNDED_VIEW_RANGES), RANGE_7D);
  assert.equal(parseRangeKey("1d", BOUNDED_VIEW_RANGES), RANGE_7D);
  assert.equal(parseRangeKey("30d", BOUNDED_VIEW_RANGES), RANGE_30D);
  assert.equal(parseRangeKey("all", BOUNDED_VIEW_RANGES), RANGE_ALL);
});

test("rangeSeconds: window length per key; 'all' is unbounded (0)", () => {
  assert.equal(rangeSeconds(RANGE_1H), HOUR);
  assert.equal(rangeSeconds(RANGE_1D), DAY);
  assert.equal(rangeSeconds(RANGE_7D), 7 * DAY);
  assert.equal(rangeSeconds(RANGE_14D), 14 * DAY);
  assert.equal(rangeSeconds(RANGE_30D), 30 * DAY);
  assert.equal(rangeSeconds(RANGE_ALL), 0);
});

test("rangeCutoff: measured back from the anchor, not from wall-clock now", () => {
  const anchor = 1700000000;
  assert.equal(rangeCutoff(RANGE_1H, anchor), anchor - HOUR);
  assert.equal(rangeCutoff(RANGE_7D, anchor), anchor - 7 * DAY);
  assert.equal(rangeCutoff(RANGE_30D, anchor), anchor - 30 * DAY);
  assert.equal(rangeCutoff(RANGE_ALL, anchor), 0);
});

test("rangeFromSearch: reads ?range= off a location search string for a view", () => {
  const anchor = 1700000000;
  assert.deepEqual(rangeFromSearch("?range=30d", anchor, BOUNDED_VIEW_RANGES), {
    key: RANGE_30D,
    cutoff: anchor - 30 * DAY,
  });
  assert.deepEqual(rangeFromSearch("", anchor, BOUNDED_VIEW_RANGES), {
    key: RANGE_7D,
    cutoff: anchor - 7 * DAY,
  });
  assert.deepEqual(rangeFromSearch("?at=42&range=all", anchor, BOUNDED_VIEW_RANGES), {
    key: RANGE_ALL,
    cutoff: 0,
  });
  assert.deepEqual(rangeFromSearch("?range=bogus", anchor, BOUNDED_VIEW_RANGES), {
    key: RANGE_7D,
    cutoff: anchor - 7 * DAY,
  });
});

test("rangeFromSearch: an unoffered 1h falls back, as the server's first frame did", () => {
  const anchor = 1700000000;
  assert.deepEqual(rangeFromSearch("?range=1h", anchor, BOUNDED_VIEW_RANGES), {
    key: RANGE_7D,
    cutoff: anchor - 7 * DAY,
  });
  assert.deepEqual(rangeFromSearch("?range=1h", anchor, [RANGE_1H, RANGE_7D]), {
    key: RANGE_1H,
    cutoff: anchor - HOUR,
  });
});

test("rangeFromSearch: a view must say which keys it offers", () => {
  assert.throws(() => rangeFromSearch("?range=1h", 1700000000), TypeError);
});
