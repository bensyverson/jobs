// Tests for internal/web/assets/js/log-chips.mjs.
//
// The Log's chip strips are capped by the server, which renders the
// overflow hidden behind a "+N more" chip. This module upgrades that
// chip from a link (?chips=all, a full page render) into an in-place
// expander, and grows the strip when a live event arrives from an
// actor who has no chip yet.
//
// There is no DOM in the node test runner, so the DOM-touching
// helpers are driven against the small fake below — it implements
// exactly the four selectors and the handful of element methods
// log-chips.mjs uses.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  expandStrip,
  logChipHref,
  actorChipMarkup,
  findChip,
  decrementMore,
} from "../assets/js/log-chips.mjs";

// --- fake DOM ---

function el(attrs = {}) {
  return {
    attrs: { ...attrs },
    hidden: "hidden" in attrs,
    textContent: "",
    removed: false,
    parent: null,
    getAttribute(n) {
      return n in this.attrs ? this.attrs[n] : null;
    },
    setAttribute(n, v) {
      this.attrs[n] = v;
    },
    hasAttribute(n) {
      return n in this.attrs;
    },
    removeAttribute(n) {
      delete this.attrs[n];
    },
    remove() {
      this.removed = true;
      if (this.parent) {
        this.parent.children = this.parent.children.filter((c) => c !== this);
      }
    },
  };
}

function matches(node, sel) {
  switch (sel) {
    case "[data-chip-overflow]":
      return node.hasAttribute("data-chip-overflow");
    case "[data-chip-more]":
      return node.hasAttribute("data-chip-more");
    case "[data-actor-chip]":
      return node.hasAttribute("data-actor-chip");
    case "a.c-filter-chip":
      return String(node.attrs.class || "").includes("c-filter-chip");
    default:
      throw new Error("fake DOM: unsupported selector " + sel);
  }
}

function group(children) {
  const g = {
    children,
    querySelectorAll(sel) {
      return this.children.filter((c) => matches(c, sel));
    },
    querySelector(sel) {
      return this.children.find((c) => matches(c, sel)) || null;
    },
  };
  for (const c of children) c.parent = g;
  return g;
}

// cappedStrip builds the shape the server renders: an "any" chip, two
// visible actor chips, two overflow chips, and a "+2 more" chip.
function cappedStrip() {
  return group([
    el({ class: "c-filter-chip" }),
    el({ class: "c-filter-chip", "data-actor-chip": "alice" }),
    el({ class: "c-filter-chip", "data-actor-chip": "bob" }),
    el({ class: "c-filter-chip", "data-actor-chip": "carol", "data-chip-overflow": "", hidden: "" }),
    el({ class: "c-filter-chip", "data-actor-chip": "dan", "data-chip-overflow": "", hidden: "" }),
    el({ class: "c-filter-chip c-filter-chip--more", "data-chip-more": "2" }),
  ]);
}

// --- expandStrip ---

test("expandStrip reveals every overflow chip and reports the count", () => {
  const g = cappedStrip();
  assert.equal(expandStrip(g), 2);

  for (const chip of g.children) {
    assert.equal(chip.hasAttribute("data-chip-overflow"), false);
    assert.equal(chip.hasAttribute("hidden"), false);
    assert.equal(chip.hidden, false);
  }
});

test("expandStrip retires the +N more chip so it cannot be clicked twice", () => {
  const g = cappedStrip();
  expandStrip(g);
  assert.equal(g.querySelector("[data-chip-more]"), null);
});

test("expandStrip on an uncapped strip is a no-op", () => {
  const g = group([
    el({ class: "c-filter-chip" }),
    el({ class: "c-filter-chip", "data-actor-chip": "alice" }),
  ]);
  assert.equal(expandStrip(g), 0);
  assert.equal(g.children.length, 2);
});

test("expandStrip tolerates a page with no chip strip", () => {
  assert.equal(expandStrip(null), 0);
  assert.equal(expandStrip(undefined), 0);
});

// --- logChipHref (mirror of logChipCtx.url) ---

test("logChipHref sets one axis and preserves the rest of the view", () => {
  assert.equal(logChipHref("?range=30d", "actor", "alice"), "/log?actor=alice&range=30d");
  assert.equal(
    logChipHref("?actor=alice&range=all&chips=all", "label", "web"),
    "/log?actor=alice&chips=all&label=web&range=all",
  );
});

test("logChipHref clears an axis when the value is empty", () => {
  assert.equal(logChipHref("?actor=alice&type=done", "actor", ""), "/log?type=done");
  assert.equal(logChipHref("?actor=alice", "actor", ""), "/log");
});

test("logChipHref drops the paging and cursor params — a chip click starts a fresh page", () => {
  assert.equal(logChipHref("?before=42&limit=5&at=9&range=14d", "actor", "bob"), "/log?actor=bob&range=14d");
});

test("logChipHref tolerates a missing search string", () => {
  assert.equal(logChipHref("", "actor", "alice"), "/log?actor=alice");
  assert.equal(logChipHref(undefined, "actor", "alice"), "/log?actor=alice");
});

// --- actorChipMarkup ---

test("actorChipMarkup mirrors the server's actor chip", () => {
  const html = actorChipMarkup("alice", "/log?actor=alice");
  assert.match(html, /class="c-filter-chip"/);
  assert.match(html, /data-actor-chip="alice"/);
  assert.match(html, /class="c-avatar c-avatar-dot" data-actor="alice"/);
  assert.match(html, />alice<\/a>$/);
});

test("actorChipMarkup escapes the actor name and the href", () => {
  const html = actorChipMarkup('a"><script>', "/log?actor=x&y");
  assert.equal(html.includes("<script>"), false);
  assert.match(html, /&amp;y/);
});

// --- findChip / decrementMore ---

test("findChip matches on the attribute value, not a built selector", () => {
  const g = group([
    el({ class: "c-filter-chip" }),
    el({ class: "c-filter-chip", "data-actor-chip": 'quote"actor' }),
  ]);
  assert.equal(findChip(g, 'quote"actor'), g.children[1]);
  assert.equal(findChip(g, "nobody"), null);
  assert.equal(findChip(null, "alice"), null);
});

test("decrementMore counts a promoted chip down and removes the chip at zero", () => {
  const g = cappedStrip();
  decrementMore(g);
  const more = g.querySelector("[data-chip-more]");
  assert.equal(more.getAttribute("data-chip-more"), "1");
  assert.equal(more.textContent, "+1 more");

  decrementMore(g);
  assert.equal(g.querySelector("[data-chip-more]"), null);
});

test("decrementMore is a no-op when the strip was never capped", () => {
  const g = group([el({ class: "c-filter-chip" })]);
  decrementMore(g);
  decrementMore(null);
  assert.equal(g.children.length, 1);
});
