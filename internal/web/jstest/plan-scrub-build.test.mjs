// Tests for internal/web/assets/js/plan-scrub-build.mjs.
//
// Pure-data layer of the Plan-view scrubber: takes a Frame from
// replay.mjs and produces the tree the renderer walks. Mirrors
// internal/web/handlers/plan.go's buildPlanNodes / filter / rollup
// logic so SSR and history mode render the same shapes.

import { test } from "node:test";
import assert from "node:assert/strict";

import { initialFrame } from "../assets/js/replay.mjs";
import {
  displayStatus,
  relativeTime,
  buildForestFromFrame,
  isArchivedSubtree,
  filterRootsByShow,
  filterRootsByKind,
  filterForestByLabels,
  labelFreqsByView,
  pickStripLabels,
  buildPlanNodes,
  planURL,
  toggleLabel,
  addLabel,
} from "../assets/js/plan-scrub-build.mjs";

// --- displayStatus ---

test("displayStatus: maps raw status + blocker flag to visual category", () => {
  assert.equal(displayStatus("available", false), "todo");
  assert.equal(displayStatus("available", true), "blocked");
  assert.equal(displayStatus("claimed", false), "active");
  assert.equal(displayStatus("claimed", true), "blocked");
  assert.equal(displayStatus("done", false), "done");
  assert.equal(displayStatus("done", true), "done");
  assert.equal(displayStatus("canceled", false), "canceled");
  assert.equal(displayStatus("canceled", true), "canceled");
  assert.equal(displayStatus("weird", false), "weird");
});

// --- relativeTime ---

test("relativeTime: matches the Go RelativeTime format ladder", () => {
  // Args are unix-second timestamps so callers can hand the cursor's
  // event.created_at directly without juggling Date objects.
  assert.equal(relativeTime(1000, 1000), "just now");
  assert.equal(relativeTime(1005, 1000), "5s");
  assert.equal(relativeTime(1000 + 90, 1000), "1m");
  assert.equal(relativeTime(1000 + 3600, 1000), "1h");
  assert.equal(relativeTime(1000 + 3660, 1000), "1h 1m");
  assert.equal(relativeTime(1000 + 25 * 3600, 1000), "1d 1h");
  assert.equal(relativeTime(1000 + 24 * 3600, 1000), "1d");
  // Future timestamps (event > now) render non-negative.
  assert.equal(relativeTime(1000, 1005), "5s");
});

// --- buildForestFromFrame ---

function frameWith({ tasks = [], blocks = [], claims = [], headEventId = 0 }) {
  return initialFrame({ headEventId, tasks, blocks, claims });
}

test("buildForestFromFrame: groups tasks by parent in sortKey", () => {
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "P", status: "available", sortKey: "000001" },
      { shortId: "C0001", title: "C1", status: "available", parentShortId: "P0001", sortKey: "000002" },
      { shortId: "C0002", title: "C2", status: "available", parentShortId: "P0001", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  assert.equal(roots.length, 1);
  assert.equal(roots[0].task.shortId, "P0001");
  // Children sorted by sortKey ascending.
  assert.deepStrictEqual(
    roots[0].children.map((n) => n.task.shortId),
    ["C0002", "C0001"],
  );
});

test("buildForestFromFrame: orphans (parent missing) become roots", () => {
  // Defensive: if a frame is missing a parent we'd otherwise drop the
  // task. Surface it as a root instead so the user still sees it.
  const f = frameWith({
    tasks: [
      { shortId: "X0001", title: "Orphan", status: "available", parentShortId: "MISSING", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  assert.equal(roots.length, 1);
  assert.equal(roots[0].task.shortId, "X0001");
});

test("buildForestFromFrame: roots ordered by sortKey asc, shortId tiebreak", () => {
  const f = frameWith({
    tasks: [
      { shortId: "A0001", title: "A", status: "available", sortKey: "000002" },
      { shortId: "B0001", title: "B", status: "available", sortKey: "000001" },
      { shortId: "C0001", title: "C", status: "available", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  assert.deepStrictEqual(
    roots.map((n) => n.task.shortId),
    ["B0001", "C0001", "A0001"],
  );
});

// --- archive filter ---

test("isArchivedSubtree: true iff every node is done/canceled", () => {
  const tree = {
    task: { shortId: "P", status: "done" },
    children: [
      { task: { shortId: "C", status: "canceled" }, children: [] },
    ],
  };
  assert.equal(isArchivedSubtree(tree), true);

  const mixed = {
    task: { shortId: "P", status: "done" },
    children: [
      { task: { shortId: "C", status: "available" }, children: [] },
    ],
  };
  assert.equal(isArchivedSubtree(mixed), false);
});

test("filterRootsByShow: active hides archived; archived shows only archived; all shows everything", () => {
  const archived = { task: { shortId: "A", status: "done" }, children: [] };
  const active = { task: { shortId: "B", status: "available" }, children: [] };
  const roots = [archived, active];
  assert.deepStrictEqual(
    filterRootsByShow(roots, "active").map((r) => r.task.shortId),
    ["B"],
  );
  assert.deepStrictEqual(
    filterRootsByShow(roots, "archived").map((r) => r.task.shortId),
    ["A"],
  );
  assert.deepStrictEqual(
    filterRootsByShow(roots, "all").map((r) => r.task.shortId),
    ["A", "B"],
  );
});

// --- label filter ---

test("filterForestByLabels: keeps tasks (and ancestors) matching any selected label", () => {
  // Label sets live on the task object inside frame; the helper reads
  // task.labels (a Set).
  const tree = [
    {
      task: { shortId: "P", status: "available", labels: new Set() },
      children: [
        { task: { shortId: "C1", status: "available", labels: new Set(["web"]) }, children: [] },
        { task: { shortId: "C2", status: "available", labels: new Set(["mobile"]) }, children: [] },
      ],
    },
    {
      task: { shortId: "Q", status: "available", labels: new Set() },
      children: [],
    },
  ];
  const filtered = filterForestByLabels(tree, ["web"]);
  // P kept because C1 matches; C2 dropped; Q dropped (no match in subtree).
  assert.equal(filtered.length, 1);
  assert.equal(filtered[0].task.shortId, "P");
  assert.equal(filtered[0].children.length, 1);
  assert.equal(filtered[0].children[0].task.shortId, "C1");
});

// --- label strip ---

test("pickStripLabels: top-N by frequency, name asc tiebreak; selected always present", () => {
  const tree = [
    {
      task: { shortId: "A", status: "available", labels: new Set(["alpha", "beta"]) },
      children: [],
    },
    {
      task: { shortId: "B", status: "available", labels: new Set(["alpha"]) },
      children: [],
    },
    {
      task: { shortId: "C", status: "available", labels: new Set(["gamma"]) },
      children: [],
    },
    {
      task: { shortId: "D", status: "done", labels: new Set(["zeta"]) },
      children: [],
    },
  ];
  // active view: only open tasks counted; alpha=2, beta=1, gamma=1.
  const top2 = pickStripLabels(tree, [], "active", 2);
  assert.deepStrictEqual(top2, ["alpha", "beta"]);

  // selected zeta isn't in top-2 active view; it must still appear.
  const withSelected = pickStripLabels(tree, ["zeta"], "active", 2);
  assert.deepStrictEqual(withSelected, ["alpha", "beta", "zeta"]);
});

test("labelFreqsByView: archived view counts only done/canceled tasks", () => {
  const tree = [
    { task: { shortId: "A", status: "available", labels: new Set(["x"]) }, children: [] },
    { task: { shortId: "B", status: "done", labels: new Set(["x", "y"]) }, children: [] },
  ];
  assert.deepStrictEqual(labelFreqsByView(tree, "archived"), { x: 1, y: 1 });
  assert.deepStrictEqual(labelFreqsByView(tree, "active"), { x: 1 });
  assert.deepStrictEqual(labelFreqsByView(tree, "all"), { x: 2, y: 1 });
});

// --- buildPlanNodes (rollup, collapsed, depth, blocked) ---

test("buildPlanNodes: blocked task with open blockers shows as blocked", () => {
  // Use sortKey to put T0001 first since shortId is the tiebreak.
  const f = frameWith({
    tasks: [
      { shortId: "T0001", title: "T", status: "available", sortKey: "000001" },
      { shortId: "B0001", title: "Blocker", status: "available", sortKey: "000002" },
    ],
    blocks: [{ blockedShortId: "T0001", blockerShortId: "B0001" }],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000);
  const t = nodes.find((n) => n.shortID === "T0001");
  assert.equal(t.displayStatus, "blocked");
  assert.equal(t.blockedBy.length, 1);
  assert.equal(t.blockedBy[0].shortID, "B0001");
  assert.equal(t.blockedBy[0].title, "Blocker");
});

test("buildPlanNodes: parent rolls up to active when any descendant is claimed", () => {
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "P", status: "available", sortKey: "000001" },
      { shortId: "C0001", title: "C", status: "claimed", parentShortId: "P0001", sortKey: "000001" },
    ],
    claims: [{ shortId: "C0001", claimedBy: "alice", expiresAt: 1700000999 }],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000);
  assert.equal(nodes[0].displayStatus, "active");
});

test("buildPlanNodes: subtree fully closed → collapsed=true", () => {
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "P", status: "done", sortKey: "000001" },
      { shortId: "C0001", title: "C", status: "done", parentShortId: "P0001", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000);
  assert.equal(nodes[0].collapsed, true);
});

test("buildPlanNodes: depth increments, hasChildren / collapsible flags set", () => {
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "P", description: "", status: "available", sortKey: "000001" },
      { shortId: "C0001", title: "C", description: "with desc", status: "available", parentShortId: "P0001", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000);
  assert.equal(nodes[0].depth, 0);
  assert.equal(nodes[0].hasChildren, true);
  assert.equal(nodes[0].collapsible, true);
  assert.equal(nodes[0].children[0].depth, 1);
  assert.equal(nodes[0].children[0].hasChildren, false);
  assert.equal(nodes[0].children[0].collapsible, true); // has description
});

test("buildPlanNodes: actor pulled from frame.claims when present", () => {
  const f = frameWith({
    tasks: [{ shortId: "T0001", title: "T", status: "claimed", sortKey: "000001" }],
    claims: [{ shortId: "T0001", claimedBy: "alice", expiresAt: 1700000999 }],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000);
  assert.equal(nodes[0].actor, "alice");
});

test("buildPlanNodes: notes carried through with status tint", () => {
  const f = frameWith({
    tasks: [
      {
        shortId: "T0001",
        title: "T",
        status: "available",
        sortKey: "000001",
        notes: [{ actor: "alice", ts: 1700000000, text: "hello" }],
      },
    ],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000060);
  assert.equal(nodes[0].notes.length, 1);
  assert.equal(nodes[0].notes[0].actor, "alice");
  assert.equal(nodes[0].notes[0].text, "hello");
  assert.equal(nodes[0].notes[0].displayStatus, "todo");
  assert.equal(nodes[0].notes[0].relTime, "1m");
});

test("buildPlanNodes: row labels are {name, url} where url is the add-label URL", () => {
  const f = frameWith({
    tasks: [
      {
        shortId: "T0001",
        title: "T",
        status: "available",
        sortKey: "000001",
        labels: ["web"],
      },
    ],
  });
  const roots = buildForestFromFrame(f);
  // No selection yet: the URL adds 'web' to the empty set.
  const nodes = buildPlanNodes(roots, f, 1700000000, { selected: [], show: "active" });
  assert.deepStrictEqual(nodes[0].labels, [
    { name: "web", url: "/plan?label=web" },
  ]);
});

test("buildPlanNodes: add-label URL preserves current show mode", () => {
  const f = frameWith({
    tasks: [
      {
        shortId: "T0001",
        title: "T",
        status: "available",
        sortKey: "000001",
        labels: ["web"],
      },
    ],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000, { selected: [], show: "archived" });
  assert.equal(nodes[0].labels[0].url, "/plan?label=web&show=archived");
});

// --- URL helpers (mirror plan.go's planURL) ---

test("planURL: empty → /plan; raw commas joining encoded segments; default show omitted", () => {
  // Mirrors plan.go planURL: callers pass sorted selected (toggleLabel
  // and addLabel sort). Each segment is QueryEscape'd, joined with
  // raw commas. Default show ("active") is omitted; archived/all are
  // included after label.
  assert.equal(planURL([], "active"), "/plan");
  assert.equal(planURL(["alpha", "web"], "active"), "/plan?label=alpha,web");
  assert.equal(planURL([], "archived"), "/plan?show=archived");
  assert.equal(planURL(["x"], "archived"), "/plan?label=x&show=archived");
  // Exotic name: comma in label name must round-trip via per-segment escape.
  assert.equal(planURL(["a,b"], "active"), "/plan?label=a%2Cb");
});

test("toggleLabel: add absent, remove present, sorted output", () => {
  assert.deepStrictEqual(toggleLabel(["web"], "alpha"), ["alpha", "web"]);
  assert.deepStrictEqual(toggleLabel(["alpha", "web"], "web"), ["alpha"]);
});

test("addLabel: add absent (sorted); no-op if present", () => {
  assert.deepStrictEqual(addLabel(["web"], "alpha"), ["alpha", "web"]);
  assert.deepStrictEqual(addLabel(["alpha", "web"], "alpha"), ["alpha", "web"]);
});

// --- kind filter (Plan vs. Issues) ---

test("filterRootsByKind: 'issue' keeps only roots whose kind is issue", () => {
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "Plan", status: "available", sortKey: "000001", kind: "task" },
      { shortId: "I0001", title: "Issues", status: "available", sortKey: "000002", kind: "issue" },
      { shortId: "C0001", title: "Bug", status: "available", parentShortId: "I0001", sortKey: "000001" },
    ],
  });
  const roots = buildForestFromFrame(f);
  assert.deepStrictEqual(
    filterRootsByKind(roots, "issue").map((n) => n.task.shortId),
    ["I0001"],
  );
});

test("filterRootsByKind: 'task' drops issue roots and keeps kind-less roots", () => {
  // The frame carries kind on roots only; a root written before kinds
  // existed (or a child surfaced as a root) has kind null and must
  // read as a task tree, matching the Go-side default.
  const f = frameWith({
    tasks: [
      { shortId: "P0001", title: "Plan", status: "available", sortKey: "000001", kind: "task" },
      { shortId: "N0001", title: "No kind", status: "available", sortKey: "000002" },
      { shortId: "I0001", title: "Issues", status: "available", sortKey: "000003", kind: "issue" },
    ],
  });
  const roots = buildForestFromFrame(f);
  assert.deepStrictEqual(
    filterRootsByKind(roots, "task").map((n) => n.task.shortId),
    ["P0001", "N0001"],
  );
});

test("planURL: an explicit base composes /issues URLs with the same rules", () => {
  assert.equal(planURL([], "active", "/issues"), "/issues");
  assert.equal(planURL(["web"], "archived", "/issues"), "/issues?label=web&show=archived");
  assert.equal(planURL([], "all", "/issues/abc12"), "/issues/abc12?show=all");
});

test("buildPlanNodes: inline label URLs follow opts.base", () => {
  const f = frameWith({
    tasks: [
      { shortId: "I0001", title: "Issues", status: "available", sortKey: "000001", kind: "issue", labels: ["web"] },
    ],
  });
  const roots = buildForestFromFrame(f);
  const nodes = buildPlanNodes(roots, f, 1700000000, {
    selected: [],
    show: "active",
    base: "/issues",
  });
  assert.equal(nodes[0].labels[0].url, "/issues?label=web");
});
