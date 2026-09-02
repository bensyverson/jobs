// Tests for the pure pieces of internal/web/assets/js/scrubber-pill.mjs.
//
// Most of the module is DOM-driven (querySelector, attributes, event
// wiring) — exercised manually in the browser. The label-mapping
// helper is the part that has a clean contract and benefits from
// being pinned: the pill's text doubles as its imperative, so a
// regression that flipped "Time travel" with "Return to live" would
// be silent in CI but obvious to a user.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  pillLabelFor,
  toggleVisibility,
  stepPosition,
  classifyKeydown,
  resolveStep,
} from "../assets/js/scrubber-pill.mjs";

// Minimal stub Document. Backs the two queries scrubber-pill makes
// against the real DOM (data-scrubber-strip, data-history-banner) so
// we can assert what the toggle does without pulling in JSDOM.
function fakeDoc() {
  const strip = {
    attrs: { "aria-hidden": "true", inert: "" },
    setAttribute(k, v) { this.attrs[k] = v; },
    removeAttribute(k) { delete this.attrs[k]; },
  };
  const banner = { hidden: true };
  return {
    strip,
    banner,
    querySelector(sel) {
      if (sel === "[data-scrubber-strip]") return strip;
      return null;
    },
    querySelectorAll(sel) {
      if (sel === "[data-history-banner]") return [banner];
      return [];
    },
  };
}

test("pillLabelFor: live mode reads as the affordance to enter scrubbing", () => {
  assert.equal(pillLabelFor("live"), "Time travel");
});

test("pillLabelFor: scrubbing mode reads as the affordance to exit", () => {
  assert.equal(pillLabelFor("scrubbing"), "Return to live");
});

test("pillLabelFor: unknown / missing mode falls back to live", () => {
  // Defensive default — a typo in a caller shouldn't blank the pill.
  assert.equal(pillLabelFor(undefined), "Time travel");
  assert.equal(pillLabelFor(""), "Time travel");
  assert.equal(pillLabelFor("banana"), "Time travel");
});

// --- toggleVisibility ---

test("toggleVisibility(scrubbing=true) reveals strip via aria-hidden + clears inert; unhides banner", () => {
  const doc = fakeDoc();
  toggleVisibility(doc, true);
  assert.equal(doc.strip.attrs["aria-hidden"], "false");
  assert.equal("inert" in doc.strip.attrs, false, "inert should be removed when scrubbing");
  assert.equal(doc.banner.hidden, false);
});

test("toggleVisibility(scrubbing=false) re-hides strip with aria-hidden + inert; rehides banner", () => {
  const doc = fakeDoc();
  // Start in the "open" state to verify the toggle path back to closed.
  toggleVisibility(doc, true);
  toggleVisibility(doc, false);
  assert.equal(doc.strip.attrs["aria-hidden"], "true");
  assert.equal(doc.strip.attrs.inert, "");
  assert.equal(doc.banner.hidden, true);
});

test("toggleVisibility tolerates missing strip / banner (cold pages without scrubber chrome)", () => {
  const doc = { querySelector: () => null, querySelectorAll: () => [] };
  // No throw is the entire contract here — pages without the chrome
  // (e.g. error pages) shouldn't crash the toggle wiring.
  assert.doesNotThrow(() => toggleVisibility(doc, true));
  assert.doesNotThrow(() => toggleVisibility(doc, false));
});

// --- stepPosition (arrow-key navigation) ---

// POS encodes a synthetic cursor for sequence number n.
function POS(n) {
  return `1000-aaaaaa-${n}`;
}

const evs = [
  { position: POS(10) },
  { position: POS(11) },
  { position: POS(12) },
  { position: POS(13) },
];

test("stepPosition: +1 returns the next event's cursor", () => {
  assert.equal(stepPosition(evs, POS(11), 1), POS(12));
});

test("stepPosition: -1 returns the previous event's cursor", () => {
  assert.equal(stepPosition(evs, POS(11), -1), POS(10));
});

test("stepPosition: clamps at the right edge (no wrap-around)", () => {
  assert.equal(stepPosition(evs, POS(13), 1), POS(13));
});

test("stepPosition: clamps at the left edge", () => {
  assert.equal(stepPosition(evs, POS(10), -1), POS(10));
});

test("stepPosition: empty events returns null", () => {
  assert.equal(stepPosition([], POS(10), 1), null);
});

test("stepPosition: an unknown cursor defaults to the edge in the requested direction", () => {
  // Race during initial load: the arrow key fires before applyCursor
  // has set currentPosition. Behave usefully — step from the nearest
  // edge. A row id is one such unknown: it is not a cursor at all.
  assert.equal(stepPosition(evs, null, -1), POS(10));
  assert.equal(stepPosition(evs, null, 1), POS(13));
  assert.equal(stepPosition(evs, POS(99), 1), POS(13));
  assert.equal(stepPosition(evs, 11, 1), POS(13));
});

// --- classifyKeydown (key contract for the scrubbing UI) ---

test("classifyKeydown: keys are inert when not scrubbing", () => {
  // Critical contract: keys must NOT act unless the user is in
  // scrubbing mode. Otherwise typing in the search bar would close
  // the page (Esc), pan (Space), or step the cursor (arrows) — all
  // disasters for a user who isn't even using the scrubber.
  assert.equal(classifyKeydown({ key: "Escape", scrubbing: false }), null);
  assert.equal(classifyKeydown({ key: " ", scrubbing: false }), null);
  assert.equal(classifyKeydown({ key: "ArrowLeft", scrubbing: false }), null);
});

test("classifyKeydown: Escape exits scrubbing", () => {
  assert.deepStrictEqual(classifyKeydown({ key: "Escape", scrubbing: true }), {
    type: "exit",
  });
});

test("classifyKeydown: Space (or Space code) latches pan modifier", () => {
  assert.deepStrictEqual(classifyKeydown({ key: " ", scrubbing: true }), {
    type: "pan-modifier-down",
  });
  // Some keyboards report the code rather than the key character.
  assert.deepStrictEqual(classifyKeydown({ key: "Spacebar", code: "Space", scrubbing: true }), {
    type: "pan-modifier-down",
  });
});

test("classifyKeydown: plain ←/→ steps by one event", () => {
  assert.deepStrictEqual(classifyKeydown({ key: "ArrowLeft", scrubbing: true }), {
    type: "step",
    direction: -1,
  });
  assert.deepStrictEqual(classifyKeydown({ key: "ArrowRight", scrubbing: true }), {
    type: "step",
    direction: 1,
  });
});

test("classifyKeydown: Alt+→ narrows the window (×0.5); Alt+← widens (×2)", () => {
  assert.deepStrictEqual(
    classifyKeydown({ key: "ArrowRight", altKey: true, scrubbing: true }),
    { type: "zoom", factor: 0.5 },
  );
  assert.deepStrictEqual(
    classifyKeydown({ key: "ArrowLeft", altKey: true, scrubbing: true }),
    { type: "zoom", factor: 2 },
  );
});

test("classifyKeydown: unrelated keys pass through", () => {
  // Letters, modifiers alone, function keys must not be claimed by
  // the scrubber — the search bar / global shortcuts depend on them.
  assert.equal(classifyKeydown({ key: "a", scrubbing: true }), null);
  assert.equal(classifyKeydown({ key: "Shift", scrubbing: true }), null);
  assert.equal(classifyKeydown({ key: "Tab", scrubbing: true }), null);
});

// --- resolveStep (race-free arrow-key stepping) ---

const stepEvents = (() => {
  // Five events spaced 1h apart, ending at NOW.
  const NOW_S = 1_700_000_000;
  return [0, 1, 2, 3, 4].map((i) => ({
    position: POS(100 + i),
    created_at: NOW_S - (4 - i) * 3600,
  }));
})();
const STEP_NOW_MS = 1_700_000_000_000;
const STEP_WIN = 24 * 3600 * 1000;
const STEP_START = STEP_NOW_MS - STEP_WIN;

test("resolveStep: returns the next cursor and its xFrac", () => {
  const r = resolveStep(stepEvents, POS(102), -1, STEP_NOW_MS, STEP_WIN, STEP_START);
  assert.equal(r.nextId, POS(101));
  // xFrac for 101 (3h ago) in a 24h window: 21h offset / 24h = 0.875.
  assert.ok(Math.abs(r.xFrac - 0.875) < 1e-6);
});

test("resolveStep: returns null when at the edge (clamped, no movement)", () => {
  // At the leftmost event with direction -1: stepPosition clamps; the
  // resolver tells the caller "no move" so the keydown handler can
  // bail before queuing a redundant frame replay.
  assert.equal(resolveStep(stepEvents, POS(100), -1, STEP_NOW_MS, STEP_WIN, STEP_START), null);
});

test("resolveStep: chained calls from the same starting cursor advance by N", () => {
  // Regression for the ArrowLeft race: rapid keypresses each consult
  // the latest currentPosition. Two consecutive resolves from 104
  // backward must land on 103 then 102 — not 103 twice.
  const first = resolveStep(stepEvents, POS(104), -1, STEP_NOW_MS, STEP_WIN, STEP_START);
  assert.equal(first.nextId, POS(103));
  const second = resolveStep(stepEvents, first.nextId, -1, STEP_NOW_MS, STEP_WIN, STEP_START);
  assert.equal(second.nextId, POS(102));
});
