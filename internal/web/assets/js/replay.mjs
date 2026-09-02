/*
  Replay buffer for the dashboard's time-travel scrubber.

  Maintains a frame cache that lets the scrubber answer "what did the
  world look like at event N?" without re-reading the full event log
  on every tick. The frame cache is doubly linked: applyEvent applies
  one event's delta forward; reverseEvent uses the prior-state
  breadcrumbs (was_status, was_claimed_by, was_expires_at, plus the
  per-event old_title, old_desc, old_sort_key, from_status, and
  existing-labels payload fields the server already carries) to undo
  a delta.

  Public API (scoped to what the scrubber UI and timeline need):

    initialFrame({ headEventId, tasks, blocks, claims }) -> Frame
        Build the frame the SSR layer hydrated us with — the "head"
        the cache pins as the live state.

    applyEvent(frame, event) -> Frame
        Pure forward fold. Returns a new frame with the event applied.

    reverseEvent(frame, event) -> Frame | null
        Pure reverse fold. Returns null when the breadcrumbs needed to
        undo the event are missing (pre-breadcrumb events, described
        in commit 915916d). Callers fall back to forward replay from a
        snapshot.

    FrameCache({ snapshotEvery })
        Stores frames by event id. nearestAtOrBefore(target) returns
        the largest cached frame <= target; nearestAtOrAfter(target)
        returns the smallest cached frame >= target. set(frame) is
        idempotent on event id; size() reports cache fill;
        shouldSnapshot(eventId, anchor) is the cadence helper used by
        replay loops to decide when to checkpoint.

  Out of scope here: HTTP fetching from /events, view-specific DOM
  updates, the scrubber pill UI. Those land in the per-view *-live.mjs
  modules and the scrubber bootstrap. This module is pure data.
*/

// Frame shape:
//   {
//     eventId: number,
//     tasks: Map<shortId, TaskState>,
//     blocks: Map<blockedShortId, Set<blockerShortId>>,
//     claims: Map<shortId, { claimedBy, expiresAt }>,
//   }
//
// TaskState carries `kind` ("task" | "issue", roots only; null on
// children) and `foundIn` (the short id of the task that surfaced
// this one, or null). Both arrive hydrated from the JSON island and
// are folded by the kind_changed / found_in_set / found_in_cleared
// events. `foundIn` follows the frame's camelCase convention; the event
// detail keys (`source_id`, `previous_source_id`) are the domain's.
//
// TaskState.notes is an Array<{ actor, ts, text }> in chronological
// order. The head frame from the JSON island ships with notes empty —
// SSR renders notes server-side from the event log via loadPlanNotes,
// so live mode doesn't need them. Scrubbing populates notes via the
// forward fold of `noted` events from the genesis snapshot, which the
// frame cache pins.

function defaultTask(shortId) {
  return {
    shortId,
    title: "",
    description: "",
    status: "available",
    parentShortId: null,
    sortKey: "",
    labels: new Set(),
    notes: [],
    criteria: [],
    // kind is the tree kind ("task" | "issue") and is carried on
    // roots only — null on every child, which is how a consumer tells
    // a root apart from a child without walking up. foundIn is the
    // short id of the task that surfaced this one, null when there is
    // no provenance edge. Both mirror the server's frame field names.
    kind: null,
    foundIn: null,
  };
}

function cloneFrame(frame) {
  const tasks = new Map();
  for (const [k, v] of frame.tasks) {
    tasks.set(k, {
      ...v,
      labels: new Set(v.labels),
      notes: v.notes ? v.notes.slice() : [],
      criteria: v.criteria
        ? v.criteria.map((c) => ({ ...c }))
        : [],
    });
  }
  const blocks = new Map();
  for (const [k, set] of frame.blocks) {
    blocks.set(k, new Set(set));
  }
  return {
    eventId: frame.eventId,
    tasks,
    blocks,
    claims: new Map(frame.claims),
  };
}

export function initialFrame(payload) {
  const tasks = new Map();
  for (const t of payload.tasks ?? []) {
    tasks.set(t.shortId, {
      ...defaultTask(t.shortId),
      ...t,
      parentShortId: t.parentShortId ?? null,
      labels: new Set(t.labels ?? []),
      notes: Array.isArray(t.notes) ? t.notes.map((n) => ({ ...n })) : [],
      criteria: Array.isArray(t.criteria) ? t.criteria.map((c) => ({ ...c })) : [],
    });
  }
  const blocks = new Map();
  for (const b of payload.blocks ?? []) {
    let set = blocks.get(b.blockedShortId);
    if (!set) {
      set = new Set();
      blocks.set(b.blockedShortId, set);
    }
    set.add(b.blockerShortId);
  }
  const claims = new Map();
  for (const c of payload.claims ?? []) {
    claims.set(c.shortId, { claimedBy: c.claimedBy, expiresAt: c.expiresAt });
  }
  return {
    eventId: payload.headEventId ?? 0,
    tasks,
    blocks,
    claims,
  };
}

// applyEvent dispatch — one tiny helper per event type. Each helper
// receives a *cloned* frame it may mutate, plus the event envelope.
const FORWARD = {
  created(frame, event) {
    const detail = event.detail ?? {};
    frame.tasks.set(event.task_id, {
      ...defaultTask(event.task_id),
      title: detail.title ?? "",
      description: detail.description ?? "",
      parentShortId: detail.parent_id ? detail.parent_id : null,
      sortKey: detail.sort_key ?? "",
      status: "available",
      labels: new Set(),
    });
  },

  claimed(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "claimed";
    frame.claims.set(event.task_id, {
      claimedBy: event.actor,
      expiresAt: detail.expires_at ?? 0,
    });
  },

  released(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "available";
    frame.claims.delete(event.task_id);
  },

  done(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "done";
    frame.claims.delete(event.task_id);
  },

  canceled(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "canceled";
    frame.claims.delete(event.task_id);
  },

  reopened(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "available";
  },

  blocked(frame, event) {
    const detail = event.detail ?? {};
    const blocked = detail.blocked_id;
    const blocker = detail.blocker_id;
    if (!blocked || !blocker) return;
    let set = frame.blocks.get(blocked);
    if (!set) {
      set = new Set();
      frame.blocks.set(blocked, set);
    }
    set.add(blocker);
  },

  unblocked(frame, event) {
    const detail = event.detail ?? {};
    const blocked = detail.blocked_id;
    const blocker = detail.blocker_id;
    if (!blocked || !blocker) return;
    const set = frame.blocks.get(blocked);
    if (!set) return;
    set.delete(blocker);
    if (set.size === 0) frame.blocks.delete(blocked);
  },

  labeled(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    for (const name of detail.names ?? []) t.labels.add(name);
  },

  edited(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    if (detail.new_title !== undefined) t.title = detail.new_title;
    if (detail.new_desc !== undefined) t.description = detail.new_desc;
  },

  moved(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    if (detail.sort_key !== undefined) t.sortKey = detail.sort_key;
  },

  noted(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    if (detail.description_after !== undefined) {
      t.description = detail.description_after;
    }
    // Track the note as its own record so per-view glue can render
    // c-plan-note rows in history mode without re-querying the event
    // log. Skip empty text — the server treats empty notes as invalid
    // input, but a defensive check keeps a malformed payload from
    // injecting a blank row.
    const text = typeof detail.text === "string" ? detail.text : "";
    if (text === "") return;
    t.notes = t.notes ? t.notes.slice() : [];
    t.notes.push({
      actor: event.actor,
      ts: event.created_at,
      text,
    });
  },

  claim_expired(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "available";
    frame.claims.delete(event.task_id);
  },

  criteria_added(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    const items = Array.isArray(detail.criteria) ? detail.criteria : [];
    t.criteria = t.criteria ? t.criteria.slice() : [];
    for (const c of items) {
      const entry = { label: c.label, state: c.state ?? "pending" };
      if (c.short_id) entry.short_id = c.short_id;
      t.criteria.push(entry);
    }
  },

  criterion_state(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t || !Array.isArray(t.criteria)) return;
    // Prefer short_id (the stable identity); fall back to label for
    // events recorded before migration 0005, and for legacy callers
    // that still pass labels.
    const idx = findCriterionIndex(t.criteria, detail);
    if (idx < 0) return;
    t.criteria = t.criteria.slice();
    t.criteria[idx] = { ...t.criteria[idx], state: detail.state };
  },

  kind_changed(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t || detail.to === undefined) return;
    t.kind = detail.to;
  },

  found_in_set(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t || !detail.source_id) return;
    t.foundIn = detail.source_id;
  },

  found_in_cleared(frame, event) {
    const t = frame.tasks.get(event.task_id);
    if (!t) return;
    t.foundIn = null;
  },
};

// findCriterionIndex resolves a criterion_state event's target back to its
// index in the criteria array, preferring short_id when present. Both the
// forward and reverse folds share this resolver so the matching rule
// stays in one place.
function findCriterionIndex(criteria, detail) {
  if (detail.short_id) {
    const i = criteria.findIndex((c) => c.short_id === detail.short_id);
    if (i >= 0) return i;
  }
  if (detail.label !== undefined) {
    return criteria.findIndex((c) => c.label === detail.label);
  }
  return -1;
}

export function applyEvent(frame, event) {
  const next = cloneFrame(frame);
  const handler = FORWARD[event.event_type];
  if (handler) handler(next, event);
  next.eventId = event.id;
  return next;
}

// Reverse helpers. Each returns true on success or false when the
// payload is missing the breadcrumbs needed to invert. reverseEvent
// short-circuits to null on first false.
const REVERSE = {
  created(frame, event) {
    frame.tasks.delete(event.task_id);
    return true;
  },

  claimed(frame, event) {
    const detail = event.detail ?? {};
    if (detail.was_claimed_by !== undefined) {
      // Override path: restore the displaced holder.
      frame.claims.set(event.task_id, {
        claimedBy: detail.was_claimed_by,
        expiresAt: detail.was_expires_at ?? 0,
      });
      const t = frame.tasks.get(event.task_id);
      if (t) t.status = "claimed";
    } else {
      // Fresh claim: simply remove and revert task to available.
      frame.claims.delete(event.task_id);
      const t = frame.tasks.get(event.task_id);
      if (t) t.status = "available";
    }
    return true;
  },

  released(frame, event) {
    const detail = event.detail ?? {};
    if (detail.was_claimed_by === undefined) return false;
    frame.claims.set(event.task_id, {
      claimedBy: detail.was_claimed_by,
      expiresAt: detail.was_expires_at ?? 0,
    });
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "claimed";
    return true;
  },

  done(frame, event) {
    const detail = event.detail ?? {};
    if (detail.was_status === undefined) return false;
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = detail.was_status;
    if (detail.was_status === "claimed" && detail.was_claimed_by !== undefined) {
      frame.claims.set(event.task_id, {
        claimedBy: detail.was_claimed_by,
        expiresAt: detail.was_expires_at ?? 0,
      });
    }
    return true;
  },

  canceled(frame, event) {
    return REVERSE.done(frame, event);
  },

  reopened(frame, event) {
    const detail = event.detail ?? {};
    if (detail.from_status === undefined) return false;
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = detail.from_status;
    return true;
  },

  blocked(frame, event) {
    const detail = event.detail ?? {};
    const blocked = detail.blocked_id;
    const blocker = detail.blocker_id;
    if (!blocked || !blocker) return false;
    const set = frame.blocks.get(blocked);
    if (set) {
      set.delete(blocker);
      if (set.size === 0) frame.blocks.delete(blocked);
    }
    return true;
  },

  unblocked(frame, event) {
    const detail = event.detail ?? {};
    const blocked = detail.blocked_id;
    const blocker = detail.blocker_id;
    if (!blocked || !blocker) return false;
    let set = frame.blocks.get(blocked);
    if (!set) {
      set = new Set();
      frame.blocks.set(blocked, set);
    }
    set.add(blocker);
    return true;
  },

  labeled(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return true;
    const existing = new Set(detail.existing ?? []);
    for (const name of detail.names ?? []) {
      if (!existing.has(name)) t.labels.delete(name);
    }
    return true;
  },

  edited(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return true;
    if (detail.new_title !== undefined && detail.old_title === undefined) return false;
    if (detail.new_desc !== undefined && detail.old_desc === undefined) return false;
    if (detail.old_title !== undefined) t.title = detail.old_title;
    if (detail.old_desc !== undefined) t.description = detail.old_desc;
    return true;
  },

  moved(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return true;
    if (detail.old_sort_key === undefined) return false;
    t.sortKey = detail.old_sort_key;
    return true;
  },

  noted(_frame, _event) {
    // noted carries description_after but no description_before.
    // Reverse-fold isn't exact for the description field. Caller
    // falls back to forward replay from a snapshot.
    return false;
  },

  claim_expired(frame, event) {
    const detail = event.detail ?? {};
    if (detail.was_claimed_by === undefined) return false;
    frame.claims.set(event.task_id, {
      claimedBy: detail.was_claimed_by,
      expiresAt: detail.was_expires_at ?? 0,
    });
    const t = frame.tasks.get(event.task_id);
    if (t) t.status = "claimed";
    return true;
  },

  criteria_added(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return true;
    const items = Array.isArray(detail.criteria) ? detail.criteria : [];
    if (items.length === 0) return true;
    // Forward fold appends in input order; reverse pops the same count
    // off the end. We don't try to undo by label since the forward
    // path is a tail-append and order is the only invariant.
    if (!Array.isArray(t.criteria) || t.criteria.length < items.length) {
      return false;
    }
    t.criteria = t.criteria.slice(0, t.criteria.length - items.length);
    return true;
  },

  criterion_state(frame, event) {
    const detail = event.detail ?? {};
    if (detail.prior === undefined) return false;
    const t = frame.tasks.get(event.task_id);
    if (!t || !Array.isArray(t.criteria)) return true;
    const idx = findCriterionIndex(t.criteria, detail);
    if (idx < 0) return true;
    t.criteria = t.criteria.slice();
    t.criteria[idx] = { ...t.criteria[idx], state: detail.prior };
    return true;
  },

  kind_changed(frame, event) {
    const detail = event.detail ?? {};
    if (detail.from === undefined) return false;
    const t = frame.tasks.get(event.task_id);
    if (t) t.kind = detail.from;
    return true;
  },

  found_in_set(frame, event) {
    const detail = event.detail ?? {};
    const t = frame.tasks.get(event.task_id);
    if (!t) return true;
    // previous_source_id is recorded only when the set displaced a
    // *different* source, so its absence means there was no edge —
    // except when the same source was set twice, which the payload
    // cannot distinguish from a first set. That case reverses to null
    // where the edge in fact stood; it is the forward fold's no-op, so
    // the artifact is confined to scrub positions between the two
    // redundant events and is not detectable from the payload. Fixing
    // it means recording previous_source_id unconditionally in
    // internal/job/foundin.go, which also changes how `job log`
    // renders an identical re-set.
    t.foundIn = detail.previous_source_id ?? null;
    return true;
  },

  found_in_cleared(frame, event) {
    const detail = event.detail ?? {};
    if (!detail.source_id) return false;
    const t = frame.tasks.get(event.task_id);
    if (t) t.foundIn = detail.source_id;
    return true;
  },
};

export function reverseEvent(frame, event) {
  const handler = REVERSE[event.event_type];
  if (!handler) return null;
  const next = cloneFrame(frame);
  next.eventId = event.id - 1;
  if (!handler(next, event)) return null;
  return next;
}

// FrameCache stores frames by event id and answers "nearest snapshot"
// queries. The cache is a Map plus a sorted index of event ids; both
// are kept in sync. Insertions are O(log n) via binary search; lookups
// are O(log n). Cache eviction is intentionally not implemented in v1
// — the dashboard's event volume in practice is low enough that the
// memory footprint is bounded by the head event id divided by
// snapshotEvery, which is fine for a local-first tool.
export class FrameCache {
  constructor(opts = {}) {
    this.snapshotEvery = opts.snapshotEvery ?? 50;
    this.frames = new Map(); // eventId -> Frame
    this.sortedIds = [];     // sorted ascending
  }

  set(frame) {
    if (this.frames.has(frame.eventId)) return;
    this.frames.set(frame.eventId, frame);
    // Insertion-sort into sortedIds.
    const id = frame.eventId;
    let lo = 0;
    let hi = this.sortedIds.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.sortedIds[mid] < id) lo = mid + 1;
      else hi = mid;
    }
    this.sortedIds.splice(lo, 0, id);
  }

  size() {
    return this.frames.size;
  }

  // nearestAtOrBefore(target) returns the cached frame with the
  // largest event id <= target, or null if no such frame exists.
  nearestAtOrBefore(target) {
    let lo = 0;
    let hi = this.sortedIds.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.sortedIds[mid] <= target) lo = mid + 1;
      else hi = mid;
    }
    // After the loop, lo is one past the last id <= target.
    if (lo === 0) return null;
    return this.frames.get(this.sortedIds[lo - 1]);
  }

  // nearestAtOrAfter(target) returns the cached frame with the
  // smallest event id >= target, or null if no such frame exists.
  nearestAtOrAfter(target) {
    let lo = 0;
    let hi = this.sortedIds.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.sortedIds[mid] < target) lo = mid + 1;
      else hi = mid;
    }
    if (lo === this.sortedIds.length) return null;
    return this.frames.get(this.sortedIds[lo]);
  }

  // shouldSnapshot decides whether a replay loop should checkpoint
  // after applying the event with id `eventId`, given that the loop
  // started from an anchor with id `anchor`. Snapshots fire every
  // snapshotEvery events from the anchor.
  shouldSnapshot(eventId, anchor) {
    if (eventId === anchor) return false;
    return (eventId - anchor) % this.snapshotEvery === 0;
  }
}

// ReplayBuffer wraps the reducer and frame cache with an injected
// async event fetcher so the scrubber can ask "what did the world
// look like at event N?" without round-tripping the full event log.
//
// Constructor: { headFrame, fetchEvents, snapshotEvery? }
//   headFrame    — the live frame produced by initialFrame() at page
//                  load. Pinned in the cache; the head answer is
//                  zero-cost.
//   fetchEvents  — async ({ since, limit }) -> Promise<Event[]>. The
//                  contract mirrors GET /events?since=X&limit=N: the
//                  function must return events with id > since,
//                  ordered ascending, up to limit rows.
//   snapshotEvery — optional cadence override; defaults to 50.
//
// Methods:
//   async frameAt(target)  -> Frame at event id `target`.
//   async range(from, to)  -> Event[] with id in [from, to] inclusive.
//
// Internal events are cached in a Map<id, Event> so repeated lookups
// don't refetch. The cache grows monotonically in v1; eviction is a
// later concern (event-volume bounded by the dashboard's local-first
// scope).
export class ReplayBuffer {
  constructor({ headFrame, fetchEvents, snapshotEvery = 50 }) {
    if (!headFrame) throw new Error("ReplayBuffer: headFrame required");
    if (typeof fetchEvents !== "function") {
      throw new Error("ReplayBuffer: fetchEvents must be a function");
    }
    this.headFrame = headFrame;
    this.fetchEvents = fetchEvents;
    this.frames = new FrameCache({ snapshotEvery });
    // Pin head and genesis frames. Genesis (event 0, empty world)
    // bounds backward replays; head bounds forward replays.
    this.frames.set(headFrame);
    if (headFrame.eventId !== 0) {
      this.frames.set(
        initialFrame({ headEventId: 0, tasks: [], blocks: [], claims: [] }),
      );
    }
    this.events = new Map();
    this._fetchPromise = null;
  }

  // Lazily loads events through the head event id. Subsequent calls
  // are no-ops; concurrent calls share the same in-flight promise so
  // we never issue two parallel fetches.
  async _ensureEventsLoaded() {
    if (this._fetchPromise) return this._fetchPromise;
    this._fetchPromise = (async () => {
      const fetched = await this.fetchEvents({
        since: 0,
        limit: this.headFrame.eventId,
      });
      for (const e of fetched) this.events.set(e.id, e);
    })();
    return this._fetchPromise;
  }

  async frameAt(target) {
    if (target === this.headFrame.eventId) return this.headFrame;
    await this._ensureEventsLoaded();

    // Pick replay direction. Forward from nearest snapshot <= target,
    // or backward from nearest snapshot >= target — whichever is fewer
    // events. Snapshot-at-exact-target is already handled by
    // nearestAtOrBefore returning that snapshot and the forward loop
    // running zero iterations.
    const before = this.frames.nearestAtOrBefore(target);
    const after = this.frames.nearestAtOrAfter(target);
    const distFwd = before ? target - before.eventId : Infinity;
    const distBwd = after ? after.eventId - target : Infinity;

    let frame;
    if (distFwd <= distBwd && before) {
      frame = this._replayForward(before, target);
    } else if (after) {
      frame = this._replayBackward(after, target, before);
    } else if (before) {
      frame = this._replayForward(before, target);
    } else {
      // No anchors at all — shouldn't happen because we pin head and
      // genesis. Defensive fallback to a fresh genesis frame.
      frame = initialFrame({ headEventId: 0, tasks: [], blocks: [], claims: [] });
    }

    this.frames.set(frame);
    return frame;
  }

  _replayForward(anchor, target) {
    let frame = anchor;
    for (let id = anchor.eventId + 1; id <= target; id++) {
      const e = this.events.get(id);
      if (!e) continue; // gap; should not happen after _ensureEventsLoaded
      frame = applyEvent(frame, e);
      if (this.frames.shouldSnapshot(id, anchor.eventId)) this.frames.set(frame);
    }
    return frame;
  }

  _replayBackward(anchor, target, fallbackBefore) {
    let frame = anchor;
    for (let id = anchor.eventId; id > target; id--) {
      const e = this.events.get(id);
      if (!e) continue;
      const reversed = reverseEvent(frame, e);
      if (reversed === null) {
        // An event in this range can't be reversed (pre-breadcrumb
        // or inherently lossy like noted). Fall back to forward
        // replay from the nearest snapshot <= target.
        const start =
          fallbackBefore ??
          initialFrame({ headEventId: 0, tasks: [], blocks: [], claims: [] });
        return this._replayForward(start, target);
      }
      frame = reversed;
    }
    return frame;
  }

  async range(fromId, toId) {
    await this._ensureEventsLoaded();
    const out = [];
    for (let id = fromId; id <= toId; id++) {
      const e = this.events.get(id);
      if (e) out.push(e);
    }
    return out;
  }
}
