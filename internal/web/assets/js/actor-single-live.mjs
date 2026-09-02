/*
  Single-actor page (/actors/<name>) live updates.

  Two surfaces move when an event arrives, and the server has already
  scoped the SSE stream to this actor (see the page's live-region src),
  so this module renders rather than filters:

    - the Events list (data-actor-events) prepends a row. The markup
      comes from log-row.mjs, the shared mirror of the "log-row"
      partial, so a live row is identical to the row the next reload
      renders. The redundant actor cell is hidden by the list's
      c-log--single-actor modifier, which the row inherits by being
      inside it.

    - the Timeline (data-actor-timeline) appends a mark to the lane for
      the event's verb, positioned by the *currently selected* window,
      which the server publishes as data-timeline-window-secs. The
      event count beside the heading follows.

  The hero stat tiles stay SSR-frozen; a reload refreshes those.

  Self-guarded: with neither marker on the page the module is a no-op,
  so it can be loaded from the shared layout without per-page wiring.
*/

import { renderLogRow } from "./log-row.mjs";

// TIMELINE_VERBS mirrors handlers.timelineVerbs — the five lanes the
// strip renders. Other event types still count toward the total but
// have no lane to land in.
export const TIMELINE_VERBS = ["created", "claimed", "done", "blocked", "noted"];

// MAX_MARKS_PER_LANE bounds a long-lived tab. The lane is a fixed-width
// track, so past a few hundred marks the extra elements are invisible
// overdraw; the oldest (leftmost, and first in DOM order) go first.
const MAX_MARKS_PER_LANE = 200;

// MAX_ROWS bounds the event list in a long-lived tab. Lower than
// log-live.mjs's cap on /log because this list is one actor's hub, not
// a history scroll — the SSR render stops at ActorEventListLimit and
// the "View all in Log" link is the way to more.
const MAX_ROWS = 200;

export function isTimelineVerb(eventType) {
  return TIMELINE_VERBS.indexOf(eventType) >= 0;
}

// markPercent returns the mark's --x for an event, formatted the way
// handlers.loadActorTimeline formats it ("%.1f"), or null when the
// event has no place on this strip: older than the window, or the
// window itself is unreadable. Future timestamps (clock skew between
// the server and a client) clamp to the right edge rather than
// overflowing the track.
export function markPercent(eventSec, nowSec, windowSecs) {
  if (!Number.isFinite(windowSecs) || windowSecs <= 0) return null;
  if (!Number.isFinite(eventSec) || !Number.isFinite(nowSec)) return null;
  const offset = ((eventSec - (nowSec - windowSecs)) / windowSecs) * 100;
  if (offset < 0) return null;
  return (offset > 100 ? 100 : offset).toFixed(1);
}

function init() {
  const live = document.querySelector("live-region");
  if (!live) return;

  const list = document.querySelector("[data-actor-events]");
  const timeline = document.querySelector("[data-actor-timeline]");
  if (!list && !timeline) return;

  const seen = new Set();
  if (list) {
    list.querySelectorAll("[data-event-position]").forEach((el) => {
      seen.add(el.getAttribute("data-event-position"));
    });
  }

  live.addEventListener("event", (ev) => {
    const data = ev.detail;
    if (!data || !data.position) return;

    if (seen.has(data.position)) return;
    seen.add(data.position);

    if (list) prependRow(list, data);
    if (timeline) addMark(timeline, data);
  });
}

function prependRow(list, e) {
  const empty = list.querySelector(".c-log-row--empty");
  if (empty && empty.parentElement) empty.remove();

  const host = document.createElement("template");
  host.innerHTML = renderLogRow(e);
  const row = host.content.firstElementChild;
  if (!row) return;
  row.classList.add("c-log-row--new");
  list.prepend(row);
  paintIfAvailable(row);

  while (list.childElementCount > MAX_ROWS) {
    list.removeChild(list.lastElementChild);
  }
}

function addMark(timeline, e) {
  bumpEventCount(timeline);
  if (!isTimelineVerb(e.event_type)) return;

  const windowSecs = Number(timeline.getAttribute("data-timeline-window-secs"));
  const x = markPercent(epochSecondsOf(e.created_at), Date.now() / 1000, windowSecs);
  if (x === null) return;

  const track = timeline.querySelector('[data-lane="' + cssEscape(e.event_type) + '"]');
  if (!track) return;

  const mark = document.createElement("span");
  mark.className = "c-actor-timeline__mark c-actor-timeline__mark--" + safeClass(e.event_type);
  mark.style.setProperty("--x", x + "%");
  mark.style.setProperty("--w", "3px");
  track.appendChild(mark);

  while (track.childElementCount > MAX_MARKS_PER_LANE) {
    track.removeChild(track.firstElementChild);
  }
}

// bumpEventCount keeps the "N events" label beside the heading honest:
// it counts every event in the window, lane or no lane, exactly as
// ActorTimeline.TotalEvents does.
function bumpEventCount(timeline) {
  const el = document.querySelector("[data-timeline-count]");
  if (!el) return;
  const next = (parseInt(el.getAttribute("data-timeline-count") || "0", 10) || 0) + 1;
  el.setAttribute("data-timeline-count", String(next));
  el.textContent = next + " events";
}

function paintIfAvailable(node) {
  if (window.JobsColors && typeof window.JobsColors.paint === "function") {
    window.JobsColors.paint(node);
  }
}

function safeClass(s) {
  return String(s == null ? "" : s).replace(/[^a-zA-Z0-9_-]/g, "");
}

function cssEscape(s) {
  if (window.CSS && typeof window.CSS.escape === "function") {
    return window.CSS.escape(s);
  }
  return String(s).replace(/["\\]/g, "\\$&");
}

function epochSecondsOf(rfc3339) {
  if (!rfc3339) return Math.floor(Date.now() / 1000);
  const t = Date.parse(rfc3339);
  return isNaN(t) ? Math.floor(Date.now() / 1000) : Math.floor(t / 1000);
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
