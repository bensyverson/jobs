// Tests for internal/web/assets/js/position.mjs.
//
// The encoding is a cursor, not a sort key: these pin that comparison
// happens on the parsed triple, never on the string.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  parsePosition,
  isPosition,
  comparePositions,
  samePosition,
  sortByPosition,
} from "../assets/js/position.mjs";

test("parsePosition: decodes a positioned cursor", () => {
  assert.deepStrictEqual(parsePosition("1756742400123-k7Qx2m-412"), {
    ts: 1756742400123,
    rep: "k7Qx2m",
    seq: 412,
  });
});

test("parsePosition: decodes a legacy cursor with an empty replica", () => {
  assert.deepStrictEqual(parsePosition("1756742400000--991"), {
    ts: 1756742400000,
    rep: "",
    seq: 991,
  });
});

test("parsePosition: rejects anything that is not a position", () => {
  for (const s of [
    "",
    null,
    undefined,
    42,
    "42",
    "1-k7Qx2m",
    "1-k7Qx2m-2-3",
    "x-k7Qx2m-2",
    "1-k7Qx2m-x",
    "0-k7Qx2m-1",
    "1-k7Qx2m-0",
    "-1-k7Qx2m-2",
    "1-k7 x2m-2",
  ]) {
    assert.equal(parsePosition(s), null, `parsePosition(${JSON.stringify(s)})`);
    assert.equal(isPosition(s), false);
  }
});

test("comparePositions: orders ts, then replica, then seq", () => {
  assert.equal(comparePositions("1-zzzzzz-99", "2-aaaaaa-1"), -1);
  assert.equal(comparePositions("1-aaaaaa-99", "1-bbbbbb-1"), -1);
  assert.equal(comparePositions("1-aaaaaa-1", "1-aaaaaa-2"), -1);
  assert.equal(comparePositions("1-aaaaaa-1", "1-aaaaaa-1"), 0);
  assert.equal(comparePositions("3-aaaaaa-1", "2-aaaaaa-1"), 1);
});

test("comparePositions: never compares the encoding as a string", () => {
  // "1000-aaaaaa-9" > "1000-aaaaaa-10" lexically; by cursor it is not.
  assert.ok("1000-aaaaaa-9" > "1000-aaaaaa-10", "the string trap still exists");
  assert.equal(comparePositions("1000-aaaaaa-9", "1000-aaaaaa-10"), -1);
  // Same trap on ts.
  assert.equal(comparePositions("900-aaaaaa-1", "1000-aaaaaa-1"), -1);
});

test("comparePositions: a legacy cursor sorts before a positioned one at the same ts", () => {
  assert.equal(comparePositions("10--4", "10-aaaaaa-1"), -1);
});

test("comparePositions: an absent cursor is the genesis end of the log", () => {
  assert.equal(comparePositions(null, "1-aaaaaa-1"), -1);
  assert.equal(comparePositions("1-aaaaaa-1", null), 1);
  assert.equal(comparePositions(null, null), 0);
  assert.equal(comparePositions("", ""), 0);
});

test("samePosition: only two real cursors can be equal", () => {
  assert.equal(samePosition("1-aaaaaa-1", "1-aaaaaa-1"), true);
  assert.equal(samePosition("1-aaaaaa-1", "1-aaaaaa-2"), false);
  assert.equal(samePosition(null, null), false);
  assert.equal(samePosition("nonsense", "nonsense"), false);
});

test("sortByPosition: orders events by cursor, not by arrival", () => {
  const events = [
    { position: "1000-aaaaaa-10" },
    { position: "1000-aaaaaa-9" },
    { position: "999-zzzzzz-1" },
  ];
  assert.deepStrictEqual(
    sortByPosition(events).map((e) => e.position),
    ["999-zzzzzz-1", "1000-aaaaaa-9", "1000-aaaaaa-10"],
  );
});
