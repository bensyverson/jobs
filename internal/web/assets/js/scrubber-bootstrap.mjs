/*
  Scrubber bootstrap: glues the SSR-emitted JSON island to the
  client-side ReplayBuffer. Runs once on page load.

  Steps:
    1. Read <script type="application/json" id="initial-frame"> from
       the DOM and parse it.
    2. Hand the parsed payload to initialFrame() to build the head
       Frame.
    3. Construct a ReplayBuffer wired to fetch
       /events?since=<position>&limit=N.
    4. Stash the buffer at window.JobsScrubber so the pill UI and
       timeline can grab it without a circular import.

  No dependencies on the DOM in the parts that matter for tests:
  the JSON-parse, fetcher-builder, and scrubber-construction are all
  pure functions over their inputs. Only the auto-init at bottom
  touches document/window — guarded so node --test can import this
  module without crashing.
*/

import { initialFrame, ReplayBuffer } from "./replay.mjs";
import { isPosition } from "./position.mjs";

// parseInitialFrameJSON parses the island payload. Returns null on
// missing / empty / malformed input so the caller can decide whether
// to fall back to live-only behavior (no scrubber).
export function parseInitialFrameJSON(raw) {
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

// readInitialFrameFromDOM looks up the island script tag and parses
// its text content. Pulled out so the auto-init at bottom is one
// line; tests don't exercise this path directly (no DOM in node).
export function readInitialFrameFromDOM(doc) {
  if (!doc) return null;
  const el = doc.getElementById("initial-frame");
  if (!el) return null;
  return parseInitialFrameJSON(el.textContent);
}

// buildEventsFetcher returns the async ({ since, limit }) -> Event[]
// function ReplayBuffer expects. The fetcher hits /events?since=<position>&
// limit=N (the server's stable JSON replay mode). Inject a custom
// fetch in tests; production gets the global.
export function buildEventsFetcher({ baseURL = "/events", fetch: fetchImpl } = {}) {
  const f = fetchImpl ?? globalThis.fetch;
  if (typeof f !== "function") {
    throw new Error("buildEventsFetcher: no fetch available");
  }
  return async ({ since = null, limit = 500 } = {}) => {
    // Use a relative URL when baseURL is path-only, an absolute URL
    // otherwise. URL constructor needs a base for relatives, but we
    // don't always have document.baseURI in node tests.
    const isAbsolute = /^https?:/i.test(baseURL);
    const url = isAbsolute
      ? new URL(baseURL)
      : new URL(baseURL, "http://placeholder.invalid/");
    // ?since= is a log position; absent means "from the beginning".
    if (isPosition(since)) url.searchParams.set("since", since);
    url.searchParams.set("limit", String(limit));
    const target = isAbsolute ? url.toString() : url.pathname + url.search;
    const res = await f(target);
    if (!res.ok) {
      throw new Error(`fetch ${baseURL}: ${res.status}`);
    }
    const events = await res.json();
    for (const e of events) normalizeEvent(e);
    return events;
  };
}

// normalizeEvent converts the on-the-wire event shape into what the
// reducer expects:
//   - detail: opaque JSON string per the /events contract → parsed
//             object so the reducer can read structured fields.
//   - created_at: RFC3339 string → unix seconds (number) so the
//             cursor math (which multiplies by 1000) works.
// Exported for tests; production callers receive normalized events
// transparently from buildEventsFetcher.
export function normalizeEvent(e) {
  if (typeof e.detail === "string" && e.detail.length > 0) {
    try {
      e.detail = JSON.parse(e.detail);
    } catch {
      e.detail = {};
    }
  } else if (typeof e.detail !== "object" || e.detail === null) {
    e.detail = {};
  }
  if (typeof e.created_at === "string") {
    const ms = Date.parse(e.created_at);
    e.created_at = Number.isFinite(ms) ? Math.floor(ms / 1000) : 0;
  }
  return e;
}

// createScrubber assembles the buffer from parsed payload + fetcher.
// Returns null when the payload is missing so the caller can decide
// whether to no-op or render a "scrubber unavailable" affordance.
export function createScrubber(payload, fetchEvents) {
  if (!payload) return null;
  const head = initialFrame(payload);
  return new ReplayBuffer({
    headFrame: head,
    fetchEvents,
  });
}

// Auto-init in browsers. Skipped in node tests (typeof document
// === 'undefined'). On success, exposes the buffer at
// window.JobsScrubber for sibling modules to consume.
if (typeof document !== "undefined" && typeof window !== "undefined") {
  const payload = readInitialFrameFromDOM(document);
  if (payload) {
    const fetcher = buildEventsFetcher();
    window.JobsScrubber = createScrubber(payload, fetcher);
  }
}
