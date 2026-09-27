// Tests for internal/web/assets/js/chart-geometry.mjs — the burn-up's
// scales and path builders, held to the Go layout. The fixture is
// written by TestBurnupGeometryFixture (internal/web/chart), which fails
// when it is stale; this suite fails when the JS stops reproducing it.
// Together the two drawings cannot drift apart (decision 9 of
// project/2026-09-27-chart-panel-revision.md).

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import {
  fmtNum,
  fmtPct,
  xOf,
  yOf,
  burnupPaths,
  markersAt,
  endLabelPositions,
} from "../assets/js/chart-geometry.mjs";

const fixture = JSON.parse(
  readFileSync(new URL("./fixtures/burnup-geometry.json", import.meta.url), "utf8"),
);

test("fixture: covers several windows, a canceled band among them", () => {
  assert.ok(fixture.cases.length >= 4);
  assert.ok(fixture.cases.some((c) => c.canceledPath !== ""));
  assert.ok(fixture.cases.some((c) => c.canceledPath === ""));
});

test("fmtNum spells every coordinate the way Go's fmtNum does, ties included", () => {
  for (const { in: v, out } of fixture.numbers) {
    assert.equal(fmtNum(v), out, `fmtNum(${v})`);
  }
});

test("fmtPct appends a percent sign to fmtNum", () => {
  assert.equal(fmtPct(12.345), "12.35%");
  assert.equal(fmtPct(-0.001), "0%");
});

for (const c of fixture.cases) {
  test(`burnupPaths reproduces the Go paths: ${c.name}`, () => {
    const p = burnupPaths(c.island);
    assert.equal(p.scope, c.scopePath, "scope");
    assert.equal(p.done, c.donePath, "done");
    assert.equal(p.gap, c.gapPath, "gap");
    assert.equal(p.blocked, c.blockedPath, "blocked");
    assert.equal(p.canceled, c.canceledPath, "canceled");
  });

  test(`markersAt the last sample matches the Go dots and end labels: ${c.name}`, () => {
    const m = markersAt(c.island, c.island.trace.length - 1);
    assert.deepEqual(m.scopeDot, c.scopeDot);
    assert.deepEqual(m.doneDot, c.doneDot);
    assert.equal(m.createdY, c.createdY);
    assert.equal(m.doneY, c.doneY);
  });
}

test("xOf maps since to 0 and until to the viewBox width", () => {
  assert.equal(xOf(1000, 1000, 5000), 0);
  assert.equal(xOf(5000, 1000, 5000), 1000);
  assert.equal(xOf(3000, 1000, 5000), 500);
});

test("xOf survives a zero-length window, as Go's timeScale does", () => {
  assert.equal(xOf(1000, 1000, 1000), 0);
});

test("yOf puts lo on the baseline and hi at the top edge", () => {
  assert.equal(yOf(10, 10, 20), 1000);
  assert.equal(yOf(20, 10, 20), 0);
  assert.equal(yOf(15, 10, 20), 500);
});

test("endLabelPositions pushes close labels apart and keeps them in the plot", () => {
  // Far apart: untouched.
  assert.deepEqual(endLabelPositions(10, 70), [10, 70]);
  // Close together mid-plot: centred 40 apart.
  assert.deepEqual(endLabelPositions(40, 45), [22.5, 62.5]);
  // Close together at the top: pushed down inside.
  assert.deepEqual(endLabelPositions(0, 2), [6, 46]);
  // At the bottom: pushed up inside.
  assert.deepEqual(endLabelPositions(98, 100), [34, 74]);
});

test("markersAt an earlier sample follows that sample", () => {
  const island = fixture.cases.find((c) => c.name === "ramp-with-band").island;
  const first = markersAt(island, 0);
  assert.equal(first.scopeDot.x, "0%");
  const mid = markersAt(island, 20);
  assert.equal(mid.scopeDot.x, "50%");
});
