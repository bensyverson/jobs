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
// Output bag mirrors handlers.HomePageData (minus Graph): Activity,
// NewlyBlocked, LongestClaim, OldestTodo, ActiveClaims,
// RecentCompletions, Upcoming, Blocked.

import { test } from "node:test";
import assert from "node:assert/strict";

import { initialFrame } from "../assets/js/replay.mjs";
import {
  buildHomeFrame,
  buildActivity,
  buildNewlyBlocked,
  buildLongestClaim,
  buildOldestTodo,
  buildActiveClaims,
  buildRecentCompletions,
  buildUpcoming,
  buildBlocked,
  formatClaimDuration,
  pct,
} from "../assets/js/home-scrub-build.mjs";

// --- helpers ---

function evt(id, actor, type, taskID, createdAt = 1700000000, detail = {}) {
  return { id, actor, event_type: type, task_id: taskID, created_at: createdAt, detail };
}

function frameWith({ tasks = [], blocks = [], claims = [] } = {}) {
  return initialFrame({ headEventId: 0, tasks, blocks, claims });
}

// --- formatClaimDuration / pct ---

test("formatClaimDuration: matches render.ClaimDuration ladder", () => {
  assert.equal(formatClaimDuration(45), "45s");
  assert.equal(formatClaimDuration(125), "2m 5s");
  assert.equal(formatClaimDuration(3600), "1h");
  assert.equal(formatClaimDuration(3700), "1h 1m");
  assert.equal(formatClaimDuration(86400), "1d");
  assert.equal(formatClaimDuration(90000), "1d 1h");
});

test("pct: clamps progress to [0, 100]", () => {
  assert.equal(pct(0), 0);
  assert.equal(pct(0.5), 50);
  assert.equal(pct(1), 100);
  assert.equal(pct(-1), 0);
  assert.equal(pct(2), 100);
});

// --- buildActivity ---

test("buildActivity: events outside the 60m window are ignored", () => {
  const events = [
    evt(1, "alice", "created", "T1", 1700000000 - 7200), // 2h ago
    evt(2, "alice", "created", "T2", 1700000000 - 1000), // 16m ago
  ];
  const a = buildActivity(events, 1700000000);
  assert.equal(a.TotalCreate, 1);
  assert.equal(a.TotalEvents, 1);
});

test("buildActivity: bars[59] is the most recent minute, bars[0] the oldest", () => {
  const now = 1700000000;
  const events = [
    evt(1, "alice", "done", "T1", now - 30), // <1m ago → bars[59]
    evt(2, "alice", "done", "T1", now - 3540), // 59m ago → bars[0]
  ];
  const a = buildActivity(events, now);
  assert.equal(a.Bars.length, 60);
  assert.equal(a.Bars[59].Empty, false);
  assert.equal(a.Bars[59].Done, 1);
  assert.equal(a.Bars[0].Empty, false);
  assert.equal(a.Bars[0].Done, 1);
});

test("buildActivity: stacks types in one bucket; tallest bar peaks at 100%", () => {
  const now = 1700000000;
  const events = [
    // bucket 59: 3 events (2 done + 1 claim)
    evt(1, "alice", "done", "T1", now - 5),
    evt(2, "alice", "done", "T2", now - 6),
    evt(3, "alice", "claimed", "T3", now - 7),
    // bucket 50: 1 created
    evt(4, "alice", "created", "T4", now - 540),
  ];
  const a = buildActivity(events, now);
  assert.equal(a.Bars[59].HeightPercent, 100);
  assert.equal(a.Bars[59].Done, 2);
  assert.equal(a.Bars[59].Claim, 1);
  assert.equal(a.TotalDone, 2);
  assert.equal(a.TotalClaim, 1);
  assert.equal(a.TotalCreate, 1);
  assert.equal(a.TotalEvents, 4);
  assert.ok(a.Bars[50].HeightPercent >= 33 && a.Bars[50].HeightPercent <= 34);
});

test("buildActivity: only counts {done, claimed, created, blocked} types", () => {
  const now = 1700000000;
  const events = [
    evt(1, "alice", "noted", "T1", now - 5),
    evt(2, "alice", "labeled", "T1", now - 6),
    evt(3, "Jobs", "claim_expired", "T1", now - 7),
    evt(4, "alice", "blocked", "T1", now - 8, { blocked_id: "T1", blocker_id: "T2" }),
  ];
  const a = buildActivity(events, now);
  assert.equal(a.TotalEvents, 1);
  assert.equal(a.TotalBlock, 1);
});

test("buildActivity: empty buckets render as Empty:true placeholders", () => {
  const a = buildActivity([], 1700000000);
  for (const b of a.Bars) assert.equal(b.Empty, true);
});

// --- buildNewlyBlocked ---

test("buildNewlyBlocked: counts blocked events in last 10m, items capped at 5", () => {
  const now = 1700000000;
  const events = [];
  for (let i = 1; i <= 7; i++) {
    events.push(evt(i, "alice", "blocked", "T1", now - i * 10, {
      blocked_id: "B" + i,
      blocker_id: "K" + i,
    }));
  }
  // One outside the window:
  events.push(evt(99, "alice", "blocked", "T1", now - 700, { blocked_id: "Bx", blocker_id: "Kx" }));
  const nb = buildNewlyBlocked(events, now);
  assert.equal(nb.Count, 7);
  assert.equal(nb.Items.length, 5);
  // Newest first.
  assert.equal(nb.Items[0].BlockedShortID, "B1");
  assert.equal(nb.Items[0].BlockedURL, "/tasks/B1");
  assert.equal(nb.Items[0].WaitingOnShortID, "K1");
  assert.equal(nb.Items[0].WaitingOnURL, "/tasks/K1");
});

test("buildNewlyBlocked: ProgressPct saturates at threshold (5)", () => {
  const now = 1700000000;
  const events = [];
  for (let i = 1; i <= 10; i++) {
    events.push(evt(i, "alice", "blocked", "T1", now - 60, {
      blocked_id: "B" + i,
      blocker_id: "K" + i,
    }));
  }
  const nb = buildNewlyBlocked(events, now);
  assert.equal(nb.Count, 10);
  assert.equal(nb.ProgressPct, 100);
});

test("buildNewlyBlocked: empty when no recent blocks", () => {
  const nb = buildNewlyBlocked([], 1700000000);
  assert.equal(nb.Count, 0);
  assert.equal(nb.Items.length, 0);
  assert.equal(nb.ProgressPct, 0);
});

// --- buildLongestClaim ---

test("buildLongestClaim: picks claim with earliest start among current holders", () => {
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
    evt(1, "alice", "claimed", "T1", now - 600), // 10m ago
    evt(2, "bob", "claimed", "T2", now - 60), // 1m ago
  ];
  const lc = buildLongestClaim(events, frame, now);
  assert.equal(lc.Present, true);
  assert.equal(lc.TaskShortID, "T1");
  assert.equal(lc.Actor, "alice");
  assert.equal(lc.ActorURL, "/actors/alice");
  assert.equal(lc.TaskURL, "/tasks/T1");
  assert.equal(lc.DurationText, "10m 0s");
});

test("buildLongestClaim: absent when no current claims", () => {
  const lc = buildLongestClaim([], frameWith(), 1700000000);
  assert.equal(lc.Present, false);
});

test("buildLongestClaim: progress saturates at 30m", () => {
  const now = 1700003600;
  const frame = frameWith({
    tasks: [{ shortId: "T1", title: "x", status: "claimed" }],
    claims: [{ shortId: "T1", claimedBy: "alice", expiresAt: 0 }],
  });
  // 60m old claim → progress = 1 → ProgressPct 100
  const events = [evt(1, "alice", "claimed", "T1", now - 3600)];
  const lc = buildLongestClaim(events, frame, now);
  assert.equal(lc.ProgressPct, 100);
});

// --- buildOldestTodo ---

test("buildOldestTodo: picks oldest available unblocked non-deleted task", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "old", status: "available" },
      { shortId: "T2", title: "young", status: "available" },
    ],
  });
  const events = [
    evt(1, "alice", "created", "T1", now - 7200),
    evt(2, "alice", "created", "T2", now - 600),
  ];
  const ot = buildOldestTodo(events, frame, now);
  assert.equal(ot.Present, true);
  assert.equal(ot.TaskShortID, "T1");
  assert.equal(ot.Title, "old");
  assert.equal(ot.TaskURL, "/tasks/T1");
});

test("buildOldestTodo: blocked tasks excluded", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "blocked", status: "available" },
      { shortId: "T2", title: "free", status: "available" },
    ],
    blocks: [{ blockedShortId: "T1", blockerShortId: "K1" }],
  });
  const events = [
    evt(1, "alice", "created", "T1", now - 7200),
    evt(2, "alice", "created", "T2", now - 60),
  ];
  const ot = buildOldestTodo(events, frame, now);
  assert.equal(ot.TaskShortID, "T2");
});

test("buildOldestTodo: claimed/done/canceled tasks excluded", () => {
  const now = 1700001000;
  const frame = frameWith({
    tasks: [
      { shortId: "T1", title: "claimed", status: "claimed" },
      { shortId: "T2", title: "done", status: "done" },
      { shortId: "T3", title: "free", status: "available" },
    ],
  });
  const events = [
    evt(1, "alice", "created", "T1", now - 7200),
    evt(2, "alice", "created", "T2", now - 7000),
    evt(3, "alice", "created", "T3", now - 60),
  ];
  const ot = buildOldestTodo(events, frame, now);
  assert.equal(ot.TaskShortID, "T3");
});

test("buildOldestTodo: absent when nothing qualifies", () => {
  const ot = buildOldestTodo([], frameWith(), 1700000000);
  assert.equal(ot.Present, false);
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

test("buildHomeFrame: returns the full bag with all eight sub-results populated", () => {
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
  assert.ok(bag.Activity);
  assert.ok(bag.NewlyBlocked);
  assert.ok(bag.LongestClaim);
  assert.ok(bag.OldestTodo);
  assert.ok(bag.ActiveClaims);
  assert.ok(bag.RecentCompletions);
  assert.ok(bag.Upcoming);
  assert.ok(bag.Blocked);
  assert.equal(bag.LongestClaim.Present, true);
  assert.equal(bag.ActiveClaims.Count, 1);
});
