// Tests for the plan-progress painter in
// internal/web/assets/js/colors.js.
//
// colors.js is a classic script, not a module: it is loaded here by
// evaluating the real file with `document` and `window` bound to the
// tiny DOM below, so the test exercises the shipped code rather than a
// copy of it. The stub implements only what paintProgress touches —
// class lists, direct-child and descendant selectors, sibling walks,
// appendChild — which is enough to answer the one structural question
// the painter asks: is this row a depth-0 row inside an issue view?

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

// --- minimal DOM ---

function matchesSelector(el, sel) {
  const attr = sel.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
  if (attr) {
    const v = el.getAttribute(attr[1]);
    if (v === null) return false;
    return attr[2] === undefined || v === attr[2];
  }
  const parts = sel.split(".");
  const tag = parts[0];
  if (tag && el.tagName !== tag.toUpperCase()) return false;
  return parts.slice(1).every((c) => el.classList.contains(c));
}

class El {
  constructor(tag, className = "", attrs = {}) {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parentNode = null;
    this.textContent = "";
    this._classes = className ? className.split(/\s+/).filter(Boolean) : [];
    this._attrs = { ...attrs };
    const props = {};
    this.style = {
      setProperty: (k, v) => {
        props[k] = v;
      },
      getPropertyValue: (k) => props[k] ?? "",
    };
    this.classList = {
      contains: (c) => this._classes.includes(c),
      add: (c) => {
        if (!this._classes.includes(c)) this._classes.push(c);
      },
    };
  }

  get className() {
    return this._classes.join(" ");
  }

  set className(v) {
    this._classes = String(v).split(/\s+/).filter(Boolean);
  }

  getAttribute(name) {
    return name in this._attrs ? this._attrs[name] : null;
  }

  setAttribute(name, value) {
    this._attrs[name] = String(value);
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }

  get nextElementSibling() {
    if (!this.parentNode) return null;
    const sibs = this.parentNode.children;
    return sibs[sibs.indexOf(this) + 1] ?? null;
  }

  descendants() {
    const out = [];
    for (const c of this.children) {
      out.push(c, ...c.descendants());
    }
    return out;
  }

  querySelectorAll(sel) {
    const scoped = sel.match(/^:scope\s*>\s*(.+)$/);
    if (scoped) return this.children.filter((c) => matchesSelector(c, scoped[1]));
    return this.descendants().filter((d) => matchesSelector(d, sel));
  }

  querySelector(sel) {
    return this.querySelectorAll(sel)[0] ?? null;
  }

  closest(sel) {
    let node = this;
    while (node) {
      if (matchesSelector(node, sel)) return node;
      node = node.parentNode;
    }
    return null;
  }
}

function makeDocument(root) {
  return {
    readyState: "complete",
    addEventListener() {},
    createElement: (tag) => new El(tag),
    querySelectorAll: (sel) => root.querySelectorAll(sel),
    querySelector: (sel) => root.querySelector(sel),
  };
}

const COLORS_SRC = readFileSync(
  new URL("../assets/js/colors.js", import.meta.url),
  "utf8",
);

// loadColors evaluates the production script against a stub document
// and returns its window.JobsColors export. Evaluating it also runs
// the DOMContentLoaded-equivalent pass, so the tree is painted once
// before the caller touches it.
function loadColors(doc) {
  const win = {};
  new Function("document", "window", COLORS_SRC)(doc, win);
  return win.JobsColors;
}

// --- fixture ---
//
// One root row with a branch child and two leaves, in the shape the
// plan-node template emits: row, then its .c-plan-subtree sibling.
//
//   root (branch)
//     child (branch)
//       leaf, done
//       leaf, todo
//
// Both branches therefore roll up to "1 of 2 tasks done".

function branchRow(shortID, status) {
  const row = new El("div", `c-plan-row c-plan-row--status-${status}`, {
    id: `task-${shortID}`,
  });
  row.appendChild(new El("button", "c-plan-row__disclosure"));
  row.appendChild(new El("div", "c-plan-row__title"));
  return row;
}

function leafRow(shortID, status) {
  const row = new El("div", `c-plan-row c-plan-row--status-${status}`, {
    id: `task-${shortID}`,
  });
  row.appendChild(new El("div", "c-plan-row__title"));
  return row;
}

function planTree(view) {
  const section = new El("section", "c-section", { "data-plan-view": view });
  const stack = section.appendChild(new El("div", "stack stack-gap-xs"));

  const root = stack.appendChild(branchRow("root1", "todo"));
  const rootSubtree = stack.appendChild(new El("div", "c-plan-subtree"));

  const child = rootSubtree.appendChild(branchRow("child", "todo"));
  const childSubtree = rootSubtree.appendChild(new El("div", "c-plan-subtree"));
  childSubtree.appendChild(leafRow("leaf1", "done"));
  childSubtree.appendChild(leafRow("leaf2", "todo"));

  return { section, root, child };
}

function progressOf(row) {
  const bar = row.querySelector(":scope > .c-plan-row__progress");
  const title = row.querySelector(":scope > .c-plan-row__title");
  const text = title ? title.querySelector(".c-plan-row__progress-text") : null;
  return {
    hasBar: bar !== null,
    label: bar ? bar.getAttribute("aria-label") : null,
    text: text ? text.textContent : null,
  };
}

// --- tests ---

test("paintProgress: a Plan root row gets the rollup bar and its text", () => {
  const tree = planTree("task");
  loadColors(makeDocument(tree.section));

  assert.deepStrictEqual(progressOf(tree.root), {
    hasBar: true,
    label: "1 of 2 complete",
    text: "1 of 2 tasks done",
  });
});

test("paintProgress: an issue root row gets no bar and no progress text", () => {
  // A pile has no denominator: the issue root is not a decomposition
  // that can be N/M finished, and its throughput already lives in the
  // view's "N open · M closed in 7d" meta line.
  const tree = planTree("issue");
  loadColors(makeDocument(tree.section));

  assert.deepStrictEqual(progressOf(tree.root), {
    hasBar: false,
    label: null,
    text: null,
  });
});

test("paintProgress: a branch inside an issue keeps its rollup bar", () => {
  // Only the root is a pile. Below it the tree is ordinary work, and a
  // bug with subtasks is a decomposition like any other.
  const tree = planTree("issue");
  loadColors(makeDocument(tree.section));

  assert.deepStrictEqual(progressOf(tree.child), {
    hasBar: true,
    label: "1 of 2 complete",
    text: "1 of 2 tasks done",
  });
});

test("paintProgress: the Plan view paints depth-0 and deeper rows alike", () => {
  const tree = planTree("task");
  loadColors(makeDocument(tree.section));

  assert.equal(progressOf(tree.child).hasBar, true);
});

test("paintProgress: is idempotent — a second pass adds no second bar", () => {
  const tree = planTree("task");
  const colors = loadColors(makeDocument(tree.section));
  colors.paintProgress(tree.section);

  assert.equal(tree.root.querySelectorAll(":scope > .c-plan-row__progress").length, 1);
});
