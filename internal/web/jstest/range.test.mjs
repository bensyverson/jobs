// Tests for internal/web/assets/js/range.mjs — the client mirror of
// handlers/range.go. The scrubber rebuilds a view from the in-memory
// event log, so it needs the same ?range= parsing and the same cutoff
// arithmetic the server used to render the first frame.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  RANGE_7D,
  RANGE_14D,
  RANGE_30D,
  RANGE_ALL,
  DEFAULT_RANGE,
  parseRangeKey,
  rangeSeconds,
  rangeCutoff,
  rangeFromSearch,
} from "../assets/js/range.mjs";

const DAY = 86400;

test("parseRangeKey: known keys pass through, anything else is the 7d default", () => {
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

test("parseRangeKey: trims and lowercases, matching parseRange in Go", () => {
  assert.equal(parseRangeKey("  30d  "), RANGE_30D);
  assert.equal(parseRangeKey("30D"), RANGE_30D);
  assert.equal(parseRangeKey("ALL"), RANGE_ALL);
});

test("rangeSeconds: window length per key; 'all' is unbounded (0)", () => {
  assert.equal(rangeSeconds(RANGE_7D), 7 * DAY);
  assert.equal(rangeSeconds(RANGE_14D), 14 * DAY);
  assert.equal(rangeSeconds(RANGE_30D), 30 * DAY);
  assert.equal(rangeSeconds(RANGE_ALL), 0);
});

test("rangeCutoff: measured back from the anchor, not from wall-clock now", () => {
  const anchor = 1700000000;
  assert.equal(rangeCutoff(RANGE_7D, anchor), anchor - 7 * DAY);
  assert.equal(rangeCutoff(RANGE_30D, anchor), anchor - 30 * DAY);
  assert.equal(rangeCutoff(RANGE_ALL, anchor), 0);
});

test("rangeFromSearch: reads ?range= off a location search string", () => {
  const anchor = 1700000000;
  assert.deepEqual(rangeFromSearch("?range=30d", anchor), {
    key: RANGE_30D,
    cutoff: anchor - 30 * DAY,
  });
  assert.deepEqual(rangeFromSearch("", anchor), {
    key: RANGE_7D,
    cutoff: anchor - 7 * DAY,
  });
  assert.deepEqual(rangeFromSearch("?at=42&range=all", anchor), {
    key: RANGE_ALL,
    cutoff: 0,
  });
  assert.deepEqual(rangeFromSearch("?range=bogus", anchor), {
    key: RANGE_7D,
    cutoff: anchor - 7 * DAY,
  });
});
