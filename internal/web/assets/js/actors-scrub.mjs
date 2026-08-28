/*
  Actors-board scrubber driver.

  When the scrubber pill dispatches 'jobs:scrubber-frame' on the
  document, this module rebuilds the .c-actors-board from the in-
  memory event log + frame, mirroring what handlers.Actors would
  render server-side at the cursor's event id — including the
  ?range= window, re-anchored on the cursor. Live mode is restored
  by 'jobs:scrubber-live' via a fetch-and-swap of the current URL,
  which carries ?range= along. The range selector itself lives
  outside the board element and is never swapped.

  Why we need events here, not just the frame: the actors-board's
  per-(actor, task) cards are derived from the event walk (latest
  state-changing event sets the verb tint; noted events fold to a
  notes badge). The frame is aggregate state — it doesn't preserve
  the per-actor history. So the driver pulls events from the
  ReplayBuffer's `range(0, cursorId)` and reduces them client-side.

  Self-guarded: if the page has no actors-board marker, the module is
  a no-op so it can load from the shared layout without per-page
  wiring.
*/

import { buildActorColumns } from "./actors-scrub-build.mjs";
import { renderActorsBoard } from "./actors-scrub-render.mjs";
import { rangeFromSearch } from "./range.mjs";

function findBoard(doc) {
  return doc.querySelector("[data-actors-board]");
}

function rehydrate(doc) {
  if (window.JobsColors) {
    if (typeof window.JobsColors.paint === "function") {
      window.JobsColors.paint(doc);
    }
  }
}

function swapHTMLInto(html, oldEl) {
  const doc = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");
  const fresh = Array.from(doc.body.children);
  if (fresh.length === 0) return;
  oldEl.replaceWith(...fresh);
}

async function applyFrameToDOM(frame, event) {
  const doc = document;
  const board = findBoard(doc);
  if (!board) return;
  const buf = window.JobsScrubber;
  if (!buf) return;
  const cursorId = event?.id ?? frame.eventId;
  const events = await buf.range(0, cursorId);
  const nowSec = event?.created_at ?? Math.floor(Date.now() / 1000);
  // The ?range= window is measured back from the cursor, not from
  // wall-clock now — scrubbing to last month shows the week before
  // *then*. Mirrors rangeAnchor + parseRange in handlers/range.go.
  const { cutoff } = rangeFromSearch(window.location.search, nowSec);
  const cols = buildActorColumns(events, frame, nowSec, cutoff);
  swapHTMLInto(renderActorsBoard(cols), findBoard(doc));
  rehydrate(doc);
}

async function refetchLive() {
  const doc = document;
  const board = findBoard(doc);
  if (!board) return;
  try {
    const res = await fetch(window.location.href, {
      headers: { Accept: "text/html" },
      credentials: "same-origin",
    });
    if (!res.ok) return;
    const html = await res.text();
    const fresh = new DOMParser().parseFromString(html, "text/html");
    const freshBoard = fresh.querySelector("[data-actors-board]");
    if (freshBoard) findBoard(doc).replaceWith(freshBoard);
    rehydrate(doc);
  } catch (_) {
    // Network blip; the next live event lets actors-live.js catch up.
  }
}

if (typeof document !== "undefined") {
  function init() {
    if (!findBoard(document)) return;
    document.addEventListener("jobs:scrubber-frame", (e) => {
      const detail = e.detail ?? {};
      if (!detail.frame) return;
      applyFrameToDOM(detail.frame, detail.event);
    });
    document.addEventListener("jobs:scrubber-live", () => {
      refetchLive();
    });
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
