// Tests for internal/web/assets/js/shortcuts.mjs.
//
// Global keyboard shortcuts: 1-5 jump to the five primary tabs, "`"
// cycles forward through them, "~" cycles backward. Pure helpers
// (pathFromKey, cyclePath, shouldIgnoreShortcut) are exercised here;
// bindShortcuts is also tested with a fake document/navigator so the
// full keydown contract — input-context guard, modifier-key bail, key
// dispatch — has coverage without a browser.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  TAB_PATHS,
  pathFromKey,
  cyclePath,
  shouldIgnoreShortcut,
  bindShortcuts,
} from "../assets/js/shortcuts.mjs";

// --- TAB_PATHS ---

test("TAB_PATHS lists the five primary tabs in header order", () => {
  // Issues sits after Plan in the header (2026-08-28-issues-ux.md,
  // decision 7), so the number keys shift Actors and Log along.
  assert.deepEqual(TAB_PATHS, ["/", "/plan", "/issues", "/actors", "/log"]);
});

// --- pathFromKey ---

test("pathFromKey maps '1'..'5' to the five tab paths", () => {
  assert.equal(pathFromKey("1"), "/");
  assert.equal(pathFromKey("2"), "/plan");
  assert.equal(pathFromKey("3"), "/issues");
  assert.equal(pathFromKey("4"), "/actors");
  assert.equal(pathFromKey("5"), "/log");
});

test("pathFromKey returns null for keys outside 1-5", () => {
  assert.equal(pathFromKey("0"), null);
  assert.equal(pathFromKey("6"), null);
  assert.equal(pathFromKey("a"), null);
  assert.equal(pathFromKey(""), null);
  assert.equal(pathFromKey(undefined), null);
});

// --- cyclePath ---

test("cyclePath forward advances one tab and wraps from last to first", () => {
  assert.equal(cyclePath("/", 1), "/plan");
  assert.equal(cyclePath("/plan", 1), "/issues");
  assert.equal(cyclePath("/issues", 1), "/actors");
  assert.equal(cyclePath("/actors", 1), "/log");
  assert.equal(cyclePath("/log", 1), "/");
});

test("cyclePath backward retreats one tab and wraps from first to last", () => {
  assert.equal(cyclePath("/plan", -1), "/");
  assert.equal(cyclePath("/", -1), "/log");
  assert.equal(cyclePath("/log", -1), "/actors");
  assert.equal(cyclePath("/actors", -1), "/issues");
});

test("cyclePath from an unknown path lands on Home (forward) or Log (backward)", () => {
  // Sub-pages like /tasks/abc123 aren't in TAB_PATHS; treat them as
  // "outside the tab strip" and seed at the appropriate end.
  assert.equal(cyclePath("/tasks/abc123", 1), "/");
  assert.equal(cyclePath("/tasks/abc123", -1), "/log");
  assert.equal(cyclePath("", 1), "/");
});

// --- shouldIgnoreShortcut ---

test("shouldIgnoreShortcut returns true for INPUT, TEXTAREA, SELECT", () => {
  assert.equal(shouldIgnoreShortcut({ tagName: "INPUT" }), true);
  assert.equal(shouldIgnoreShortcut({ tagName: "TEXTAREA" }), true);
  assert.equal(shouldIgnoreShortcut({ tagName: "SELECT" }), true);
});

test("shouldIgnoreShortcut returns true for contentEditable elements", () => {
  assert.equal(shouldIgnoreShortcut({ tagName: "DIV", isContentEditable: true }), true);
});

test("shouldIgnoreShortcut returns false for plain DIV / BUTTON / null", () => {
  assert.equal(shouldIgnoreShortcut({ tagName: "DIV", isContentEditable: false }), false);
  assert.equal(shouldIgnoreShortcut({ tagName: "BUTTON" }), false);
  assert.equal(shouldIgnoreShortcut(null), false);
});

// --- bindShortcuts (DOM-bound controller) ---

function fakeDoc() {
  let handler = null;
  return {
    addEventListener(type, fn) {
      if (type === "keydown") handler = fn;
    },
    fire(event) {
      if (!handler) throw new Error("bindShortcuts never registered keydown");
      handler(event);
    },
  };
}

function ev(over = {}) {
  let prevented = false;
  return {
    key: over.key,
    metaKey: !!over.metaKey,
    ctrlKey: !!over.ctrlKey,
    altKey: !!over.altKey,
    shiftKey: !!over.shiftKey,
    target: over.target || { tagName: "BODY" },
    preventDefault() { prevented = true; },
    get prevented() { return prevented; },
  };
}

test("bindShortcuts: number keys navigate to the matching tab and preventDefault", () => {
  // Seed currentPath to a sub-page so every tab key is a real
  // navigation (the no-op-when-already-there branch has its own
  // test below).
  const doc = fakeDoc();
  const calls = [];
  bindShortcuts({ document: doc, navigate: (u) => calls.push(u), getCurrentPath: () => "/tasks/abc" });

  const cases = [
    ["1", "/"],
    ["2", "/plan"],
    ["3", "/issues"],
    ["4", "/actors"],
    ["5", "/log"],
  ];
  for (const [key, want] of cases) {
    const e = ev({ key });
    doc.fire(e);
    assert.equal(calls.at(-1), want, `key ${key}`);
    assert.equal(e.prevented, true, `key ${key} should preventDefault`);
  }
});

test("bindShortcuts: backtick cycles forward, tilde cycles backward", () => {
  const doc = fakeDoc();
  const calls = [];
  let path = "/plan";
  bindShortcuts({ document: doc, navigate: (u) => { calls.push(u); path = u; }, getCurrentPath: () => path });

  doc.fire(ev({ key: "`" }));
  assert.equal(calls.at(-1), "/issues");

  doc.fire(ev({ key: "~", shiftKey: true }));
  assert.equal(calls.at(-1), "/plan");
});

test("bindShortcuts: bails when the focused element is an input or contenteditable", () => {
  const doc = fakeDoc();
  const calls = [];
  bindShortcuts({ document: doc, navigate: (u) => calls.push(u), getCurrentPath: () => "/" });

  for (const target of [
    { tagName: "INPUT" },
    { tagName: "TEXTAREA" },
    { tagName: "SELECT" },
    { tagName: "DIV", isContentEditable: true },
  ]) {
    const e = ev({ key: "2", target });
    doc.fire(e);
    assert.equal(e.prevented, false);
  }
  assert.deepEqual(calls, []);
});

test("bindShortcuts: bails on Cmd/Ctrl/Alt combos so browser shortcuts pass through", () => {
  const doc = fakeDoc();
  const calls = [];
  bindShortcuts({ document: doc, navigate: (u) => calls.push(u), getCurrentPath: () => "/" });

  for (const mod of [{ metaKey: true }, { ctrlKey: true }, { altKey: true }]) {
    const e = ev({ key: "2", ...mod });
    doc.fire(e);
    assert.equal(e.prevented, false);
  }
  assert.deepEqual(calls, []);
});

test("bindShortcuts: does not navigate or preventDefault for unrelated keys", () => {
  const doc = fakeDoc();
  const calls = [];
  bindShortcuts({ document: doc, navigate: (u) => calls.push(u), getCurrentPath: () => "/" });

  for (const key of ["a", "6", "Enter", "Escape", "ArrowDown"]) {
    const e = ev({ key });
    doc.fire(e);
    assert.equal(e.prevented, false, `key ${key}`);
  }
  assert.deepEqual(calls, []);
});

test("bindShortcuts: does not re-navigate when already on the target tab", () => {
  // Pressing "2" on /plan should be a no-op; otherwise we'd churn
  // history with redundant pushState entries.
  const doc = fakeDoc();
  const calls = [];
  bindShortcuts({ document: doc, navigate: (u) => calls.push(u), getCurrentPath: () => "/plan" });

  doc.fire(ev({ key: "2" }));
  assert.deepEqual(calls, []);
});
