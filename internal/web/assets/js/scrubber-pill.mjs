/*
  Scrubber pill controller — toggles the dashboard between "live" and
  "scrubbing" modes and drives the cursor while scrubbing.

  Live mode is the default: footer shows the "● Live" pill, the strip
  and history banner are hidden, the dashboard's existing *-live.js
  modules handle SSE-driven updates as before. Entering scrubbing mode
  reveals the strip + banner, populates the density bars and cursor,
  and fans out a "jobs:scrubber-frame" CustomEvent on every cursor
  change so per-view modules can swap their DOM (per-view glue lands
  in follow-on commits).

  This module owns:
    - The toggle (click / Esc / "Return to live" buttons).
    - Density bar rendering on first activation.
    - Pointer-driven cursor drag with single-flight frame replays.
    - Banner text composition.

  It does NOT own per-view DOM updates. Listeners attach to the
  document via 'jobs:scrubber-frame' / 'jobs:scrubber-live' events.
*/

import {
  xToPosition,
  positionToX,
  computeDensityBars,
  formatHistoryBannerText,
  parseAtFromQuery,
  composeURLWithAt,
  composeURLWithoutAt,
  panWindowStartMs,
  zoomWindow,
  formatAxisLabels,
  clampWindowEndToNow,
  clampWindowStartToFloor,
  classifyWheelAxis,
  windowForPosition,
} from "./scrubber-cursor.mjs";
import { samePosition } from "./position.mjs";

const PAGE_SCRUBBING_CLASS = "page--scrubbing";
const PAGE_PANNING_CLASS = "page--scrubber-panning";
const ONE_DAY_MS = 24 * 60 * 60 * 1000;

// Module-level state. Populated lazily on first scrubbing entry; the
// dashboard often loads in live mode and never enters the scrubber,
// so we don't pay the events-fetch cost up-front.
let events = [];
let nowMs = 0;
let buf = null;
let initialized = false;

// Single-flight queue for cursor-driven replays. Pointermove can
// fire 60+ times per second; ReplayBuffer.frameAt is async. Without
// gating, promises pile up and DOM updates land out of order.
let inFlightX = null;
let queuedX = null;
// Pinned cursor for the queued/in-flight call. When set, applyCursor
// bypasses xFrac→position resolution and uses it directly. Used by
// arrow-key stepping so the synchronously committed currentPosition
// can't be re-rounded off by float arithmetic in xToPosition.
let inFlightId = null;
let queuedId = null;

// Most recently applied log position. Tracks where the cursor "is" in
// event-space so the arrow keys can step ±1 from a known anchor
// (a re-derive from xFrac would round to whichever event the cursor
// was nearest, which loses the previous step's exact position).
let currentPosition = null;

// Visible window. `windowStartMs === null` means "trailing window
// ending at nowMs" (the default before any pan). Once panned, we
// pin the start so subsequent renders use the same reference point.
let windowMs = ONE_DAY_MS;
let windowStartMs = null;

// Held-modifier latch: pan mode is active while the user holds Space
// or Alt/Option AND drags on the track. Tracked at the document
// level so the cursor changes to the grab affordance even before
// pointerdown.
let panModifierHeld = false;

function findPageRoot(doc) {
  return doc.querySelector(".page") ?? doc.body;
}

// The strip uses aria-hidden + inert (not the `hidden` attribute) so
// the slide-up CSS transition has something to animate against. The
// banner has no transition; plain `hidden` is correct.
export function toggleVisibility(doc, scrubbing) {
  const strip = doc.querySelector("[data-scrubber-strip]");
  if (strip) {
    if (scrubbing) {
      strip.setAttribute("aria-hidden", "false");
      strip.removeAttribute("inert");
    } else {
      strip.setAttribute("aria-hidden", "true");
      strip.setAttribute("inert", "");
    }
  }
  for (const banner of doc.querySelectorAll("[data-history-banner]")) {
    banner.hidden = !scrubbing;
  }
}

// pillLabelFor returns the toggle button's text for each mode. The
// label doubles as the imperative ("click me to do X"), so it flips
// between "Time travel" (live → enter scrubbing) and "Return to
// live" (scrubbing → exit).
export function pillLabelFor(mode) {
  return mode === "scrubbing" ? "Return to live" : "Time travel";
}

function setPillState(doc, mode) {
  const label = doc.querySelector("[data-scrubber-pill-label]");
  if (label) label.textContent = pillLabelFor(mode);
  const pill = doc.querySelector("[data-scrubber-toggle]");
  if (pill) pill.setAttribute("aria-label", pillLabelFor(mode));
}

function setCursor(doc, xFrac) {
  const strip = doc.querySelector("[data-scrubber-strip]");
  if (strip) strip.style.setProperty("--x", `${(xFrac * 100).toFixed(1)}%`);
}

function effectiveWindowStartMs() {
  return windowStartMs ?? nowMs - windowMs;
}

// Earliest known event timestamp, used as the pan/zoom floor so we
// can't drift the window into pre-history. Returns null until events
// have loaded so the floor doesn't snap to something synthetic.
function historyFloorMs() {
  if (events.length === 0) return null;
  return events[0].created_at * 1000;
}

// applyClamps confines the visible window to [historyFloor, now]:
//   1. Shrink windowMs if the proposed window is wider than the
//      recorded history span. Otherwise the user could keep zooming
//      out into empty pre-history once the start has been pinned.
//   2. Pin the start to the floor if it drifted earlier.
//   3. Pin the end to now if it drifted later.
function applyClamps(start, win) {
  const floor = historyFloorMs();
  let nextWin = win;
  if (floor != null) {
    const span = nowMs - floor;
    if (span > 0 && nextWin > span) nextWin = span;
  }
  const a = clampWindowStartToFloor(start, nextWin, floor);
  const b = clampWindowEndToNow(a.windowStartMs, a.windowMs, nowMs);
  return b;
}

function renderAxis(doc) {
  const axis = doc.querySelector(".c-scrubber-strip__axis");
  if (!axis) return;
  const labels = formatAxisLabels(effectiveWindowStartMs(), windowMs, nowMs);
  const positions = [0, 25, 50, 75, 100];
  // Idempotent: keep the existing 5 spans and just rewrite text.
  const spans = axis.querySelectorAll("span");
  if (spans.length === labels.length) {
    for (let i = 0; i < labels.length; i++) spans[i].textContent = labels[i];
    return;
  }
  axis.replaceChildren();
  for (let i = 0; i < labels.length; i++) {
    const span = doc.createElement("span");
    span.style.left = `${positions[i]}%`;
    span.textContent = labels[i];
    axis.appendChild(span);
  }
}

// rerenderViewport refreshes both the density bars and the axis
// labels. Called any time the visible window changes (pan, zoom).
function rerenderViewport(doc) {
  renderDensityBars(doc);
  renderAxis(doc);
}

function renderDensityBars(doc) {
  const container = doc.querySelector("[data-scrubber-bars]");
  if (!container) return;
  const bars = computeDensityBars(events, nowMs, 60, windowMs, effectiveWindowStartMs());
  // Idempotent: only re-render if bar count changed (it won't, but a
  // future opt-in to re-bin on viewport resize could).
  if (container.children.length === bars.length) {
    for (let i = 0; i < bars.length; i++) {
      container.children[i].style.setProperty("--h", `${bars[i]}%`);
    }
    return;
  }
  container.replaceChildren();
  for (const h of bars) {
    const span = doc.createElement("span");
    span.className = "c-scrubber-strip__bar";
    span.style.setProperty("--h", `${h}%`);
    container.appendChild(span);
  }
}

function updateBanner(doc, event) {
  const text = event ? formatHistoryBannerText(event, nowMs) : "—";
  for (const el of doc.querySelectorAll("[data-scrubber-at], [data-history-banner-at]")) {
    el.textContent = text;
  }
}

// syncURL updates the address bar to reflect the current scrub state
// without adding a history entry per drag tick. Browser back/forward
// still works because the state changes on enter/exit are pushed
// (replaceState during drag, pushState on exit). Falls back to a
// no-op when window.history is unavailable (node tests, sandboxed
// embeds).
function syncURL(position, mode = "replace") {
  if (typeof window === "undefined" || !window.history) return;
  const href = window.location.href;
  const next = position == null ? composeURLWithoutAt(href) : composeURLWithAt(href, position);
  const fn = mode === "push" ? "pushState" : "replaceState";
  try {
    window.history[fn]({}, "", next);
  } catch {
    // Cross-origin / sandboxed iframes may reject history ops. Treat
    // as a no-op — the scrubber still works in-page; URL just won't
    // reflect state.
  }
}

async function applyCursor(doc, xFrac, { updateURL = true, pinnedId = null } = {}) {
  if (events.length === 0 || !buf) return;
  const position = pinnedId ?? xToPosition(xFrac, events, nowMs, windowMs, effectiveWindowStartMs());
  if (position == null) return;
  const event = events.find((e) => samePosition(e.position, position));
  const frame = await buf.frameAt(position);
  currentPosition = position;
  updateBanner(doc, event);
  if (updateURL) syncURL(position, "replace");
  doc.dispatchEvent(
    new CustomEvent("jobs:scrubber-frame", { detail: { frame, event } }),
  );
}

// classifyKeydown maps a keypress + modifier state to the action the
// scrubber should take. Returns null when the key isn't one we
// handle so the event passes through to other listeners (typing in
// the search bar, plan-keyboard's tree nav, etc.). Pure: lets the
// keydown handler in wireScrubberPill stay small and the contract
// stay testable without a DOM.
export function classifyKeydown({ key, code, altKey, scrubbing }) {
  if (!scrubbing) return null;
  if (key === "Escape") return { type: "exit" };
  if (key === " " || code === "Space") return { type: "pan-modifier-down" };
  if (key === "ArrowLeft" || key === "ArrowRight") {
    if (altKey) {
      return { type: "zoom", factor: key === "ArrowRight" ? 0.5 : 2 };
    }
    return { type: "step", direction: key === "ArrowLeft" ? -1 : 1 };
  }
  return null;
}

// resolveStep is the race-free wrapper around stepPosition: returns
// both the next cursor and its xFrac in the visible window, or null
// when stepping is a no-op (already at the edge in that direction).
//
// Pure: same inputs → same output. The keydown handler calls this
// once per arrow press and then commits the result *synchronously*
// to currentPosition before kicking off the async frame replay — that
// way two rapid presses each see fresh state and advance by two.
export function resolveStep(events, current, direction, nowMs, windowMs, windowStartMs) {
  const nextId = stepPosition(events, current, direction);
  if (nextId == null || samePosition(nextId, current)) return null;
  const xFrac = positionToX(nextId, events, nowMs, windowMs, windowStartMs);
  return { nextId, xFrac };
}

// stepPosition returns the log position one step earlier (-1) or later
// (+1) from `current`. Pure: takes the current event list + cursor +
// direction, returns the new cursor. Clamps at the array boundaries
// (no wrap-around) so holding the arrow key reaches the edge and
// stops, rather than jumping to the opposite side. When `current`
// isn't in the list (race during initial load), defaults to the
// edge in the requested direction.
export function stepPosition(events, current, direction) {
  if (events.length === 0) return null;
  const idx = events.findIndex((e) => samePosition(e.position, current));
  if (idx === -1) {
    return direction < 0 ? events[0].position : events[events.length - 1].position;
  }
  const next = idx + (direction < 0 ? -1 : 1);
  if (next < 0 || next >= events.length) return current;
  return events[next].position;
}

// setCursorByX runs at most one applyCursor at a time, queuing the
// most recent target while one is in flight. When the in-flight call
// resolves, we apply the queued x (which may itself be replaced
// during that resolution) and loop until queuedX drains.
//
// `pinnedId` (when supplied) bypasses xFrac→position resolution so the
// arrow-key stepper can guarantee its precomputed cursor lands as-is,
// even when float rounding in xToPosition would have rounded down.
async function setCursorByX(doc, xFrac, pinnedId = null) {
  setCursor(doc, xFrac);
  if (inFlightX !== null) {
    queuedX = xFrac;
    queuedId = pinnedId;
    return;
  }
  inFlightX = xFrac;
  inFlightId = pinnedId;
  try {
    await applyCursor(doc, xFrac, { pinnedId });
    while (queuedX !== null) {
      const nextX = queuedX;
      const nextId = queuedId;
      queuedX = null;
      queuedId = null;
      inFlightX = nextX;
      inFlightId = nextId;
      await applyCursor(doc, nextX, { pinnedId: nextId });
    }
  } finally {
    inFlightX = null;
    inFlightId = null;
  }
}

async function ensureInitialized(doc) {
  if (initialized) return;
  initialized = true;
  buf = (typeof window !== "undefined" ? window.JobsScrubber : null) ?? null;
  if (!buf) return;
  nowMs = Date.now();
  try {
    events = await buf.range(null, buf.headFrame.position);
  } catch {
    // If event fetch fails, density bars stay empty; the cursor still
    // toggles visually but frameAt calls are no-ops. The user gets a
    // degraded scrubber rather than a broken page.
    events = [];
  }
  renderDensityBars(doc);
}

export async function enterScrubbing(doc = document, { atPosition = null } = {}) {
  // Reset pan + zoom state so each new scrubbing session starts on
  // the default trailing 24h window. Holding a previous session's
  // viewport would confuse anyone re-entering scrubbing minutes later.
  windowStartMs = null;
  windowMs = ONE_DAY_MS;
  await ensureInitialized(doc);
  // When cold-loading with ?at=<position> for an event older than the
  // default 24h window, widen the window so the event lands inside it.
  // Otherwise positionToX clamps xFrac to 0 and applyCursor's xFrac→
  // position fallback resolves to the wrong event.
  if (atPosition != null && events.length > 0) {
    const w = windowForPosition(atPosition, events, nowMs, ONE_DAY_MS);
    windowStartMs = w.windowStartMs;
    windowMs = w.windowMs;
  }
  rerenderViewport(doc);
  const page = findPageRoot(doc);
  page.classList.add(PAGE_SCRUBBING_CLASS);
  toggleVisibility(doc, true);
  setPillState(doc, "scrubbing");
  // If a specific cursor is requested (cold-load with ?at=<position>),
  // seek there. Otherwise default to "now" — entering scrubbing without
  // a drag means "I want to see what just happened."
  if (atPosition != null && events.length > 0) {
    const xFrac = positionToX(atPosition, events, nowMs, windowMs, effectiveWindowStartMs());
    setCursor(doc, xFrac);
    // Pin the cursor so applyCursor doesn't re-resolve xFrac→position
    // and silently land on a different event when the cursor sits at
    // the window's edge.
    await applyCursor(doc, xFrac, { updateURL: false, pinnedId: atPosition });
  } else {
    setCursor(doc, 1);
    if (events.length > 0) {
      const last = events[events.length - 1];
      currentPosition = last.position;
      updateBanner(doc, last);
    }
  }
}

export function exitScrubbing(doc = document) {
  const page = findPageRoot(doc);
  page.classList.remove(PAGE_SCRUBBING_CLASS);
  toggleVisibility(doc, false);
  setPillState(doc, "live");
  setCursor(doc, 1);
  currentPosition = null;
  syncURL(null, "push");
  doc.dispatchEvent(new CustomEvent("jobs:scrubber-live"));
}

export function isScrubbing(doc = document) {
  return findPageRoot(doc).classList.contains(PAGE_SCRUBBING_CLASS);
}

function isPanModifier(ev) {
  // Space-as-modifier is a deliberate analog of pan in image editors:
  // it doesn't conflict with arrow-key navigation (which uses ←/→
  // with no modifier) and matches the visual cursor cue. Alt/Option
  // is the secondary form so trackpad users without a comfortable
  // Space-hold can still pan.
  return ev.altKey || panModifierHeld;
}

function wireDrag(doc) {
  const track = doc.querySelector("[data-scrubber-track]");
  if (!track) return;
  let dragging = false;
  let panning = false;
  let lastPanX = 0;
  function xFrom(ev) {
    const rect = track.getBoundingClientRect();
    if (rect.width === 0) return 0;
    const raw = (ev.clientX - rect.left) / rect.width;
    return Math.max(0, Math.min(1, raw));
  }
  track.addEventListener("pointerdown", (ev) => {
    if (isPanModifier(ev)) {
      panning = true;
      lastPanX = ev.clientX;
      findPageRoot(doc).classList.add(PAGE_PANNING_CLASS);
      if (track.setPointerCapture) track.setPointerCapture(ev.pointerId);
      ev.preventDefault();
      return;
    }
    dragging = true;
    if (track.setPointerCapture) track.setPointerCapture(ev.pointerId);
    setCursorByX(doc, xFrom(ev));
  });
  track.addEventListener("pointermove", (ev) => {
    if (panning) {
      const dx = ev.clientX - lastPanX;
      lastPanX = ev.clientX;
      const rect = track.getBoundingClientRect();
      const proposed = panWindowStartMs(
        effectiveWindowStartMs(),
        dx,
        rect.width,
        windowMs,
      );
      const clamped = applyClamps(proposed, windowMs);
      windowStartMs = clamped.windowStartMs;
      rerenderViewport(doc);
      // Keep the cursor anchored to its event in the new window.
      if (currentPosition != null) {
        const xFrac = positionToX(currentPosition, events, nowMs, windowMs, effectiveWindowStartMs());
        setCursor(doc, xFrac);
      }
      return;
    }
    if (!dragging) return;
    setCursorByX(doc, xFrom(ev));
  });
  function stopDrag(ev) {
    dragging = false;
    if (panning) {
      panning = false;
      findPageRoot(doc).classList.remove(PAGE_PANNING_CLASS);
    }
    if (track.releasePointerCapture) {
      try {
        track.releasePointerCapture(ev.pointerId);
      } catch {
        // Already released; ignore.
      }
    }
  }
  track.addEventListener("pointerup", stopDrag);
  track.addEventListener("pointercancel", stopDrag);

  // Wheel over the track is two gestures sharing one event stream:
  //   - vertical scroll → zoom (anchored on pointer)
  //   - horizontal scroll → pan
  //   - pinch (macOS reports it as wheel + ctrlKey) → zoom regardless
  //     of axis
  // classifyWheelAxis picks one axis at the start of the gesture and
  // locks it for the rest, with a 120ms idle gap separating gestures.
  // Without the lock, a slightly noisy two-finger swipe flips between
  // pan and zoom mid-stride.
  let wheelAxis = null;
  let wheelLastTs = null;
  track.addEventListener(
    "wheel",
    (ev) => {
      if (!isScrubbing(doc)) return;
      ev.preventDefault();
      const cls = classifyWheelAxis({
        deltaX: ev.deltaX,
        deltaY: ev.deltaY,
        ctrlKey: ev.ctrlKey,
        timeStamp: ev.timeStamp,
        lastTimeStamp: wheelLastTs,
        lastAxis: wheelAxis,
      });
      if (cls.axis == null) return;
      if (cls.locked) {
        wheelAxis = cls.axis;
        wheelLastTs = ev.timeStamp;
      }
      const rect = track.getBoundingClientRect();
      if (cls.axis === "x") {
        // Horizontal swipe pans the visible window. Sign matches the
        // pointer-pan convention: positive deltaX (content moves left)
        // pulls newer content into view, so windowStart shifts forward.
        const proposed = panWindowStartMs(
          effectiveWindowStartMs(),
          -ev.deltaX,
          rect.width,
          windowMs,
        );
        const clamped = applyClamps(proposed, windowMs);
        windowStartMs = clamped.windowStartMs;
      } else {
        // Anchor the zoom on the time cursor itself, not the pointer.
        // The cursor is the "thing the user is reading" — keeping it
        // pinned under the same screen-x as the window grows/shrinks
        // matches the "this event stays put" mental model. Falls back
        // to the midpoint when no cursor has been established yet.
        const anchorX = currentPosition != null
          ? positionToX(currentPosition, events, nowMs, windowMs, effectiveWindowStartMs())
          : 0.5;
        // deltaY > 0 means "scroll down" — convention is zoom out.
        const factor = ev.deltaY > 0 ? 1.1 : 0.9;
        const next = zoomWindow(effectiveWindowStartMs(), windowMs, anchorX, factor);
        const clamped = applyClamps(next.windowStartMs, next.windowMs);
        windowStartMs = clamped.windowStartMs;
        windowMs = clamped.windowMs;
      }
      rerenderViewport(doc);
      // Keep the cursor anchored to its event in the new window.
      if (currentPosition != null) {
        const xFrac = positionToX(currentPosition, events, nowMs, windowMs, effectiveWindowStartMs());
        setCursor(doc, xFrac);
      }
    },
    { passive: false },
  );
}

export function wireScrubberPill(doc = document) {
  const pill = doc.querySelector("[data-scrubber-toggle]");
  if (pill) {
    pill.addEventListener("click", () => {
      if (isScrubbing(doc)) exitScrubbing(doc);
      else enterScrubbing(doc);
    });
  }
  for (const btn of doc.querySelectorAll("[data-scrubber-return]")) {
    btn.addEventListener("click", () => exitScrubbing(doc));
  }
  doc.addEventListener("keydown", (e) => {
    const action = classifyKeydown({
      key: e.key,
      code: e.code,
      altKey: e.altKey,
      scrubbing: isScrubbing(doc),
    });
    if (!action) return;
    switch (action.type) {
      case "exit":
        exitScrubbing(doc);
        return;
      case "pan-modifier-down":
        // Don't preventDefault — if the user is typing in an input,
        // Space must still produce a space character. The track's
        // pointerdown is the only place we actually consume the
        // modifier.
        panModifierHeld = true;
        findPageRoot(doc).classList.add(PAGE_PANNING_CLASS);
        return;
      case "step": {
        e.preventDefault();
        e.stopPropagation();
        const step = resolveStep(
          events,
          currentPosition,
          action.direction,
          nowMs,
          windowMs,
          effectiveWindowStartMs(),
        );
        if (!step) return;
        // Commit synchronously so a second keydown that fires before
        // the frame-replay promise resolves sees the new id, not the
        // stale one. Without this, rapid ←/← presses pile up against
        // the same currentPosition and only advance by one total.
        currentPosition = step.nextId;
        setCursorByX(doc, step.xFrac, step.nextId);
        return;
      }
      case "zoom": {
        e.preventDefault();
        e.stopPropagation();
        const anchorX = currentPosition != null
          ? positionToX(currentPosition, events, nowMs, windowMs, effectiveWindowStartMs())
          : 0.5;
        const next = zoomWindow(effectiveWindowStartMs(), windowMs, anchorX, action.factor);
        const clamped = applyClamps(next.windowStartMs, next.windowMs);
        windowStartMs = clamped.windowStartMs;
        windowMs = clamped.windowMs;
        rerenderViewport(doc);
        if (currentPosition != null) {
          const xFrac = positionToX(currentPosition, events, nowMs, windowMs, effectiveWindowStartMs());
          setCursor(doc, xFrac);
        }
        return;
      }
    }
  });
  doc.addEventListener("keyup", (e) => {
    if (e.key === " " || e.code === "Space") {
      panModifierHeld = false;
      // Keep the panning class while a pointer drag is mid-flight; the
      // pointerup handler will clear it. If no drag is in flight, the
      // class will fall off here.
      const page = findPageRoot(doc);
      if (page) page.classList.remove(PAGE_PANNING_CLASS);
    }
  });
  wireDrag(doc);

  // Browser back/forward sync. popstate fires when the user navigates
  // through history; check the URL and re-enter or exit accordingly.
  if (typeof window !== "undefined") {
    window.addEventListener("popstate", () => {
      const at = parseAtFromQuery(window.location.search);
      if (at != null) {
        enterScrubbing(doc, { atPosition: at });
      } else if (isScrubbing(doc)) {
        exitScrubbing(doc);
      }
    });
  }
}

// hydrateFromURL is the cold-load entry point: if the page loaded
// with ?at=<position> in the URL, enter scrubbing mode and seek there. Called
// once from the auto-init below; exposed for tests.
export function hydrateFromURL(doc = document, search = "") {
  const at = parseAtFromQuery(search);
  if (at == null) return;
  enterScrubbing(doc, { atPosition: at });
}

if (typeof document !== "undefined") {
  function init() {
    wireScrubberPill();
    if (typeof window !== "undefined") {
      hydrateFromURL(document, window.location.search);
    }
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
