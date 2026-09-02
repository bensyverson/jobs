/*
  Scrubber math, side-effect-free.

  Maps cursor x-fraction (0..1 across the strip) to log positions and
  back, using event timestamps as the time axis. The strip's left
  edge is "24h ago" and the right edge is "now"; events outside that
  window aren't navigable through the strip but are still reachable
  by URL.

  Also computes density bars for the histogram and formats the
  history banner string. All shared with scrubber-pill.mjs and any
  future module that needs to project events onto the strip.
*/

import { isPosition, samePosition } from "./position.mjs";

const ONE_DAY_MS = 24 * 60 * 60 * 1000;

// xToPosition maps a cursor x-fraction (0 = window start, 1 = window
// end) to the log position of the newest event whose created_at falls at
// or before that moment. An empty events array returns null; x past the
// most recent event clamps to head; x before the window's oldest event
// clamps to that event.
//
// `windowStartMs` defaults to `nowMs - windowMs` — the original
// "trailing 24h ending now" mode. Pan and zoom override it.
//
// Events are expected to be sorted ascending by created_at — the
// /events endpoint returns them in that order.
export function xToPosition(
  xFrac,
  events,
  nowMs,
  windowMs = ONE_DAY_MS,
  windowStartMs = nowMs - windowMs,
) {
  if (events.length === 0) return null;
  const targetMs = windowStartMs + xFrac * windowMs;
  // Binary search for the rightmost event with created_at*1000 <= targetMs.
  let lo = 0;
  let hi = events.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (events[mid].created_at * 1000 <= targetMs) lo = mid + 1;
    else hi = mid;
  }
  if (lo === 0) return events[0].position;
  return events[lo - 1].position;
}

// positionToX is the inverse: returns the x-fraction of the cursor
// that corresponds to the given log position, by looking up the event's
// created_at and projecting it into the [windowStartMs,
// windowStartMs + windowMs] window. Events outside the window clamp
// to 0 or 1; an unknown cursor returns 1.
export function positionToX(
  position,
  events,
  nowMs,
  windowMs = ONE_DAY_MS,
  windowStartMs = nowMs - windowMs,
) {
  const event = events.find((e) => samePosition(e.position, position));
  if (!event) return 1;
  const eventMs = event.created_at * 1000;
  const offset = eventMs - windowStartMs;
  if (offset <= 0) return 0;
  if (offset >= windowMs) return 1;
  return offset / windowMs;
}

// computeDensityBars buckets events into N bars across the window
// and returns each bar's relative height as an integer percent. The
// busiest bucket is normalized to 100; empty buckets stay at 0.
// Output is fixed-length (bucketCount) so the caller can render bars
// even when the DB has zero events.
export function computeDensityBars(
  events,
  nowMs,
  bucketCount = 60,
  windowMs = ONE_DAY_MS,
  windowStartMs = nowMs - windowMs,
) {
  const buckets = new Array(bucketCount).fill(0);
  const bucketSizeMs = windowMs / bucketCount;
  for (const e of events) {
    const offset = e.created_at * 1000 - windowStartMs;
    if (offset < 0 || offset >= windowMs) continue;
    // Bucket 0 = window start; bucket N-1 = window end.
    let idx = Math.floor(offset / bucketSizeMs);
    if (idx < 0) idx = 0;
    if (idx >= bucketCount) idx = bucketCount - 1;
    buckets[idx]++;
  }
  let max = 0;
  for (const c of buckets) if (c > max) max = c;
  if (max === 0) return buckets.map(() => 0);
  return buckets.map((c) => Math.round((c / max) * 100));
}

// Zoom limits: 1 minute on the tight end (the smallest scale where
// the density histogram still reads as more than a single bin) and
// 10 years on the wide end. The wide end is just a sanity guard
// against NaN / infinity — the real "you've zoomed out enough" stop
// comes from clampWindowStartToFloor in the UI layer, which pins the
// left edge to the first recorded event.
const MIN_WINDOW_MS = 60 * 1000;
const MAX_WINDOW_MS = 10 * 365 * ONE_DAY_MS;

// zoomWindow returns the new {windowStartMs, windowMs} after
// multiplying the visible length by `factor`, anchored at
// `anchorXFrac`. Anchor-centered means: the time at the anchor's
// x-fraction is the same before and after the zoom — so scrollwheel
// over a particular event keeps that event under the pointer.
//
// Pure: clamps windowMs to [MIN_WINDOW_MS, MAX_WINDOW_MS]; never
// returns NaN.
export function zoomWindow(currentStartMs, currentWindowMs, anchorXFrac, factor) {
  const safeAnchor = Number.isFinite(anchorXFrac) ? anchorXFrac : 0.5;
  const anchorTimeMs = currentStartMs + safeAnchor * currentWindowMs;
  let nextWindowMs = currentWindowMs * factor;
  if (!Number.isFinite(nextWindowMs) || nextWindowMs <= 0) nextWindowMs = currentWindowMs;
  if (nextWindowMs < MIN_WINDOW_MS) nextWindowMs = MIN_WINDOW_MS;
  if (nextWindowMs > MAX_WINDOW_MS) nextWindowMs = MAX_WINDOW_MS;
  const nextStartMs = anchorTimeMs - safeAnchor * nextWindowMs;
  return { windowStartMs: nextStartMs, windowMs: nextWindowMs };
}

// clampWindowEndToNow guarantees the visible window's right edge
// never extends past `nowMs`. If the proposed window already ends at
// or before now, it's returned unchanged; otherwise we translate the
// start back so the right edge pins to now (windowMs is preserved —
// clamping is a translation, not a resize).
//
// Pure: pan and zoom math stays unconstrained; the UI calls this at
// the boundary so "no future scrubbing" is one rule applied in one
// place.
export function clampWindowEndToNow(windowStartMs, windowMs, nowMs) {
  const end = windowStartMs + windowMs;
  if (end <= nowMs) return { windowStartMs, windowMs };
  return { windowStartMs: nowMs - windowMs, windowMs };
}

// clampWindowStartToFloor guarantees the visible window's left edge
// never extends earlier than `floorMs` (typically the first event's
// timestamp). If `floorMs` is null — e.g. before events have loaded —
// this is a no-op so we don't pin to a synthetic boundary.
//
// Symmetric counterpart to clampWindowEndToNow. Pure: translates the
// start, never resizes.
export function clampWindowStartToFloor(windowStartMs, windowMs, floorMs) {
  if (floorMs == null) return { windowStartMs, windowMs };
  if (windowStartMs >= floorMs) return { windowStartMs, windowMs };
  return { windowStartMs: floorMs, windowMs };
}

// windowForPosition picks the visible window for a cold-load with
// ?at=<position>. When the target event sits inside the trailing
// `defaultWindowMs` ending at `nowMs`, the default window is returned
// unchanged. When the event is older than the default window, the
// window is widened so the event lands inside it (right edge pinned
// to nowMs so the user can still scrub forward to live). Returns the
// default for an unknown or future-dated cursor.
//
// Pure: no DOM, no clamping against the history floor — that happens
// in the UI layer when needed. Exists so cold-loading ?at=<position> for
// an event older than 24h doesn't pin the cursor to xFrac=0 and silently
// re-resolve to a different event.
export function windowForPosition(
  position,
  events,
  nowMs,
  defaultWindowMs = ONE_DAY_MS,
) {
  const defaults = {
    windowStartMs: nowMs - defaultWindowMs,
    windowMs: defaultWindowMs,
  };
  const event = events.find((e) => samePosition(e.position, position));
  if (!event) return defaults;
  const eventMs = event.created_at * 1000;
  if (eventMs >= nowMs - defaultWindowMs) return defaults;
  // Widen so the event sits at xFrac ≈ 0.25 with the right edge at
  // nowMs. The 4/3 multiplier comes from solving
  //   windowStartMs + 0.25 * windowMs = eventMs
  //   windowStartMs + windowMs        = nowMs
  // for windowMs given (nowMs - eventMs).
  const windowMs = (nowMs - eventMs) * (4 / 3);
  return { windowStartMs: nowMs - windowMs, windowMs };
}

// classifyWheelAxis decides whether a wheel event should drive zoom
// (vertical) or pan (horizontal), with axis-lock + idle-reset so a
// single trackpad gesture commits to one mode start-to-finish.
//
// Inputs:
//   deltaX, deltaY  — current wheel event deltas
//   ctrlKey         — macOS reports trackpad pinch as wheel + ctrlKey
//   timeStamp       — current event's timeStamp
//   lastTimeStamp   — last classified event's timeStamp (or null)
//   lastAxis        — last classified axis ("x" | "y" | null)
//
// Returns { axis, locked }:
//   axis    — "x" (pan), "y" (zoom), or null (no movement)
//   locked  — true when the result should update the caller's state
//             (pinch returns locked=false so it doesn't poison the
//             real per-gesture lock)
//
// Idle threshold: 120ms. macOS trackpad wheels arrive ~16ms apart;
// 120ms comfortably distinguishes "still in gesture" from "new gesture."
export function classifyWheelAxis({
  deltaX,
  deltaY,
  ctrlKey,
  timeStamp,
  lastTimeStamp,
  lastAxis,
}) {
  if (ctrlKey) return { axis: "y", locked: false };
  const ax = Math.abs(deltaX);
  const ay = Math.abs(deltaY);
  if (ax === 0 && ay === 0) return { axis: null, locked: false };
  const idle = lastTimeStamp == null || timeStamp - lastTimeStamp > 120;
  if (!idle && lastAxis) return { axis: lastAxis, locked: true };
  return { axis: ax > ay ? "x" : "y", locked: true };
}

// formatAxisLabels returns 5 labels for the time axis at x = 0, 25,
// 50, 75, 100%. Each label is the relative age at that x position
// (e.g. "24h", "18h", ...), with "now" used when the gridline is at
// or after `nowMs`. Useful for rebuilding the strip's axis under
// pan/zoom without re-templating the markup.
export function formatAxisLabels(windowStartMs, windowMs, nowMs) {
  const out = new Array(5);
  for (let i = 0; i < 5; i++) {
    const xFrac = i / 4;
    const tMs = windowStartMs + xFrac * windowMs;
    const ageMs = nowMs - tMs;
    if (ageMs <= 0) {
      out[i] = "now";
    } else {
      out[i] = formatAge(ageMs).replace(/\s+ago$/, "");
    }
  }
  return out;
}

// panWindowStartMs returns the new windowStartMs after a pan gesture
// of `deltaPx` over a track of `trackWidthPx` showing `windowMs` of
// time. Convention matches map-style panning: dragging right pulls
// older content into view (windowStart shifts earlier).
//
// Pure: no clamping, no DOM. Callers decide whether to clamp against
// any boundary; unbounded pan is a feature for sparse-event windows.
export function panWindowStartMs(
  currentStartMs,
  deltaPx,
  trackWidthPx,
  windowMs,
) {
  if (trackWidthPx <= 0) return currentStartMs;
  return currentStartMs - (deltaPx / trackWidthPx) * windowMs;
}

// formatAge formats a millisecond duration as a compact relative
// string ("45s ago", "5m ago", "3h ago", "2d ago"). Negative inputs
// are treated as zero; matches the convention used in render.RelativeTime
// on the Go side so the wall-clock + relative align across the page.
export function formatAge(ageMs) {
  const sec = Math.max(0, Math.floor(ageMs / 1000));
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

// formatHistoryBannerText builds the "?at=<position> · Ns ago · YYYY-MM-DD HH:MM:SS"
// string the history banner shows. UTC timestamp keeps the wire
// representation consistent regardless of viewer locale; the relative
// part follows the local clock-derived `ageMs`.
export function formatHistoryBannerText(event, nowMs) {
  const ageMs = nowMs - event.created_at * 1000;
  const age = formatAge(ageMs);
  const iso = new Date(event.created_at * 1000).toISOString().replace("T", " ").slice(0, 19);
  return `?at=${event.position} · ${age} · ${iso}`;
}

// parseAtFromQuery extracts a log position from a URL query string.
// Returns null for missing, empty or unparseable input — the caller
// treats null as "live, no scrub state in URL." Same shape as the
// server's parseAtParam in internal/web/handlers so client and server
// agree on what counts as a valid ?at. A bare row id is not a cursor:
// a rebuild renumbers those, so it is rejected here as the server
// rejects it.
export function parseAtFromQuery(searchString) {
  if (!searchString) return null;
  const params = new URLSearchParams(searchString);
  const raw = params.get("at");
  if (!raw) return null;
  return isPosition(raw) ? raw : null;
}

// composeURLWithAt returns a same-origin path-and-query string with
// ?at=<position> set, preserving every other query parameter and the
// hash fragment. Pure: takes a path-or-URL string in, returns a
// string out — no DOM, no location.
export function composeURLWithAt(href, position) {
  const u = new URL(href, "http://placeholder.invalid/");
  u.searchParams.set("at", String(position));
  return u.pathname + u.search + u.hash;
}

// composeURLWithoutAt is the inverse: drops ?at, preserves the rest.
// Returns the path with no trailing "?" when ?at was the only param.
export function composeURLWithoutAt(href) {
  const u = new URL(href, "http://placeholder.invalid/");
  u.searchParams.delete("at");
  const search = u.searchParams.toString();
  return u.pathname + (search ? "?" + search : "") + u.hash;
}
