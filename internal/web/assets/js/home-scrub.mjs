/*
  Home-view scrubber driver.

  Two responsibilities, both fired from scrubber-pill CustomEvents on
  document:

    jobs:scrubber-frame: cursor moved to a non-live point.
      1. Mark the dependency-flow graph as pending (.is-pending → CSS
         dims + blurs with a 300ms transition).
      2. Immediately rebuild the four signal cards + four panels from
         the in-memory event log + frame and swap them in. The cards
         and panels are cheap to derive and read better with no
         intermediate "loading" state.
      3. Debounce 300ms, then POST the frame's tasks/blocks to
         /home/graph (the server runs the same Subway core /home runs).
         Swap the returned fragment into [data-home-graph] .c-mini-graph
         and drop .is-pending — CSS transitions back to crisp.

    jobs:scrubber-live: pill returned to live.
      Refetch /home and swap the four signal cards, four panels, and
      the graph back to live state. Drops .is-pending defensively in
      case a previous fetch was still in flight.

  Why the cards/panels are JS-rebuilt while the graph is server-
  rendered: the card aggregations are pure functions of the event log
  + frame and the JS reducer already has both. The graph layout is a
  thousand-LOC pipeline (signals.subway + render.subway_layout); we
  send the frame back to the server rather than port that pipeline.

  Self-guarded: bails when no [data-home-claims] is present so the
  same module can ride the shared layout without per-page wiring.
*/

import { buildHomeFrame } from "./home-scrub-build.mjs";
import {
  renderSignals,
  renderActiveClaims,
  renderRecentCompletions,
  renderUpcoming,
  renderBlocked,
} from "./home-scrub-render.mjs";

const GRAPH_DEBOUNCE_MS = 300;

const SECTION_RENDERERS = [
  { selector: "main .c-grid-signals", render: (bag) => renderSignals(bag) },
  { selector: "[data-home-claims]", render: (bag) => renderActiveClaims(bag.ActiveClaims) },
  { selector: "[data-home-recent]", render: (bag) => renderRecentCompletions(bag.RecentCompletions) },
  { selector: "[data-home-upcoming]", render: (bag) => renderUpcoming(bag.Upcoming) },
  { selector: "[data-home-blocked]", render: (bag) => renderBlocked(bag.Blocked) },
];

function isHomePage(doc) {
  return !!doc.querySelector("[data-home-claims]");
}

function rehydrate(doc) {
  if (window.JobsColors && typeof window.JobsColors.paint === "function") {
    window.JobsColors.paint(doc);
  }
}

function swapInto(selector, html) {
  const parsed = new DOMParser().parseFromString(`<body>${html}</body>`, "text/html");
  const fresh = parsed.body.firstElementChild;
  if (!fresh) return;
  const old = document.querySelector(selector);
  if (!old) return;
  old.replaceWith(fresh);
}

function swapAllSections(bag) {
  for (const { selector, render } of SECTION_RENDERERS) {
    swapInto(selector, render(bag));
  }
  rehydrate(document);
}

// frameToGraphPayload projects the cursor's frame into the JSON shape
// the POST /home/graph endpoint expects (matches signals.SubwayInput).
// frame.tasks is Map<shortId, TaskState>; frame.blocks is
// Map<blockedShortId, Set<blockerShortId>>; frame.claims is
// Map<shortId, { claimedBy, expiresAt }>.
function frameToGraphPayload(frame) {
  const tasks = [];
  for (const [shortId, t] of frame.tasks) {
    const claim = frame.claims.get(shortId);
    tasks.push({
      shortId,
      title: t.title ?? "",
      status: t.status ?? "available",
      parentShortId: t.parentShortId ?? "",
      sortKey: t.sortKey ?? "",
      claimedBy: claim ? claim.claimedBy : "",
    });
  }
  const blocks = [];
  for (const [blockedShortId, blockerSet] of frame.blocks) {
    for (const blockerShortId of blockerSet) {
      blocks.push({ blockedShortId, blockerShortId });
    }
  }
  return { tasks, blocks };
}

let pendingGraphFetch = null;

function markGraphPending() {
  const g = document.querySelector("[data-home-graph] .c-mini-graph");
  if (g) g.classList.add("is-pending");
}

function clearGraphPending() {
  const g = document.querySelector("[data-home-graph] .c-mini-graph");
  if (g) g.classList.remove("is-pending");
}

async function refetchGraph(frame) {
  try {
    const res = await fetch("/home/graph", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "text/html" },
      credentials: "same-origin",
      body: JSON.stringify(frameToGraphPayload(frame)),
    });
    if (!res.ok) {
      // Server hiccup; leave the stale graph in place but drop pending
      // so the user isn't stuck staring at a permanent blur.
      clearGraphPending();
      return;
    }
    const html = await res.text();
    swapInto("[data-home-graph] .c-mini-graph", html);
    clearGraphPending();
  } catch (_) {
    clearGraphPending();
  }
}

async function applyFrameToDOM(frame, event) {
  const buf = window.JobsScrubber;
  if (!buf) return;
  markGraphPending();
  const cursor = event?.position ?? frame.position;
  const events = await buf.range(null, cursor);
  const nowSec = event?.created_at ?? Math.floor(Date.now() / 1000);
  const bag = buildHomeFrame(events, frame, nowSec);
  swapAllSections(bag);
  // Debounce the graph refetch — coalesce rapid drag events into one
  // POST per pause.
  if (pendingGraphFetch) clearTimeout(pendingGraphFetch);
  pendingGraphFetch = setTimeout(() => {
    pendingGraphFetch = null;
    refetchGraph(frame);
  }, GRAPH_DEBOUNCE_MS);
}

async function refetchLive() {
  try {
    const res = await fetch(window.location.href, {
      headers: { Accept: "text/html" },
      credentials: "same-origin",
    });
    if (!res.ok) {
      clearGraphPending();
      return;
    }
    const html = await res.text();
    const fresh = new DOMParser().parseFromString(html, "text/html");
    for (const { selector } of SECTION_RENDERERS) {
      const oldEl = document.querySelector(selector);
      const newEl = fresh.querySelector(selector);
      if (oldEl && newEl) oldEl.replaceWith(newEl);
    }
    const oldGraph = document.querySelector("[data-home-graph] .c-mini-graph");
    const newGraph = fresh.querySelector("[data-home-graph] .c-mini-graph");
    if (oldGraph && newGraph) oldGraph.replaceWith(newGraph);
    clearGraphPending();
    rehydrate(document);
  } catch (_) {
    clearGraphPending();
  }
}

if (typeof document !== "undefined") {
  function init() {
    if (!isHomePage(document)) return;
    document.addEventListener("jobs:scrubber-frame", (e) => {
      const detail = e.detail ?? {};
      if (!detail.frame) return;
      applyFrameToDOM(detail.frame, detail.event);
    });
    document.addEventListener("jobs:scrubber-live", () => {
      // Cancel any in-flight debounced graph fetch — live re-fetch will
      // bring everything back consistently.
      if (pendingGraphFetch) {
        clearTimeout(pendingGraphFetch);
        pendingGraphFetch = null;
      }
      refetchLive();
    });
  }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
}
