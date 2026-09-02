// Tests for internal/web/assets/js/plan-scrub-render.mjs.
//
// The render layer is a pure HTML-string emitter. Driver code parses
// the result with DOMParser and swaps the <section> into the live
// page (mirrors plan-live.js's fetch-and-swap idiom). String output
// keeps tests DOM-free.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  renderPlanSection,
  renderFilterBar,
  escapeHTML,
} from "../assets/js/plan-scrub-render.mjs";

// --- escapeHTML ---

test("escapeHTML: escapes <, >, &, \", '", () => {
  assert.equal(escapeHTML(`<a href="x" class='y'>&copy;</a>`),
    "&lt;a href=&#34;x&#34; class=&#39;y&#39;&gt;&amp;copy;&lt;/a&gt;");
});

// --- renderPlanSection ---

test("renderPlanSection: empty roots emit the c-plan-empty placeholder", () => {
  const html = renderPlanSection([]);
  // The section now carries the view's data attributes (the SSR
  // template does the same), so the open tag is no longer bare.
  assert.match(html, /<section class="c-section" aria-label="Plan" data-plan-view="task"/);
  assert.match(html, /c-plan-empty/);
  assert.match(html, /No active tasks\./);
});

test("renderPlanSection: one node emits a row with id, status pill, and labels", () => {
  const node = {
    shortID: "ABC12",
    url: "/tasks/ABC12",
    title: "Hello & world",
    description: "",
    displayStatus: "todo",
    actor: "alice",
    labels: [{ name: "web", url: "/plan?label=web" }],
    relTime: "5m",
    isoTime: "2026-04-26T12:00:00.000Z",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: false,
    collapsed: false,
  };
  const html = renderPlanSection([node]);
  assert.match(html, /id="task-ABC12"/);
  assert.match(html, /data-plan-task="ABC12"/);
  assert.match(html, /c-status-pill--todo/);
  // HTML-escaped title.
  assert.match(html, /Hello &amp; world/);
  // Avatar slot with actor.
  assert.match(html, /data-actor="alice"/);
  // Label chip with URL + name.
  assert.match(html, /href="\/plan\?label=web"/);
  assert.match(html, /data-label="web"/);
  // Time element with iso datetime + rel text.
  assert.match(html, /<time datetime="2026-04-26T12:00:00\.000Z">5m<\/time>/);
});

test("renderPlanSection: collapsible row emits disclosure button, leaf gets placeholder span", () => {
  const leaf = {
    shortID: "L0001",
    url: "/tasks/L0001",
    title: "leaf",
    description: "",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: false,
    collapsed: false,
  };
  const branch = { ...leaf, shortID: "B0001", title: "branch", hasChildren: true, collapsible: true };
  const html = renderPlanSection([branch, leaf]);
  // Branch has a disclosure button.
  assert.match(html, /<button class="c-plan-row__disclosure"/);
  // Leaf row carries a placeholder span (the empty <span></span> for grid alignment).
  assert.match(html, /id="task-L0001"[^>]*>\s*<span><\/span>/);
});

test("renderPlanSection: collapsed row carries data-collapsed=true and the c-plan-row--collapsed class", () => {
  const node = {
    shortID: "ABC12",
    url: "/tasks/ABC12",
    title: "T",
    description: "",
    displayStatus: "done",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: true,
    collapsed: true,
  };
  const html = renderPlanSection([node]);
  assert.match(html, /data-collapsed="true"/);
  assert.match(html, /c-plan-row--collapsed/);
});

test("renderPlanSection: blocked-by line lists comma-separated id pills with title attrs", () => {
  const node = {
    shortID: "T0001",
    url: "/tasks/T0001",
    title: "T",
    description: "",
    displayStatus: "blocked",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [
      { shortID: "B0001", url: "#task-B0001", title: "Blocker A" },
      { shortID: "B0002", url: "#task-B0002", title: "" },
    ],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: false,
    collapsed: false,
  };
  const html = renderPlanSection([node]);
  assert.match(html, /Blocked by/);
  assert.match(html, /href="#task-B0001"[^>]*title="Blocker A"/);
  // No title attribute when title is empty.
  assert.match(html, /href="#task-B0002"[^>]*>B0002</);
  assert.doesNotMatch(html, /href="#task-B0002"[^>]*title=/);
});

test("renderPlanSection: notes group renders as <details> with one row per note", () => {
  const node = {
    shortID: "T0001",
    url: "/tasks/T0001",
    title: "T",
    description: "",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [
      {
        actor: "alice",
        relTime: "1m",
        isoTime: "2026-04-26T12:00:00.000Z",
        text: "first <note>",
        displayStatus: "todo",
      },
      {
        actor: "bob",
        relTime: "30s",
        isoTime: "2026-04-26T12:01:00.000Z",
        text: "second",
        displayStatus: "todo",
      },
    ],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: true,
    collapsed: false,
  };
  const html = renderPlanSection([node]);
  assert.match(html, /<details class="c-plan-notes-group"/);
  assert.match(html, /2 notes/);
  // Note text is HTML-escaped inside the <pre>.
  assert.match(html, /first &lt;note&gt;/);
  // Each note renders an actor + time.
  assert.match(html, /c-plan-note__actor">alice</);
  assert.match(html, /c-plan-note__actor">bob</);
});

test("renderPlanSection: subtree renders children recursively under c-plan-subtree", () => {
  const child = {
    shortID: "C0001",
    url: "/tasks/C0001",
    title: "child",
    description: "",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 1,
    hasChildren: false,
    collapsible: false,
    collapsed: false,
  };
  const parent = {
    shortID: "P0001",
    url: "/tasks/P0001",
    title: "parent",
    description: "",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [child],
    depth: 0,
    hasChildren: true,
    collapsible: true,
    collapsed: false,
  };
  const html = renderPlanSection([parent]);
  assert.match(html, /<div class="c-plan-subtree">/);
  assert.match(html, /id="task-C0001"/);
  // Parent row appears before child row.
  assert.ok(html.indexOf("task-P0001") < html.indexOf("task-C0001"));
});

test("renderPlanSection: status pill icon + label match the SSR template", () => {
  const make = (status) => ({
    shortID: "T",
    url: "/tasks/T",
    title: "x",
    description: "",
    displayStatus: status,
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: false,
    collapsed: false,
  });
  // Each status maps to its label.
  for (const [status, label] of [
    ["done", "Done"],
    ["blocked", "Blocked"],
    ["active", "Active"],
    ["canceled", "Canceled"],
    ["todo", "Todo"],
  ]) {
    const html = renderPlanSection([make(status)]);
    assert.match(html, new RegExp(`c-status-pill c-status-pill--${status}`));
    assert.match(html, new RegExp(`>${label}<`));
  }
});

// --- renderFilterBar ---

test("renderFilterBar: emits Active/Archived/All tabs with active class on current show", () => {
  const html = renderFilterBar({
    showTabs: [
      { label: "Active", url: "/plan", active: true },
      { label: "Archived", url: "/plan?show=archived", active: false },
      { label: "All", url: "/plan?show=all", active: false },
    ],
    allURL: "/plan",
    allActive: true,
    labels: [],
  });
  assert.match(html, /<a href="\/plan" class="c-tab c-tab--active">Active<\/a>/);
  assert.match(html, /<a href="\/plan\?show=archived" class="c-tab">Archived<\/a>/);
});

test("renderFilterBar: emits label pills with active class and any-pill", () => {
  const html = renderFilterBar({
    showTabs: [],
    allURL: "/plan",
    allActive: false,
    labels: [
      { name: "web", url: "/plan?label=web", active: true },
      { name: "alpha", url: "/plan?label=alpha,web", active: false },
    ],
  });
  assert.match(html, /c-label-pill c-label-pill--all">any<\/a>/);
  assert.match(html, /c-label-pill c-label-pill--active" data-label="web"/);
  assert.match(html, /c-label-pill" data-label="alpha"/);
});

// --- view parameterisation (Plan vs. Issues) ---

test("renderFilterBar: labels the filter nav for the view and omits the meta when empty", () => {
  const html = renderFilterBar({
    showTabs: [],
    allURL: "/plan",
    allActive: true,
    labels: [],
  });
  assert.match(html, /<nav class="c-tabs" aria-label="Plan filter">/);
  assert.ok(!html.includes("c-view-meta"));
});

test("renderFilterBar: emits the view meta line when one is supplied", () => {
  const html = renderFilterBar({
    showTabs: [],
    allURL: "/issues",
    allActive: true,
    labels: [],
    filterLabel: "Issues filter",
    meta: "3 open · 5 closed in 7d",
  });
  assert.match(html, /<nav class="c-tabs" aria-label="Issues filter">/);
  assert.match(html, /<span class="c-view-meta" data-view-meta>3 open · 5 closed in 7d<\/span>/);
});

test("renderPlanSection: defaults to the Plan view's section attributes", () => {
  const html = renderPlanSection([]);
  assert.match(html, /aria-label="Plan"/);
  assert.match(html, /data-plan-view="task"/);
  assert.match(html, /data-plan-base="\/plan"/);
  assert.match(html, /No active tasks\./);
});

test("renderPlanSection: takes the section attributes and empty text from the view", () => {
  const html = renderPlanSection([], {
    label: "Issues",
    kind: "issue",
    base: "/issues/abc12",
    emptyText: "No open issues.",
  });
  assert.match(html, /aria-label="Issues"/);
  assert.match(html, /data-plan-view="issue"/);
  assert.match(html, /data-plan-base="\/issues\/abc12"/);
  assert.match(html, /No open issues\./);
});

// --- description prose ---

test("renderPlanSection: row description renders as prose blocks", () => {
  const html = renderPlanSection([
    {
      shortID: "abc12",
      url: "/tasks/abc12",
      title: "T",
      description: "one\ntwo\n\n- a\n- b",
      displayStatus: "todo",
      actor: "",
      labels: [],
      relTime: "",
      isoTime: "",
      blockedBy: [],
      notes: [],
      children: [],
      depth: 0,
      hasChildren: false,
      collapsible: true,
      collapsed: false,
    },
  ]);
  assert.ok(
    html.includes(
      `<div class="c-plan-row__desc c-prose"><p>one two</p><ul><li>a</li><li>b</li></ul></div>`,
    ),
    html,
  );
});

// --- prose links on scrubbed rows ---

test("renderPlanSection: a row description links the ids in the links map", () => {
  const node = {
    shortID: "ABC12",
    url: "/tasks/ABC12",
    title: "Mentions another task",
    description: "blocked on XY9Qr2 until `XY9Qr2` lands; banana is not a task",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "5m",
    isoTime: "2026-04-26T12:00:00.000Z",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: true,
    collapsed: false,
  };
  const html = renderPlanSection([node], {}, { XY9Qr2: "/tasks/XY9Qr2" });
  assert.match(html, /blocked on <a href="\/tasks\/XY9Qr2">XY9Qr2<\/a> until/);
  assert.match(html, /<a href="\/tasks\/XY9Qr2"><code>XY9Qr2<\/code><\/a>/);
  assert.doesNotMatch(html, /banana<\/a>/);
});

test("renderPlanSection: without a links map a description links nothing", () => {
  const node = {
    shortID: "ABC12",
    url: "/tasks/ABC12",
    title: "t",
    description: "blocked on XY9Qr2",
    displayStatus: "todo",
    actor: "",
    labels: [],
    relTime: "",
    isoTime: "",
    blockedBy: [],
    notes: [],
    children: [],
    depth: 0,
    hasChildren: false,
    collapsible: true,
    collapsed: false,
  };
  assert.match(renderPlanSection([node]), /<p>blocked on XY9Qr2<\/p>/);
});
