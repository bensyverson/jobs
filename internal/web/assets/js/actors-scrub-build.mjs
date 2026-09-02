/*
  Pure-data layer for the Actors-board scrubber.

  Ports loadActorColumns from internal/web/handlers/actors.go to JS so
  the /actors view can rebuild itself off the in-memory event log when
  the scrubber moves the cursor. Same shapes, same defaults, same
  rollup rules — the column DOM matches what plan.go would render at
  the cursor's log position.

  Inputs:
    events  Array<Event>  — events with id <= cursor, in id-asc order.
                            Wire shape from /events; created_at is
                            unix seconds (the bootstrap layer
                            normalizes RFC3339 → seconds).
    frame   Frame         — used for task title/description lookup
                            (the events carry task_id but not the
                            current title; frame.tasks resolves that).
    nowSec  number        — cursor event's created_at, frozen so
                            ageText reflects the historical moment.
    cutoffSec number       — ?range= lower bound in unix seconds;
                            events older than it are dropped before
                            the rollup, mirroring the server's
                            `e.created_at >= ?` clause. 0 (the
                            default) is the unbounded "all" range.
                            Measured back from nowSec, so scrubbing
                            moves the window with the cursor.
*/

import { relativeTime } from "./scrub-util.mjs";

// COLUMN_CARD_LIMIT mirrors handlers.ActorColumnCardLimit. Active
// claims are always retained; only history truncates when full.
export const COLUMN_CARD_LIMIT = 100;

// stateChangingTypes mirrors handlers.stateChangingTypes server-side.
const STATE_CHANGING = new Set([
  "created",
  "claimed",
  "done",
  "blocked",
  "unblocked",
  "released",
  "canceled",
]);

// Events that clear the per-task claimer derived from the walk.
// `claimed` itself (re)sets the claimer; everything below clears it.
const CLAIM_CLEARING = new Set([
  "released",
  "done",
  "canceled",
  "claim_expired",
  "reopened",
]);

export function noteCountLabel(n) {
  if (n <= 0) return "";
  if (n === 1) return "1 note";
  return `${n} notes`;
}

export function actorStatusText(claimCount, lastSeen) {
  if (claimCount === 0) return `idle · last seen ${lastSeen}`;
  if (claimCount === 1) return `1 claim · last seen ${lastSeen}`;
  return `${claimCount} claims · last seen ${lastSeen}`;
}

// buildActorColumns walks the event stream once, accumulating per-
// (actor, task) cards plus per-actor and per-task state. Returns the
// columns sorted by lastSeen desc with name as the tiebreak. Empty
// actor strings are skipped (matches the server's `actor <> ''`
// filter).
//
// Each card's verb / verbClass / stateClass is set by the latest
// state-changing event for that pair; `noted` events fold into
// noteCount only. Pairs that received only `noted` events stay
// invisible — without a state-changer there's no card body to render.
//
// IsClaim is computed from currentClaimer: per-task, the last actor
// whose `claimed` event hasn't been undone by a clearing event. So
// scrubbing through a `claim --force` correctly demotes the prior
// holder's card to history while the new holder's card stays in the
// claim band.
//
// cutoffSec is applied at the top of the walk, exactly where the
// server applies it in SQL: an actor with no event in the window gets
// no column at all, and a surviving column carries only the cards its
// in-window events produced.
export function buildActorColumns(events, frame, nowSec, cutoffSec = 0) {
  const pairs = new Map(); // "actor\0taskID" -> pairAccum
  const actors = new Map(); // actor -> { lastSeen, pairOrder: [key,...] }
  const currentClaimer = new Map(); // taskID -> actor (or absent)

  const pairKey = (actor, taskID) => actor + "\u0000" + taskID;

  const ensureActor = (name) => {
    let a = actors.get(name);
    if (!a) {
      a = { lastSeen: 0, pairOrder: [], claimCount: 0 };
      actors.set(name, a);
    }
    return a;
  };

  for (const e of events) {
    if (!e.actor) continue; // skip empty-actor events (system rows the
    //                         server filters via SQL)
    if (cutoffSec > 0 && e.created_at < cutoffSec) continue; // out of range
    const taskID = e.task_id;
    const key = pairKey(e.actor, taskID);
    let p = pairs.get(key);
    if (!p) {
      const task = frame.tasks.get(taskID);
      p = {
        actor: e.actor,
        taskID,
        hasState: false,
        noteCount: 0,
        eventAt: 0,
        verb: "",
        taskTitle: task?.title ?? "",
        taskDesc: task?.description ?? "",
      };
      pairs.set(key, p);
      const a = ensureActor(e.actor);
      a.pairOrder.push(key);
    }

    if (e.event_type === "noted") {
      p.noteCount++;
    } else if (STATE_CHANGING.has(e.event_type)) {
      p.verb = e.event_type;
      p.eventAt = e.created_at;
      p.hasState = true;
    }

    if (e.event_type === "claimed") {
      currentClaimer.set(taskID, e.actor);
    } else if (CLAIM_CLEARING.has(e.event_type)) {
      currentClaimer.delete(taskID);
    }

    const a = ensureActor(e.actor);
    if (e.created_at > a.lastSeen) a.lastSeen = e.created_at;
  }

  const cols = [];
  for (const [actorName, a] of actors) {
    const claimCards = [];
    const historyCards = [];
    for (const key of a.pairOrder) {
      const p = pairs.get(key);
      if (!p.hasState) continue;
      const isClaim = currentClaimer.get(p.taskID) === actorName;
      if (isClaim) a.claimCount++;
      const card = {
        stateClass: "c-actor-card--" + p.verb,
        verb: p.verb,
        verbClass: "c-log-row__verb--" + p.verb,
        ageText: relativeTime(nowSec, p.eventAt),
        noteCount: p.noteCount,
        noteText: noteCountLabel(p.noteCount),
        taskShortID: p.taskID,
        taskURL: "/tasks/" + p.taskID,
        taskTitle: p.taskTitle,
        taskDesc: p.taskDesc,
        isClaim,
        eventAt: p.eventAt,
        cardKey: actorName + ":" + p.taskID,
      };
      if (isClaim) claimCards.push(card);
      else historyCards.push(card);
    }
    // Newest-first within each band; CSS column-reverse flips the
    // visual order so DOM-first claims dock at the bottom.
    claimCards.sort((x, y) => y.eventAt - x.eventAt);
    historyCards.sort((x, y) => y.eventAt - x.eventAt);

    // Cap: claims always retained, history fills the remainder.
    const budget = Math.max(0, COLUMN_CARD_LIMIT - claimCards.length);
    const trimmed = historyCards.length > budget ? historyCards.slice(0, budget) : historyCards;

    const lastSeenText = relativeTime(nowSec, a.lastSeen);
    cols.push({
      name: actorName,
      url: "/actors/" + encodeURIComponent(actorName),
      idle: a.claimCount === 0,
      claimCount: a.claimCount,
      statusText: actorStatusText(a.claimCount, lastSeenText),
      cards: [...claimCards, ...trimmed],
      lastSeen: a.lastSeen,
    });
  }

  cols.sort((x, y) => {
    if (x.lastSeen !== y.lastSeen) return y.lastSeen - x.lastSeen;
    return x.name < y.name ? -1 : x.name > y.name ? 1 : 0;
  });
  return cols;
}
