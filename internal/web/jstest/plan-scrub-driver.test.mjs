// Tests for the pure pieces of internal/web/assets/js/plan-scrub.mjs.
//
// The driver itself reads window.location and document, dispatches
// CustomEvents, and runs DOMParser — none of that is available in the
// node test runner. The pure helpers below are the parts we can
// validate without a browser; the in-browser wiring is exercised
// manually (the plan-live.js fetch-and-swap path it mirrors is
// long-tested in production).

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  parseFiltersFromSearch,
  buildShowTabs,
  buildPlanLabelChips,
  composePlanFilterBarShape,
  readViewFromDOM,
} from "../assets/js/plan-scrub.mjs";
import { initialFrame } from "../assets/js/replay.mjs";

// --- parseFiltersFromSearch ---

test("parseFiltersFromSearch: empty / missing returns sensible defaults", () => {
  assert.deepStrictEqual(parseFiltersFromSearch(""), { selected: [], show: "active" });
  assert.deepStrictEqual(parseFiltersFromSearch("?"), { selected: [], show: "active" });
});

test("parseFiltersFromSearch: ?label=foo,bar splits into a sorted, deduped list", () => {
  // Mirrors plan.go parseLabelParam: comma-split, trimmed, deduped, sorted.
  assert.deepStrictEqual(parseFiltersFromSearch("?label=web,alpha,web"), {
    selected: ["alpha", "web"],
    show: "active",
  });
});

test("parseFiltersFromSearch: ?show=archived/all is preserved; junk falls back to active", () => {
  assert.equal(parseFiltersFromSearch("?show=archived").show, "archived");
  assert.equal(parseFiltersFromSearch("?show=all").show, "all");
  assert.equal(parseFiltersFromSearch("?show=banana").show, "active");
});

test("parseFiltersFromSearch: ignores ?at= (the scrubber owns that param)", () => {
  // ?at= drives the cursor; it's not a Plan filter. The driver reads
  // search → filters with ?at left for the scrubber to consume.
  assert.deepStrictEqual(parseFiltersFromSearch("?at=42&label=web"), {
    selected: ["web"],
    show: "active",
  });
});

// --- buildShowTabs ---

test("buildShowTabs: emits Active/Archived/All with the current show flagged active", () => {
  const tabs = buildShowTabs([], "archived");
  assert.deepStrictEqual(tabs, [
    { label: "Active", url: "/plan", active: false },
    { label: "Archived", url: "/plan?show=archived", active: true },
    { label: "All", url: "/plan?show=all", active: false },
  ]);
});

test("buildShowTabs: preserves the current label selection across tab URLs", () => {
  const tabs = buildShowTabs(["web"], "active");
  assert.equal(tabs[0].url, "/plan?label=web");
  assert.equal(tabs[1].url, "/plan?label=web&show=archived");
  assert.equal(tabs[2].url, "/plan?label=web&show=all");
});

// --- buildPlanLabelChips ---

test("buildPlanLabelChips: each strip name → toggle URL, active=true if selected", () => {
  // Clicking 'alpha' (not selected) adds it; clicking 'web' (selected)
  // removes it, leaving the empty-selection URL "/plan".
  const chips = buildPlanLabelChips(["alpha", "web"], ["web"], "active");
  assert.deepStrictEqual(chips, [
    { name: "alpha", url: "/plan?label=alpha,web", active: false },
    { name: "web", url: "/plan", active: true },
  ]);
});

// --- composePlanFilterBarShape ---

test("composePlanFilterBarShape: assembles the renderFilterBar input from a frame + filters", () => {
  // Integration shape check — walk a tiny frame end-to-end so the
  // strip labels, tabs, and "any" pill all line up.
  const frame = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "A0001", title: "A", status: "available", sortKey: "000001", labels: ["web"] },
      { shortId: "B0001", title: "B", status: "available", sortKey: "000002", labels: ["web", "dx"] },
    ],
    blocks: [],
    claims: [],
  });
  const out = composePlanFilterBarShape(frame, { selected: [], show: "active" });
  assert.equal(out.allActive, true);
  assert.equal(out.allURL, "/plan");
  assert.equal(out.showTabs.length, 3);
  assert.deepStrictEqual(
    out.labels.map((l) => l.name),
    ["web", "dx"],
  );
});

// --- view parameterisation ---

test("composePlanFilterBarShape: an issue view filters the forest by kind and targets /issues", () => {
  const frame = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "P0001", title: "Plan", status: "available", sortKey: "000001", kind: "task", labels: ["web"] },
      { shortId: "I0001", title: "Issues", status: "available", sortKey: "000002", kind: "issue" },
      { shortId: "C0001", title: "Bug", status: "available", parentShortId: "I0001", sortKey: "000001", labels: ["parser"] },
    ],
    blocks: [],
    claims: [],
  });
  const out = composePlanFilterBarShape(
    frame,
    { selected: [], show: "active" },
    { kind: "issue", base: "/issues", filterLabel: "Issues filter", meta: "1 open · 0 closed in 7d" },
  );
  assert.equal(out.allURL, "/issues");
  assert.equal(out.filterLabel, "Issues filter");
  assert.equal(out.meta, "1 open · 0 closed in 7d");
  // Strip labels come from the issue forest only — "web" lives on the
  // task root and must not leak into the Issues view's filter strip.
  assert.deepStrictEqual(
    out.labels.map((l) => l.name),
    ["parser"],
  );
  assert.equal(out.showTabs[1].url, "/issues?show=archived");
});

test("composePlanFilterBarShape: the default (task) view still excludes issue roots", () => {
  const frame = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "P0001", title: "Plan", status: "available", sortKey: "000001", kind: "task", labels: ["web"] },
      { shortId: "I0001", title: "Issues", status: "available", sortKey: "000002", kind: "issue", labels: ["parser"] },
    ],
    blocks: [],
    claims: [],
  });
  const out = composePlanFilterBarShape(frame, { selected: [], show: "active" });
  assert.deepStrictEqual(
    out.labels.map((l) => l.name),
    ["web"],
  );
  assert.equal(out.allURL, "/plan");
});

// --- readViewFromDOM ---
//
// The driver recovers the per-view config from the attributes the SSR
// template already emitted, so no page has to wire the module up. The
// fakes below carry only the two methods it calls.

function fakeSection(attrs) {
  return { getAttribute: (k) => attrs[k] ?? null };
}

function fakeDoc(metaText) {
  return {
    querySelector: (sel) =>
      sel === "main [data-view-meta]" && metaText !== null
        ? { textContent: metaText }
        : null,
  };
}

test("readViewFromDOM: an issue section yields the Issues names, base and meta", () => {
  const view = readViewFromDOM(
    fakeSection({ "data-plan-view": "issue", "data-plan-base": "/issues/abc12" }),
    fakeDoc("3 open · 5 closed in 7d"),
  );
  assert.deepStrictEqual(view, {
    kind: "issue",
    base: "/issues/abc12",
    sectionLabel: "Issues",
    filterLabel: "Issues filter",
    emptyText: "No open issues.",
    meta: "3 open · 5 closed in 7d",
  });
});

test("readViewFromDOM: a plan section yields the Plan defaults and no meta", () => {
  const view = readViewFromDOM(
    fakeSection({ "data-plan-view": "task", "data-plan-base": "/plan" }),
    fakeDoc(null),
  );
  assert.equal(view.kind, "task");
  assert.equal(view.base, "/plan");
  assert.equal(view.sectionLabel, "Plan");
  assert.equal(view.emptyText, "No active tasks.");
  assert.equal(view.meta, "");
});

test("readViewFromDOM: a section missing its attributes falls back to the Plan view", () => {
  const view = readViewFromDOM(fakeSection({}), fakeDoc(null));
  assert.equal(view.kind, "task");
  assert.equal(view.base, "/plan");
});
