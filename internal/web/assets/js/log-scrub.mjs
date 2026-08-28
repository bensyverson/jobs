/*
  Log-view scrubber driver.

  /log is an event-window view: the server already honors ?at=N
  (R0Ro4) and emits the right HTML at any cursor — the chip
  vocabulary, the "showing N of M events" counter, and the rendered
  rows are all scoped to the at-window, and every chip href (like the
  range tabs) carries ?at forward so a click stays parked in history
  instead of jumping back to live. So unlike Plan and Actors, we
  don't need a JS port of the rollup; we just need to refetch when
  the cursor moves and swap the three regions the cursor affects:

    main .c-view-header        (range tab hrefs carry ?at)
    main section.c-filter-bar  (chip hrefs carry ?at; strips are scoped to the cursor)
    main .c-log-live           (TotalEvents counter)
    main .c-log                (the event list itself)

  Scrubber-pill calls syncURL with the new ?at *before* dispatching
  jobs:scrubber-frame, so by the time we run window.location.href
  already reflects the cursor. Live mode strips ?at via the same helper
  the pill uses.

  Self-guarded: no-op when the page lacks .c-log so the same module
  can ride the shared layout without per-page wiring.
*/

import { composeURLWithoutAt } from "./scrubber-cursor.mjs";

const SWAP_SELECTORS = [
  "main .c-view-header",
  "main section.c-filter-bar",
  "main .c-log-live",
  "main .c-log",
];

function findLog(doc) {
  return doc.querySelector(".c-log");
}

function rehydrate(doc) {
  if (window.JobsColors && typeof window.JobsColors.paint === "function") {
    window.JobsColors.paint(doc);
  }
}

async function refetchAndSwap(url) {
  try {
    const res = await fetch(url, {
      headers: { Accept: "text/html" },
      credentials: "same-origin",
    });
    if (!res.ok) return;
    const html = await res.text();
    const fresh = new DOMParser().parseFromString(html, "text/html");
    for (const sel of SWAP_SELECTORS) {
      const oldEl = document.querySelector(sel);
      const newEl = fresh.querySelector(sel);
      if (oldEl && newEl) oldEl.replaceWith(newEl);
    }
    rehydrate(document);
  } catch (_) {
    // Network blip; the next event will retry.
  }
}

if (typeof document !== "undefined") {
  function init() {
    if (!findLog(document)) return;
    document.addEventListener("jobs:scrubber-frame", () => {
      // syncURL has already set ?at=N on the address bar by the time
      // this fires; refetching window.location.href returns the
      // server-rendered page at that cursor.
      refetchAndSwap(window.location.href);
    });
    document.addEventListener("jobs:scrubber-live", () => {
      // Live mode: the pill's exitScrubbing already stripped ?at via
      // pushState, but we recompute defensively in case a future
      // change drops that step.
      refetchAndSwap(composeURLWithoutAt(window.location.href));
    });
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
