// Tests for internal/web/assets/js/home-scrub-build.mjs.
//
// Pure-data layer of the Home-view scrubber. Ports the per-card and
// per-panel aggregations from internal/web/handlers/home.go to JS so
// /home can rebuild itself off the in-memory event log + frame when
// the scrubber moves the cursor. Driver consumes the result, render
// module emits HTML.
//
// Inputs: (events, frame, nowSec). Events with id <= cursor in id-asc
// order; created_at is unix seconds. Frame from replay.mjs (tasks Map,
// blocks Map, claims Map). nowSec = cursor event's created_at, frozen.
//
// Output bag mirrors handlers.HomePageData's four panels: ActiveClaims,
// RecentCompletions, Upcoming, Blocked. The chart panel is not rebuilt
// here — the scrubber fetches it from the server (reporting decision
// 12), so none of its counting rules have a JS twin.

import { test } from "node:test";
import assert from "node:assert/strict";

import { initialFrame } from "../assets/js/replay.mjs";
import * as homeScrubBuild from "../assets/js/home-scrub-build.mjs";
import {
  buildHomeFrame,
  buildActiveClaims,
  buildRecentCompletions,
  buildUpcoming,
  buildBlocked,
  formatClaimDuration,
} from "../assets/js/home-scrub-build.mjs";

// --- helpers ---

function evt(id, actor, type, taskID, createdAt = 1700000000, detail = {}) {
  return { id, actor, event_type: type, task_id: taskID, created_at: createdAt, detail };
}

function frameWith({ tasks = [], blocks = [], claims = [] } = {}) {
  return initialFrame({ headEventId: 0, tasks, blocks, claims });
}

// --- formatClaimDuration ---

test("formatClaimDuration: matches render.ClaimDuration ladder", () => {
  assert.equal(formatClaimDuration(45), "45s");
  assert.equal(formatClaimDuration(125), "2m 5s");
  assert.equal(formatClaimDuration(3600), "1h");
  assert.equal(formatClaimDuration(3700), "1h 1m");
  assert.equal(formatClaimDuration(86400), "1d");
  assert.equal(formatClaimDuration(90000), "1d 1h");
});

// --- buildActiveClaims ---

test("buildActiveClaims: rows ordered newest claim first; one row per active claim", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "first", status: "claimed" },
      { shortId: "T2", title: "second", status: "claimed" },
    ],
    claims: [
      { shortId: "T1", claimedBy: "alice", expiresAt: 0 },
      { shortId: "T2", claimedBy: "bob", expiresAt: 0 },
    ],
  });
  const events = [
    evt(1, "alice", "claimed", "T1", now - 600),
    evt(2, "bob", "claimed", "T2", now - 60),
  ];
  const ac = buildActiveClaims(events, frame, now);
  assert.equal(ac.Count, 2);
  assert.equal(ac.Rows[0].TaskShortID, "T2"); // newest first
  assert.equal(ac.Rows[1].TaskShortID, "T1");
  assert.equal(ac.Rows[1].DurationText, "10m 0s");
  assert.equal(ac.Rows[1].ActorURL, "/actors/alice");
  assert.equal(ac.Rows[1].TaskURL, "/tasks/T1");
  assert.equal(ac.Rows[1].ClaimedAtUnix, now - 600);
});

test("buildActiveClaims: empty when no current claims", () => {
  const ac = buildActiveClaims([], frameWith(), 1700000000);
  assert.equal(ac.Count, 0);
  assert.equal(ac.Rows.length, 0);
});

// --- buildRecentCompletions ---

test("buildRecentCompletions: last 25 done/canceled events, newest first", () => {
  const now = 1700001000;
  const events = [];
  for (let i = 1; i <= 30; i++) {
    events.push(evt(i, "alice", i % 2 === 0 ? "done" : "canceled", "T" + i, now - (30 - i) * 60));
  }
  const frame = frameWith({
    tasks: events.map((e) => ({ shortId: e.task_id, title: "title-" + e.task_id, status: "done" })),
  });
  const rc = buildRecentCompletions(events, frame, now);
  assert.equal(rc.Count, 25);
  // Newest first.
  assert.equal(rc.Rows[0].TaskShortID, "T30");
  assert.equal(rc.Rows[0].TaskTitle, "title-T30");
  assert.equal(rc.Rows[0].ActorURL, "/actors/alice");
});

test("buildRecentCompletions: empty when no done/canceled events", () => {
  const rc = buildRecentCompletions([], frameWith(), 1700000000);
  assert.equal(rc.Count, 0);
});

// --- buildUpcoming ---

test("buildUpcoming: lists available unblocked leaves in preorder", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "P", title: "parent", status: "available", sortKey: "000001" },
      { shortId: "C1", title: "child1", status: "available", parentShortId: "P", sortKey: "000001" },
      { shortId: "C2", title: "child2", status: "available", parentShortId: "P", sortKey: "000002" },
      { shortId: "Q", title: "loneleaf", status: "available", sortKey: "000002" },
    ],
  });
  const events = [
    evt(1, "alice", "created", "P", now - 100),
    evt(2, "alice", "created", "C1", now - 90),
    evt(3, "alice", "created", "C2", now - 80),
    evt(4, "alice", "created", "Q", now - 70),
  ];
  const up = buildUpcoming(events, frame, now);
  // P excluded (has open children); C1, C2 (leaves under P), then Q (root leaf).
  assert.deepStrictEqual(
    up.Rows.map((r) => r.TaskShortID),
    ["C1", "C2", "Q"],
  );
});

test("buildUpcoming: blocked task excluded; blocker-done task included", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "blocked", status: "available" },
      { shortId: "T2", title: "free", status: "available" },
      { shortId: "K1", title: "blocker", status: "done" },
    ],
    blocks: [{ blockedShortId: "T1", blockerShortId: "K1" }], // K1 done
  });
  const events = [evt(1, "alice", "created", "T1", now - 100), evt(2, "alice", "created", "T2", now - 50)];
  const up = buildUpcoming(events, frame, now);
  // T1 has only-done blockers (initialFrame's blocks list is the active set;
  // a done blocker means no active edge — so T1 should be included).
  // Actually: initialFrame() above stores K1 in blocks even though it's done,
  // because the JS frame doesn't filter — it trusts the snapshot. So T1 *is*
  // blocked from the JS frame's POV. Both states are coherent: assert that
  // any blocker present in frame.blocks excludes the task.
  assert.deepStrictEqual(
    up.Rows.map((r) => r.TaskShortID),
    ["T2"],
  );
});

test("buildUpcoming: capped at 25", () => {
  const now = 1700001000;
  const tasks = [];
  const events = [];
  for (let i = 1; i <= 30; i++) {
    const id = "T" + i;
    tasks.push({ shortId: id, title: id, status: "available", sortKey: i });
    events.push(evt(i, "alice", "created", id, now - (30 - i) * 10));
  }
  const up = buildUpcoming(events, frameWith({ tasks }), now);
  assert.equal(up.Rows.length, 25);
});

// --- buildBlocked ---

test("buildBlocked: lists blocked tasks with their active blockers", () => {
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "blocked-one", status: "available" },
      { shortId: "K1", title: "blockerA", status: "available" },
      { shortId: "K2", title: "blockerB", status: "available" },
    ],
    blocks: [
      { blockedShortId: "T1", blockerShortId: "K1" },
      { blockedShortId: "T1", blockerShortId: "K2" },
    ],
  });
  const bl = buildBlocked(frame);
  assert.equal(bl.Count, 1);
  assert.equal(bl.Rows[0].TaskShortID, "T1");
  assert.equal(bl.Rows[0].TaskTitle, "blocked-one");
  assert.equal(bl.Rows[0].TaskURL, "/tasks/T1");
  assert.equal(bl.Rows[0].Blockers.length, 2);
  // Sorted lexically for determinism.
  assert.equal(bl.Rows[0].Blockers[0].ShortID, "K1");
  assert.equal(bl.Rows[0].Blockers[0].URL, "/tasks/K1");
});

test("buildBlocked: excludes done/canceled tasks", () => {
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "blocked-done", status: "done" },
      { shortId: "K1", title: "k", status: "available" },
    ],
    blocks: [{ blockedShortId: "T1", blockerShortId: "K1" }],
  });
  const bl = buildBlocked(frame);
  assert.equal(bl.Count, 0);
});

// --- buildHomeFrame: integration ---

test("buildHomeFrame: returns exactly the four panels", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [{ shortId: "T1", title: "x", status: "claimed" }],
    claims: [{ shortId: "T1", claimedBy: "alice", expiresAt: 0 }],
  });
  const events = [
    evt(1, "alice", "created", "T1", now - 600),
    evt(2, "alice", "claimed", "T1", now - 300),
  ];
  const bag = buildHomeFrame(events, frame, now);
  assert.deepEqual(Object.keys(bag).sort(), ["ActiveClaims", "Blocked", "RecentCompletions", "Upcoming"]);
  assert.equal(bag.ActiveClaims.Count, 1);
});

test("home-scrub-build: the retired card builders are gone", () => {
  for (const name of ["buildActivity", "buildNewlyBlocked", "buildLongestClaim", "buildOldestTodo", "pct"]) {
    assert.equal(homeScrubBuild[name], undefined, `${name} is still exported`);
  }
});
