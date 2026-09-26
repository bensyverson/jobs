// Tests for internal/web/assets/js/home-scrub-render.mjs.
//
// Pure HTML emitter mirroring internal/web/templates/html/pages/
// home.html.tmpl for the four panels. The chart panel is fetched from
// the server instead (reporting decision 12). The driver
// parses each fragment with DOMParser and swaps it into the live
// page; the data-home-* hooks the live updater (home-live.js) needs
// stay intact.

import { test } from "node:test";
import assert from "node:assert/strict";

import * as homeScrubRender from "../assets/js/home-scrub-render.mjs";
import {
  renderActiveClaims,
  renderRecentCompletions,
  renderUpcoming,
  renderBlocked,
} from "../assets/js/home-scrub-render.mjs";

// --- helpers ---

function defaultBag(over = {}) {
  return {
    ActiveClaims: { Count: 0, Rows: [] },
    RecentCompletions: { Count: 0, Rows: [] },
    Upcoming: { Count: 0, Rows: [] },
    Blocked: { Count: 0, Rows: [] },
    ...over,
  };
}

// --- Active claims panel ---

test("renderActiveClaims: section carries data-home-claims and meta count", () => {
  const html = renderActiveClaims({
    Count: 1,
    Rows: [
      {
        Actor: "alice",
        ActorURL: "/actors/alice",
        TaskShortID: "T1",
        TaskURL: "/tasks/T1",
        TaskTitle: "Some task",
        DurationText: "5m 30s",
        ClaimedAtUnix: 1700000000,
      },
    ],
  });
  assert.match(html, /data-home-claims/);
  assert.match(html, /1 in flight/);
  assert.match(html, /data-claimed-at="1700000000"/);
  // data-claim-idle is a boolean attribute (no value) matching the Go
  // template; the duration text follows directly after the > .
  assert.match(html, /data-claim-idle>5m 30s</);
  assert.match(html, /data-actor="alice"/);
  assert.match(html, /Some task/);
});

test("renderActiveClaims: empty state renders prototype copy in c-empty", () => {
  const html = renderActiveClaims({ Count: 0, Rows: [] });
  assert.match(html, /<p class="c-empty">No claims in flight\.<\/p>/);
});

// --- Recent completions panel ---

test("renderRecentCompletions: section carries data-home-recent + 'last N' meta", () => {
  const html = renderRecentCompletions({
    Count: 2,
    Rows: [
      {
        Actor: "alice",
        ActorURL: "/actors/alice",
        TaskShortID: "T1",
        TaskURL: "/tasks/T1",
        TaskTitle: "Done task",
        AgeText: "2m",
        CompletedAtUnix: 1700000000,
      },
      {
        Actor: "bob",
        ActorURL: "/actors/bob",
        TaskShortID: "T2",
        TaskURL: "/tasks/T2",
        TaskTitle: "Other",
        AgeText: "5m",
        CompletedAtUnix: 1700000000,
      },
    ],
  });
  assert.match(html, /data-home-recent/);
  assert.match(html, /last 2/);
  assert.match(html, /Done task/);
  assert.match(html, /Other/);
  assert.match(html, /data-actor="bob"/);
});

test("renderRecentCompletions: empty state renders prototype copy in c-empty", () => {
  const html = renderRecentCompletions({ Count: 0, Rows: [] });
  assert.match(html, /<p class="c-empty">Nothing completed in the last 10 minutes\.<\/p>/);
});

// --- Upcoming panel ---

test("renderUpcoming: section carries data-home-upcoming + 'N ready' meta", () => {
  const html = renderUpcoming({
    Count: 1,
    Rows: [
      {
        TaskShortID: "T1",
        TaskURL: "/tasks/T1",
        TaskTitle: "Build it",
        AgeText: "1h 5m",
        CreatedAtUnix: 1700000000,
      },
    ],
  });
  assert.match(html, /data-home-upcoming/);
  assert.match(html, /1 ready/);
  assert.match(html, /data-created-at="1700000000"/);
  assert.match(html, /Build it/);
});

test("renderUpcoming: empty state renders prototype copy in c-empty", () => {
  const html = renderUpcoming({ Count: 0, Rows: [] });
  assert.match(html, /<p class="c-empty">Nothing ready to claim\.<\/p>/);
});

// --- Blocked panel ---

test("renderBlocked: section carries data-home-blocked + 'N waiting' meta + blocker pills", () => {
  const html = renderBlocked({
    Count: 1,
    Rows: [
      {
        TaskShortID: "T1",
        TaskURL: "/tasks/T1",
        TaskTitle: "Stuck",
        Blockers: [
          { ShortID: "K1", URL: "/tasks/K1" },
          { ShortID: "K2", URL: "/tasks/K2" },
        ],
      },
    ],
  });
  assert.match(html, /data-home-blocked/);
  assert.match(html, /1 waiting/);
  assert.match(html, /Stuck/);
  assert.match(html, /href="\/tasks\/K1" class="c-id-pill">K1</);
  assert.match(html, /href="\/tasks\/K2" class="c-id-pill">K2</);
  assert.match(html, /waiting on/);
});

test("renderBlocked: empty state renders prototype copy in c-empty", () => {
  const html = renderBlocked({ Count: 0, Rows: [] });
  assert.match(html, /<p class="c-empty">Nothing blocked\.<\/p>/);
});

// --- Escaping ---

test("renderActiveClaims: escapes task title", () => {
  const html = renderActiveClaims({
    Count: 1,
    Rows: [
      {
        Actor: "alice",
        ActorURL: "/actors/alice",
        TaskShortID: "T1",
        TaskURL: "/tasks/T1",
        TaskTitle: "T & U",
        DurationText: "1s",
        ClaimedAtUnix: 1700000000,
      },
    ],
  });
  assert.match(html, /T &amp; U/);
});

test("home-scrub-render: renderSignals is retired", () => {
  assert.equal(homeScrubRender.renderSignals, undefined);
});
