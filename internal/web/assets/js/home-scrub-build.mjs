/*
  Pure-data layer for the Home-view scrubber.

  Ports the four panels' aggregations from internal/web/handlers/home.go
  to JS so /home can rebuild them off the in-memory event log + frame
  when the scrubber moves the cursor. The driver wires this to the
  scrubber CustomEvent, the render module emits HTML.

  The chart panel is deliberately NOT here: a JS port of the burn-up
  would be a second definition of the counting rules, so <chart-panel>
  fetches it from the server at the cursor instead (reporting decision
  12, project/2026-09-26-reporting.md).

  Inputs:
    events  Array<Event>  — events with id <= cursor in id-asc order.
                            Wire shape from /events; created_at in
                            unix seconds (the bootstrap layer
                            normalizes RFC3339 → seconds).
    frame   Frame         — current task/block/claim state at the
                            cursor. From replay.mjs.
    nowSec  number        — cursor event's created_at, frozen so age
                            text reflects the historical moment.

  Output bag mirrors handlers.HomePageData's panels (minus Graph, which
  is refetched server-side via POST /home/graph, and Panel, fetched by
  <chart-panel>): ActiveClaims, RecentCompletions, Upcoming, Blocked.
*/

import { relativeTime } from "./scrub-util.mjs";

const RECENT_COMPLETIONS_LIMIT = 25;
const UPCOMING_LIMIT = 25;
const BLOCKED_STRIP_LIMIT = 20;

// formatClaimDuration mirrors render.ClaimDuration:
//   <1m → "Ns"; <1h → "Nm Ms"; <1d → "Hh" or "Hh Mm"; else "Dd" or "Dd Hh".
export function formatClaimDuration(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  const remS = s - m * 60;
  if (m < 60) return m + "m " + remS + "s";
  const h = Math.floor(m / 60);
  const remM = m - h * 60;
  if (h < 24) return remM === 0 ? h + "h" : h + "h " + remM + "m";
  const d = Math.floor(h / 24);
  const remH = h - d * 24;
  return remH === 0 ? d + "d" : d + "d " + remH + "h";
}

// claimHistory walks events to derive, per task, the most recent
// `claimed` event timestamp + actor that hasn't been cleared by a
// release/done/canceled/expired/reopened event. Returns
// Map<taskShortId, { actor, claimedAt }>.
function claimHistory(events) {
  const m = new Map();
  for (const e of events) {
    if (e.event_type === "claimed") {
      m.set(e.task_id, { actor: e.actor, claimedAt: e.created_at });
    } else if (
      e.event_type === "released" ||
      e.event_type === "done" ||
      e.event_type === "canceled" ||
      e.event_type === "claim_expired" ||
      e.event_type === "reopened"
    ) {
      m.delete(e.task_id);
    }
  }
  return m;
}

// taskCreatedAt walks events for `created` events to derive each
// task's creation timestamp. Tasks created before the earliest event
// in the buffer (shouldn't happen in practice) come back undefined;
// callers default missing entries to 0 so they sort as "infinitely
// old," matching the server which sorts by created_at ASC.
function taskCreatedAt(events) {
  const m = new Map();
  for (const e of events) {
    if (e.event_type === "created" && !m.has(e.task_id)) {
      m.set(e.task_id, e.created_at);
    }
  }
  return m;
}

// buildActiveClaims ports loadActiveClaims. One row per task currently
// in frame.claims, newest claim first.
export function buildActiveClaims(events, frame, nowSec) {
  const claims = claimHistory(events);
  const rows = [];
  for (const [taskID] of frame.claims) {
    const c = claims.get(taskID);
    if (!c) continue;
    const t = frame.tasks.get(taskID);
    const age = Math.max(0, nowSec - c.claimedAt);
    rows.push({
      Actor: c.actor,
      ActorURL: "/actors/" + c.actor,
      TaskShortID: taskID,
      TaskURL: "/tasks/" + taskID,
      TaskTitle: t?.title ?? "",
      DurationText: formatClaimDuration(age),
      ClaimedAtUnix: c.claimedAt,
    });
  }
  rows.sort((a, b) => b.ClaimedAtUnix - a.ClaimedAtUnix);
  return { Count: rows.length, Rows: rows };
}

// buildRecentCompletions ports loadRecentCompletions. Last 25
// done/canceled events, newest first; tasks present in frame.tasks
// supply title.
export function buildRecentCompletions(events, frame, nowSec) {
  const rows = [];
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (e.event_type !== "done" && e.event_type !== "canceled") continue;
    const t = frame.tasks.get(e.task_id);
    rows.push({
      Actor: e.actor,
      ActorURL: "/actors/" + e.actor,
      TaskShortID: e.task_id,
      TaskURL: "/tasks/" + e.task_id,
      TaskTitle: t?.title ?? "",
      AgeText: relativeTime(nowSec, e.created_at),
      CompletedAtUnix: e.created_at,
    });
    if (rows.length >= RECENT_COMPLETIONS_LIMIT) break;
  }
  return { Count: rows.length, Rows: rows };
}

// preorderTasks walks frame.tasks in DFS-preorder, roots by sort key
// first, then descending into each child by sort key. Mirrors the
// recursive sort-key CTE in loadUpcoming.
function preorderTasks(frame) {
  const childrenOf = new Map();
  const roots = [];
  for (const [shortId, t] of frame.tasks) {
    if (t.parentShortId) {
      let arr = childrenOf.get(t.parentShortId);
      if (!arr) {
        arr = [];
        childrenOf.set(t.parentShortId, arr);
      }
      arr.push({ shortId, sortKey: t.sortKey ?? "" });
    } else {
      roots.push({ shortId, sortKey: t.sortKey ?? "" });
    }
  }
  const cmp = (a, b) =>
    (a.sortKey < b.sortKey ? -1 : a.sortKey > b.sortKey ? 1 : 0) ||
    (a.shortId < b.shortId ? -1 : 1);
  roots.sort(cmp);
  for (const arr of childrenOf.values()) arr.sort(cmp);
  const out = [];
  const visit = (entry) => {
    out.push(entry.shortId);
    const kids = childrenOf.get(entry.shortId);
    if (!kids) return;
    for (const k of kids) visit(k);
  };
  for (const r of roots) visit(r);
  return out;
}

// hasOpenChild is a leaf check: a task is a "leaf" for the Upcoming
// panel when no child is in {available, claimed} (done/canceled
// children don't count as open).
function hasOpenChild(frame, shortId) {
  for (const [, t] of frame.tasks) {
    if (t.parentShortId !== shortId) continue;
    if (t.status === "done" || t.status === "canceled") continue;
    return true;
  }
  return false;
}

// buildUpcoming ports loadUpcoming. Available leaves with no active
// blockers and no open children, in preorder, capped at UPCOMING_LIMIT.
export function buildUpcoming(events, frame, nowSec) {
  const created = taskCreatedAt(events);
  const order = preorderTasks(frame);
  const rows = [];
  for (const shortId of order) {
    const t = frame.tasks.get(shortId);
    if (!t || t.status !== "available") continue;
    if (frame.blocks.has(shortId)) continue;
    if (hasOpenChild(frame, shortId)) continue;
    const ts = created.get(shortId) ?? 0;
    rows.push({
      TaskShortID: shortId,
      TaskURL: "/tasks/" + shortId,
      TaskTitle: t.title ?? "",
      AgeText: relativeTime(nowSec, ts),
      CreatedAtUnix: ts,
    });
    if (rows.length >= UPCOMING_LIMIT) break;
  }
  return { Count: rows.length, Rows: rows };
}

// buildBlocked ports loadBlockedStrip. Tasks with at least one entry
// in frame.blocks, lexically sorted by blocker for determinism.
// Done/canceled blocked tasks are excluded (matches the server's
// status filter).
export function buildBlocked(frame) {
  const rows = [];
  for (const [blockedShortId, blockerSet] of frame.blocks) {
    const t = frame.tasks.get(blockedShortId);
    if (!t) continue;
    if (t.status === "done" || t.status === "canceled") continue;
    const blockers = [...blockerSet].sort();
    rows.push({
      TaskShortID: blockedShortId,
      TaskURL: "/tasks/" + blockedShortId,
      TaskTitle: t.title ?? "",
      Blockers: blockers.map((b) => ({ ShortID: b, URL: "/tasks/" + b })),
    });
    if (rows.length >= BLOCKED_STRIP_LIMIT) break;
  }
  return { Count: rows.length, Rows: rows };
}

// buildHomeFrame is the top-level entry the driver calls. Returns
// the four panels' data shaped for the render module.
export function buildHomeFrame(events, frame, nowSec) {
  return {
    ActiveClaims: buildActiveClaims(events, frame, nowSec),
    RecentCompletions: buildRecentCompletions(events, frame, nowSec),
    Upcoming: buildUpcoming(events, frame, nowSec),
    Blocked: buildBlocked(frame),
  };
}
