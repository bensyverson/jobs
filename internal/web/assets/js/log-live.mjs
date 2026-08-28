/*
  Log view live-tail. Subscribes to the <live-region>'s 'event'
  custom event and prepends a new .c-log-row to the .c-log list for
  each incoming event. Server-side already filters the SSE stream to
  match the page's filter state (see LogPageData.EventsURL), so this
  module doesn't need to re-check filters — it just renders.

  The row markup itself comes from log-row.mjs, the shared mirror of
  the server's "log-row" template block, so a live row is identical to
  the row the next page load renders for the same event. The only
  difference this module adds is the c-log-row--new class that plays
  the arrival animation.

  Self-guarded: if the page has no .c-log, the module is a no-op.
  That way we can load it from the shared layout without per-page
  wiring.
*/

import { renderLogRow } from "./log-row.mjs";

// MAX_ROWS caps the live strip; 500 matches the server-side JSON
// backfill limit, so a long-running tab can't grow without bound.
const MAX_ROWS = 500;

function init() {
  const list = document.querySelector(".c-log");
  if (!list) return;

  const live = document.querySelector("live-region");
  if (!live) return;

  const empty = list.querySelector(".c-log-row--empty");

  // Dedup set: every event id already present in the DOM (server
  // SSR plus any rows we've prepended). Backfill/SSR overlap
  // happens when the page loads with events already rendered and
  // the SSE stream replays them from localStorage-resumed ?since=;
  // without this set we'd duplicate every overlapping row.
  const seen = new Set();
  list.querySelectorAll("[data-event-id]").forEach((el) => {
    seen.add(el.getAttribute("data-event-id"));
  });

  live.addEventListener("event", (ev) => {
    const data = ev.detail;
    if (!data || data.id == null) return;

    const idStr = String(data.id);
    if (seen.has(idStr)) return;
    seen.add(idStr);

    if (empty && empty.parentElement) empty.remove();

    const row = buildRow(data);
    if (!row) return;
    list.prepend(row);

    if (window.JobsColors && typeof window.JobsColors.paint === "function") {
      window.JobsColors.paint(row);
    }

    // Trim the live strip once it has grown past the cap, keeping the
    // dedup set trimmed in parallel.
    while (list.childElementCount > MAX_ROWS) {
      const dropped = list.lastElementChild;
      const droppedID = dropped && dropped.getAttribute("data-event-id");
      list.removeChild(dropped);
      if (droppedID) seen.delete(droppedID);
    }
  });
}

// buildRow parses the shared row markup into an element and marks it
// as newly arrived so the slide-in animation plays.
function buildRow(data) {
  const host = document.createElement("template");
  host.innerHTML = renderLogRow(data);
  const row = host.content.firstElementChild;
  if (!row) return null;
  row.classList.add("c-log-row--new");
  return row;
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
