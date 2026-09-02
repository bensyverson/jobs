// Tests for internal/web/assets/js/replay.mjs.
//
// Runs under Node's built-in test runner (node --test).
// No browser, no DOM, no deps — the module under test is pure data.
//
// The reducer fold is event-table-driven; each test fixture mirrors a
// single event the server actually produces (verified against samples
// from the local .jobs.db on 2026-04-26). Round-trip tests verify
// applyEvent / reverseEvent compose to identity for every event type
// where the server records the breadcrumbs (post-commit 915916d).

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  initialFrame,
  applyEvent,
  reverseEvent,
  FrameCache,
  ReplayBuffer,
} from "../assets/js/replay.mjs";

// --- helpers ---

// Minimal initial frame factory for tests. Lets each test name only
// the slots it cares about; everything else defaults to empty.
function emptyFrame(eventId = 0) {
  return initialFrame({
    headEventId: eventId,
    tasks: [],
    blocks: [],
    claims: [],
  });
}

// Frames are deep-equal'd field-by-field. Maps and Sets need
// normalization since assert.deepStrictEqual treats them by reference
// in some edge cases — converting to plain objects is robust.
function normalize(frame) {
  return {
    eventId: frame.eventId,
    tasks: Object.fromEntries(
      [...frame.tasks].map(([k, v]) => [
        k,
        { ...v, labels: [...(v.labels ?? [])].sort() },
      ]),
    ),
    blocks: Object.fromEntries(
      [...frame.blocks].map(([k, set]) => [k, [...set].sort()]),
    ),
    claims: Object.fromEntries(frame.claims),
  };
}

function assertFramesEqual(actual, expected, msg) {
  assert.deepStrictEqual(normalize(actual), normalize(expected), msg);
}

// --- initialFrame ---

test("initialFrame: empty payload produces an empty frame", () => {
  const f = initialFrame({
    headEventId: 0,
    tasks: [],
    blocks: [],
    claims: [],
  });
  assert.equal(f.eventId, 0);
  assert.equal(f.tasks.size, 0);
  assert.equal(f.blocks.size, 0);
  assert.equal(f.claims.size, 0);
});

test("initialFrame: hydrates tasks, blocks, claims from the JSON island", () => {
  const f = initialFrame({
    headEventId: 42,
    tasks: [
      {
        shortId: "ABC12",
        title: "Task A",
        description: "desc",
        status: "available",
        parentShortId: null,
        sortKey: "000001",
        labels: ["web", "dx"],
      },
    ],
    blocks: [{ blockedShortId: "ABC12", blockerShortId: "XYZ99" }],
    claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1000 }],
  });

  assert.equal(f.eventId, 42);
  assert.equal(f.tasks.size, 1);
  const t = f.tasks.get("ABC12");
  assert.equal(t.title, "Task A");
  assert.deepStrictEqual([...t.labels].sort(), ["dx", "web"]);
  assert.deepStrictEqual([...f.blocks.get("ABC12")], ["XYZ99"]);
  assert.deepStrictEqual(f.claims.get("ABC12"), {
    claimedBy: "alice",
    expiresAt: 1000,
  });
});

// --- forward fold per event type ---

test("applyEvent created: inserts task with title/desc/parent/sortKey", () => {
  const before = emptyFrame(0);
  const event = {
    id: 1,
    task_id: "ABC12",
    actor: "alice",
    event_type: "created",
    detail: {
      title: "New task",
      description: "desc",
      parent_id: "",
      sort_key: "000005",
    },
  };

  const after = applyEvent(before, event);
  const t = after.tasks.get("ABC12");
  assert.equal(t.title, "New task");
  assert.equal(t.description, "desc");
  assert.equal(t.parentShortId, null);
  assert.equal(t.sortKey, "000005");
  assert.equal(t.status, "available");
  assert.equal(after.eventId, 1);
});

test("applyEvent claimed: sets claim with expires_at; status -> claimed", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 2,
    task_id: "ABC12",
    actor: "alice",
    event_type: "claimed",
    detail: { duration: "30m", expires_at: 1500 },
  });

  assert.equal(after.tasks.get("ABC12").status, "claimed");
  assert.deepStrictEqual(after.claims.get("ABC12"), {
    claimedBy: "alice",
    expiresAt: 1500,
  });
});

test("applyEvent released: clears claim; status -> available", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
    blocks: [],
    claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
  });
  const after = applyEvent(before, {
    id: 3,
    task_id: "ABC12",
    actor: "alice",
    event_type: "released",
    detail: { was_claimed_by: "alice", was_expires_at: 1500 },
  });

  assert.equal(after.tasks.get("ABC12").status, "available");
  assert.equal(after.claims.has("ABC12"), false);
});

test("applyEvent done: status -> done; clears any claim", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
    blocks: [],
    claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
  });
  const after = applyEvent(before, {
    id: 4,
    task_id: "ABC12",
    actor: "alice",
    event_type: "done",
    detail: {
      cascade: false,
      was_status: "claimed",
      was_claimed_by: "alice",
      was_expires_at: 1500,
    },
  });

  assert.equal(after.tasks.get("ABC12").status, "done");
  assert.equal(after.claims.has("ABC12"), false);
});

test("applyEvent canceled: status -> canceled; clears any claim", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
    blocks: [],
    claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
  });
  const after = applyEvent(before, {
    id: 5,
    task_id: "ABC12",
    actor: "alice",
    event_type: "canceled",
    detail: {
      cascade: false,
      was_status: "claimed",
      was_claimed_by: "alice",
      was_expires_at: 1500,
    },
  });

  assert.equal(after.tasks.get("ABC12").status, "canceled");
  assert.equal(after.claims.has("ABC12"), false);
});

test("applyEvent reopened: status -> available", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "done", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 6,
    task_id: "ABC12",
    actor: "alice",
    event_type: "reopened",
    detail: { from_status: "done" },
  });

  assert.equal(after.tasks.get("ABC12").status, "available");
});

test("applyEvent blocked: adds edge to blocks map", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "ABC12", title: "T", status: "available", sortKey: "000000" },
      { shortId: "XYZ99", title: "B", status: "available", sortKey: "000000" },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 7,
    task_id: "ABC12",
    actor: "alice",
    event_type: "blocked",
    detail: { blocked_id: "ABC12", blocker_id: "XYZ99" },
  });

  assert.deepStrictEqual([...after.blocks.get("ABC12")], ["XYZ99"]);
});

test("applyEvent unblocked: removes edge from blocks map", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "ABC12", title: "T", status: "available", sortKey: "000000" },
      { shortId: "XYZ99", title: "B", status: "available", sortKey: "000000" },
    ],
    blocks: [{ blockedShortId: "ABC12", blockerShortId: "XYZ99" }],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 8,
    task_id: "ABC12",
    actor: "alice",
    event_type: "unblocked",
    detail: { blocked_id: "ABC12", blocker_id: "XYZ99", reason: "blocker_done" },
  });

  assert.equal(after.blocks.has("ABC12"), false);
});

test("applyEvent labeled: adds names; existing labels untouched", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        labels: ["existing"],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 9,
    task_id: "ABC12",
    actor: "alice",
    event_type: "labeled",
    detail: { names: ["web", "dx"], existing: ["existing"] },
  });

  assert.deepStrictEqual(
    [...after.tasks.get("ABC12").labels].sort(),
    ["dx", "existing", "web"],
  );
});

test("applyEvent edited: updates title and/or description", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "old title",
        description: "old desc",
        status: "available",
        sortKey: "000000",
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 10,
    task_id: "ABC12",
    actor: "alice",
    event_type: "edited",
    detail: {
      old_title: "old title",
      new_title: "new title",
      old_desc: "old desc",
      new_desc: "new desc",
    },
  });

  const t = after.tasks.get("ABC12");
  assert.equal(t.title, "new title");
  assert.equal(t.description, "new desc");
});

test("applyEvent moved: updates sortKey", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "ABC12", title: "T", status: "available", sortKey: "000005" },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 11,
    task_id: "ABC12",
    actor: "alice",
    event_type: "moved",
    detail: {
      direction: "after",
      relative_to: "XYZ99",
      old_sort_key: "000005",
      sort_key: "000010",
    },
  });

  assert.equal(after.tasks.get("ABC12").sortKey, "000010");
});

test("applyEvent noted: replaces description with description_after", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "before",
        status: "available",
        sortKey: "000000",
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 12,
    task_id: "ABC12",
    actor: "alice",
    event_type: "noted",
    detail: {
      description_after: "before\n\n[note] after",
      text: "after",
    },
  });

  assert.equal(after.tasks.get("ABC12").description, "before\n\n[note] after");
});

test("applyEvent noted: appends a note to task.notes", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "",
        status: "available",
        sortKey: "000000",
      },
    ],
    blocks: [],
    claims: [],
  });
  const event = {
    id: 20,
    task_id: "ABC12",
    actor: "alice",
    created_at: 1700000000,
    event_type: "noted",
    detail: { description_after: "first", text: "first" },
  };
  const after = applyEvent(before, event);
  assert.deepStrictEqual(after.tasks.get("ABC12").notes, [
    { actor: "alice", ts: 1700000000, text: "first" },
  ]);
});

test("applyEvent noted: appends in chronological order across multiple events", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "",
        status: "available",
        sortKey: "000000",
      },
    ],
    blocks: [],
    claims: [],
  });
  const f1 = applyEvent(before, {
    id: 21,
    task_id: "ABC12",
    actor: "alice",
    created_at: 1700000000,
    event_type: "noted",
    detail: { description_after: "first", text: "first" },
  });
  const f2 = applyEvent(f1, {
    id: 22,
    task_id: "ABC12",
    actor: "bob",
    created_at: 1700000060,
    event_type: "noted",
    detail: {
      description_after: "first\n\n[2 mins later] second",
      text: "second",
    },
  });
  assert.deepStrictEqual(f2.tasks.get("ABC12").notes, [
    { actor: "alice", ts: 1700000000, text: "first" },
    { actor: "bob", ts: 1700000060, text: "second" },
  ]);
});

test("applyEvent noted: missing detail.text is skipped (no empty notes)", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "",
        status: "available",
        sortKey: "000000",
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 23,
    task_id: "ABC12",
    actor: "alice",
    created_at: 1700000000,
    event_type: "noted",
    detail: { description_after: "x" }, // no text
  });
  assert.deepStrictEqual(after.tasks.get("ABC12").notes, []);
});

test("initialFrame: hydrates per-task notes from the JSON island", () => {
  const f = initialFrame({
    headEventId: 5,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "first\n\n[then] second",
        status: "available",
        sortKey: "000000",
        notes: [
          { actor: "alice", ts: 1700000000, text: "first" },
          { actor: "bob", ts: 1700000060, text: "second" },
        ],
      },
    ],
    blocks: [],
    claims: [],
  });
  assert.deepStrictEqual(f.tasks.get("ABC12").notes, [
    { actor: "alice", ts: 1700000000, text: "first" },
    { actor: "bob", ts: 1700000060, text: "second" },
  ]);
});

test("initialFrame: missing notes defaults to []", () => {
  const f = initialFrame({
    headEventId: 5,
    tasks: [
      { shortId: "ABC12", title: "T", description: "", status: "available", sortKey: "000000" },
    ],
    blocks: [],
    claims: [],
  });
  assert.deepStrictEqual(f.tasks.get("ABC12").notes, []);
});

test("applyEvent noted: does not mutate the prior frame's notes array", () => {
  // Cloning isolation: pushing to the new frame's notes array must
  // not retroactively mutate the prior frame the cache may still hold.
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        description: "",
        status: "available",
        sortKey: "000000",
        notes: [{ actor: "alice", ts: 1700000000, text: "first" }],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 30,
    task_id: "ABC12",
    actor: "bob",
    created_at: 1700000060,
    event_type: "noted",
    detail: { description_after: "x", text: "second" },
  });
  assert.equal(before.tasks.get("ABC12").notes.length, 1);
  assert.equal(after.tasks.get("ABC12").notes.length, 2);
});

// --- criteria ---

test("initialFrame: hydrates per-task criteria from the JSON island", () => {
  const f = initialFrame({
    headEventId: 5,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [
          { label: "alpha", state: "pending" },
          { label: "beta", state: "passed" },
        ],
      },
    ],
    blocks: [],
    claims: [],
  });
  assert.deepStrictEqual(f.tasks.get("ABC12").criteria, [
    { label: "alpha", state: "pending" },
    { label: "beta", state: "passed" },
  ]);
});

test("initialFrame: missing criteria defaults to []", () => {
  const f = initialFrame({
    headEventId: 0,
    tasks: [
      { shortId: "ABC12", title: "T", status: "available", sortKey: "000000" },
    ],
    blocks: [],
    claims: [],
  });
  assert.deepStrictEqual(f.tasks.get("ABC12").criteria, []);
});

test("applyEvent criteria_added: appends each entry in input order", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [{ label: "existing", state: "pending" }],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 20,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criteria_added",
    detail: {
      criteria: [
        { label: "alpha", state: "pending" },
        { label: "beta", state: "pending" },
      ],
    },
  });

  assert.deepStrictEqual(after.tasks.get("ABC12").criteria, [
    { label: "existing", state: "pending" },
    { label: "alpha", state: "pending" },
    { label: "beta", state: "pending" },
  ]);
  // Cloning isolation: prior frame untouched.
  assert.equal(before.tasks.get("ABC12").criteria.length, 1);
});

test("applyEvent criterion_state: mutates the matching row by label", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [
          { label: "alpha", state: "pending" },
          { label: "beta", state: "pending" },
        ],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 21,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criterion_state",
    detail: { label: "beta", state: "passed", prior: "pending" },
  });

  assert.deepStrictEqual(after.tasks.get("ABC12").criteria, [
    { label: "alpha", state: "pending" },
    { label: "beta", state: "passed" },
  ]);
  // Prior frame's row preserves its old state — prove cloning, not aliasing.
  assert.equal(before.tasks.get("ABC12").criteria[1].state, "pending");
});

test("applyEvent criteria_added: stamps short_id when provided", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 25,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criteria_added",
    detail: {
      criteria: [
        { short_id: "x7e", label: "alpha", state: "pending" },
        { short_id: "InT", label: "beta", state: "pending" },
      ],
    },
  });
  assert.deepStrictEqual(after.tasks.get("ABC12").criteria, [
    { short_id: "x7e", label: "alpha", state: "pending" },
    { short_id: "InT", label: "beta", state: "pending" },
  ]);
});

test("applyEvent criterion_state: matches by short_id when present", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [
          { short_id: "x7e", label: "alpha", state: "pending" },
          { short_id: "InT", label: "beta", state: "pending" },
        ],
      },
    ],
    blocks: [],
    claims: [],
  });
  // Even if the label rotted (label is now "alpha-renamed" in the event),
  // short_id-keyed matching still hits the right row.
  const after = applyEvent(before, {
    id: 26,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criterion_state",
    detail: {
      short_id: "x7e",
      label: "alpha-renamed",
      state: "passed",
      prior: "pending",
    },
  });
  assert.equal(after.tasks.get("ABC12").criteria[0].state, "passed");
  assert.equal(after.tasks.get("ABC12").criteria[1].state, "pending");
});

test("applyEvent criterion_state: legacy label-keyed event still folds", () => {
  // Pre-migration events have no short_id on the detail; they must keep
  // working via label fallback or the existing event log breaks.
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [{ label: "alpha", state: "pending" }],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 27,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criterion_state",
    detail: { label: "alpha", state: "passed", prior: "pending" },
  });
  assert.equal(after.tasks.get("ABC12").criteria[0].state, "passed");
});

test("applyEvent criterion_state: unknown label is a no-op", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [
      {
        shortId: "ABC12",
        title: "T",
        status: "available",
        sortKey: "000000",
        criteria: [{ label: "alpha", state: "pending" }],
      },
    ],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 22,
    task_id: "ABC12",
    actor: "alice",
    event_type: "criterion_state",
    detail: { label: "ghost", state: "passed", prior: "pending" },
  });
  assert.deepStrictEqual(after.tasks.get("ABC12").criteria, [
    { label: "alpha", state: "pending" },
  ]);
});

test("applyEvent claim_expired: clears claim; status -> available", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
    blocks: [],
    claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 500 }],
  });
  const after = applyEvent(before, {
    id: 13,
    task_id: "ABC12",
    actor: "Jobs",
    event_type: "claim_expired",
    detail: { was_claimed_by: "alice", was_expires_at: 500 },
  });

  assert.equal(after.tasks.get("ABC12").status, "available");
  assert.equal(after.claims.has("ABC12"), false);
});

// --- reverse fold (forward-then-reverse identity) ---
//
// Each forward-foldable event type with breadcrumbs round-trips
// to identity. Pre-breadcrumb events (older DBs) are tested
// separately by asserting reverseEvent returns null.

const roundTripCases = [
  {
    name: "created",
    seed: () => emptyFrame(0),
    event: {
      id: 1,
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: {
        title: "T",
        description: "d",
        parent_id: "",
        sort_key: "000001",
      },
    },
  },
  {
    name: "claimed (fresh)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 2,
      task_id: "ABC12",
      actor: "alice",
      event_type: "claimed",
      detail: { duration: "30m", expires_at: 1500 },
    },
  },
  {
    name: "claimed (--force override)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
      blocks: [],
      claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 800 }],
    }),
    event: {
      id: 3,
      task_id: "ABC12",
      actor: "bob",
      event_type: "claimed",
      detail: {
        duration: "30m",
        expires_at: 1500,
        was_claimed_by: "alice",
        was_expires_at: 800,
      },
    },
  },
  {
    name: "released (post-breadcrumb)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
      blocks: [],
      claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
    }),
    event: {
      id: 4,
      task_id: "ABC12",
      actor: "alice",
      event_type: "released",
      detail: { was_claimed_by: "alice", was_expires_at: 1500 },
    },
  },
  {
    name: "done (was claimed)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
      blocks: [],
      claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
    }),
    event: {
      id: 5,
      task_id: "ABC12",
      actor: "alice",
      event_type: "done",
      detail: {
        cascade: false,
        was_status: "claimed",
        was_claimed_by: "alice",
        was_expires_at: 1500,
      },
    },
  },
  {
    name: "done (was available)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 6,
      task_id: "ABC12",
      actor: "alice",
      event_type: "done",
      detail: { cascade: false, was_status: "available" },
    },
  },
  {
    name: "canceled (was claimed)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
      blocks: [],
      claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 1500 }],
    }),
    event: {
      id: 7,
      task_id: "ABC12",
      actor: "alice",
      event_type: "canceled",
      detail: {
        cascade: false,
        reason: "no",
        was_status: "claimed",
        was_claimed_by: "alice",
        was_expires_at: 1500,
      },
    },
  },
  {
    name: "claim_expired",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "claimed", sortKey: "000000" }],
      blocks: [],
      claims: [{ shortId: "ABC12", claimedBy: "alice", expiresAt: 500 }],
    }),
    event: {
      id: 8,
      task_id: "ABC12",
      actor: "Jobs",
      event_type: "claim_expired",
      detail: { was_claimed_by: "alice", was_expires_at: 500 },
    },
  },
  {
    name: "reopened (from done)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "done", sortKey: "000000" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 9,
      task_id: "ABC12",
      actor: "alice",
      event_type: "reopened",
      detail: { from_status: "done" },
    },
  },
  {
    name: "blocked",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        { shortId: "ABC12", title: "T", status: "available", sortKey: "000000" },
        { shortId: "XYZ99", title: "B", status: "available", sortKey: "000000" },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 10,
      task_id: "ABC12",
      actor: "alice",
      event_type: "blocked",
      detail: { blocked_id: "ABC12", blocker_id: "XYZ99" },
    },
  },
  {
    name: "unblocked",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        { shortId: "ABC12", title: "T", status: "available", sortKey: "000000" },
        { shortId: "XYZ99", title: "B", status: "available", sortKey: "000000" },
      ],
      blocks: [{ blockedShortId: "ABC12", blockerShortId: "XYZ99" }],
      claims: [],
    }),
    event: {
      id: 11,
      task_id: "ABC12",
      actor: "alice",
      event_type: "unblocked",
      detail: {
        blocked_id: "ABC12",
        blocker_id: "XYZ99",
        reason: "blocker_done",
      },
    },
  },
  {
    name: "labeled",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        {
          shortId: "ABC12",
          title: "T",
          status: "available",
          sortKey: "000000",
          labels: ["existing"],
        },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 12,
      task_id: "ABC12",
      actor: "alice",
      event_type: "labeled",
      detail: { names: ["web", "dx"], existing: ["existing"] },
    },
  },
  {
    name: "edited",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        {
          shortId: "ABC12",
          title: "old title",
          description: "old desc",
          status: "available",
          sortKey: "000000",
        },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 13,
      task_id: "ABC12",
      actor: "alice",
      event_type: "edited",
      detail: {
        old_title: "old title",
        new_title: "new title",
        old_desc: "old desc",
        new_desc: "new desc",
      },
    },
  },
  {
    name: "moved",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        { shortId: "ABC12", title: "T", status: "available", sortKey: "000005" },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 14,
      task_id: "ABC12",
      actor: "alice",
      event_type: "moved",
      detail: {
        direction: "after",
        relative_to: "XYZ99",
        old_sort_key: "000005",
        sort_key: "000010",
      },
    },
  },
  {
    name: "criteria_added",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        {
          shortId: "ABC12",
          title: "T",
          status: "available",
          sortKey: "000000",
          criteria: [{ label: "existing", state: "pending" }],
        },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 15,
      task_id: "ABC12",
      actor: "alice",
      event_type: "criteria_added",
      detail: {
        criteria: [
          { label: "alpha", state: "pending" },
          { label: "beta", state: "pending" },
        ],
      },
    },
  },
  {
    name: "criterion_state",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [
        {
          shortId: "ABC12",
          title: "T",
          status: "available",
          sortKey: "000000",
          criteria: [
            { label: "alpha", state: "pending" },
            { label: "beta", state: "pending" },
          ],
        },
      ],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 16,
      task_id: "ABC12",
      actor: "alice",
      event_type: "criterion_state",
      detail: { label: "beta", state: "passed", prior: "pending" },
    },
  },
  {
    name: "kind_changed",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ROOT1", title: "R", status: "available", sortKey: "000000", kind: "task" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 17,
      task_id: "ROOT1",
      actor: "alice",
      event_type: "kind_changed",
      detail: { from: "task", to: "issue" },
    },
  },
  {
    name: "found_in_set (first edge)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 18,
      task_id: "ABC12",
      actor: "alice",
      event_type: "found_in_set",
      detail: { task_id: "ABC12", source_id: "SRC99" },
    },
  },
  {
    name: "found_in_set (replacing an edge)",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000", foundIn: "OLD11" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 19,
      task_id: "ABC12",
      actor: "alice",
      event_type: "found_in_set",
      detail: { task_id: "ABC12", source_id: "SRC99", previous_source_id: "OLD11" },
    },
  },
  {
    name: "found_in_cleared",
    seed: () => initialFrame({
      headEventId: 0,
      tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000", foundIn: "SRC99" }],
      blocks: [],
      claims: [],
    }),
    event: {
      id: 20,
      task_id: "ABC12",
      actor: "alice",
      event_type: "found_in_cleared",
      detail: { task_id: "ABC12", source_id: "SRC99" },
    },
  },
];

for (const tc of roundTripCases) {
  test(`reverseEvent ${tc.name}: forward then reverse returns identity`, () => {
    const seed = tc.seed();
    // Round-trip semantics: forward(event with id=X) takes a frame at
    // event id X-1 to one at X; reverse(event id X) goes back. Align
    // the seed's cursor so the reverse lands on the same eventId.
    seed.eventId = tc.event.id - 1;
    const forwarded = applyEvent(seed, tc.event);
    const reversed = reverseEvent(forwarded, tc.event);
    assert.notEqual(reversed, null, "reverse should not be null for breadcrumb-bearing events");
    assertFramesEqual(reversed, seed, "round-trip should equal seed");
  });
}

// --- reverse-fold pre-breadcrumb fallback ---

test("reverseEvent done without was_status: returns null (caller falls back)", () => {
  const seed = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "done", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const event = {
    id: 100,
    task_id: "ABC12",
    actor: "alice",
    event_type: "done",
    detail: { cascade: false }, // pre-breadcrumb event: no was_status
  };
  assert.equal(reverseEvent(seed, event), null);
});

test("reverseEvent released without was_claimed_by: returns null", () => {
  const seed = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "T", status: "available", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const event = {
    id: 101,
    task_id: "ABC12",
    actor: "alice",
    event_type: "released",
    detail: {}, // pre-breadcrumb
  };
  assert.equal(reverseEvent(seed, event), null);
});

// --- FrameCache ---

test("FrameCache: nearestAtOrBefore returns the largest frame <= target", () => {
  const cache = new FrameCache({ snapshotEvery: 50 });
  cache.set(emptyFrame(0));
  cache.set(emptyFrame(50));
  cache.set(emptyFrame(150));

  assert.equal(cache.nearestAtOrBefore(0).eventId, 0);
  assert.equal(cache.nearestAtOrBefore(49).eventId, 0);
  assert.equal(cache.nearestAtOrBefore(50).eventId, 50);
  assert.equal(cache.nearestAtOrBefore(149).eventId, 50);
  assert.equal(cache.nearestAtOrBefore(999).eventId, 150);
});

test("FrameCache: nearestAtOrAfter returns the smallest frame >= target", () => {
  const cache = new FrameCache({ snapshotEvery: 50 });
  cache.set(emptyFrame(50));
  cache.set(emptyFrame(150));

  assert.equal(cache.nearestAtOrAfter(0).eventId, 50);
  assert.equal(cache.nearestAtOrAfter(50).eventId, 50);
  assert.equal(cache.nearestAtOrAfter(51).eventId, 150);
  assert.equal(cache.nearestAtOrAfter(150).eventId, 150);
  assert.equal(cache.nearestAtOrAfter(151), null);
});

test("FrameCache: set is idempotent on event id", () => {
  const cache = new FrameCache({ snapshotEvery: 50 });
  const f1 = emptyFrame(50);
  cache.set(f1);
  cache.set(f1);
  assert.equal(cache.size(), 1);
});

test("FrameCache: shouldSnapshot fires every snapshotEvery events from anchor", () => {
  const cache = new FrameCache({ snapshotEvery: 10 });
  // From anchor=0: snapshot at 10, 20, 30, ...
  assert.equal(cache.shouldSnapshot(5, 0), false);
  assert.equal(cache.shouldSnapshot(10, 0), true);
  assert.equal(cache.shouldSnapshot(20, 0), true);
  assert.equal(cache.shouldSnapshot(25, 0), false);
});

// --- ReplayBuffer ---
//
// ReplayBuffer wires the reducer and cache together with an injected
// async event-fetcher. Tests here use a synchronous fake fetcher
// backed by an in-memory event log, so we can verify behavior without
// any network. Production wires fetchEvents to GET /events?since=X.

// A simple builder for synthetic event sequences: each call appends
// one event and returns the events log + the head frame produced by
// applying every event in order from an initialFrame. Matches the
// shape ReplayBuffer expects.
function buildEventLog(steps) {
  const events = [];
  let frame = initialFrame({ headEventId: 0, tasks: [], blocks: [], claims: [] });
  for (const [i, step] of steps.entries()) {
    const event = { id: i + 1, ...step };
    events.push(event);
    frame = applyEvent(frame, event);
  }
  return { events, headFrame: frame };
}

// A fake fetcher that serves events from an in-memory log. Models the
// /events?since=X&limit=N contract: returns events with id > since,
// up to limit rows. Records calls so tests can assert prefetch /
// caching behavior.
function fakeFetcher(events) {
  const calls = [];
  return {
    calls,
    async fetchEvents({ since = 0, limit = 500 } = {}) {
      calls.push({ since, limit });
      return events
        .filter((e) => e.id > since)
        .slice(0, limit);
    },
  };
}

test("ReplayBuffer: frameAt(head) returns the head frame without fetching", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  const got = await buf.frameAt(headFrame.eventId);
  assert.equal(got.eventId, headFrame.eventId);
  assert.equal(fetcher.calls.length, 0, "head lookup must not hit the fetcher");
});

test("ReplayBuffer: frameAt(earlier id) replays back from head", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "claimed",
      detail: { duration: "30m", expires_at: 1500 },
    },
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "released",
      detail: { was_claimed_by: "alice", was_expires_at: 1500 },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  // At event id 2, alice still holds the claim (released hasn't fired).
  const got = await buf.frameAt(2);
  assert.equal(got.eventId, 2);
  assert.equal(got.tasks.get("ABC12").status, "claimed");
  assert.equal(got.claims.has("ABC12"), true);
});

test("ReplayBuffer: frameAt is memoized — repeated calls reuse the cache", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "claimed",
      detail: { duration: "30m", expires_at: 1500 },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  await buf.frameAt(1);
  const callsAfterFirst = fetcher.calls.length;
  await buf.frameAt(1);
  assert.equal(fetcher.calls.length, callsAfterFirst, "repeated frameAt must not refetch");
});

test("ReplayBuffer: range returns events in [fromId, toId] inclusive", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "A",
      actor: "alice",
      event_type: "created",
      detail: { title: "A", description: "", parent_id: "", sort_key: "000000" },
    },
    {
      task_id: "B",
      actor: "alice",
      event_type: "created",
      detail: { title: "B", description: "", parent_id: "", sort_key: "000000" },
    },
    {
      task_id: "C",
      actor: "alice",
      event_type: "created",
      detail: { title: "C", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  const range = await buf.range(2, 3);
  assert.deepStrictEqual(
    range.map((e) => e.id),
    [2, 3],
  );
});

// --- ReplayBuffer edge cases (Kqxr0) ---
//
// These guard the corners that the happy-path tests don't exercise:
// the genesis frame, navigating past head, empty range queries, and
// the reverse-fold fallback through `noted` events.

test("ReplayBuffer: frameAt(0) returns the pinned genesis frame", async () => {
  // The constructor pins event 0 (empty world) when headEventId > 0;
  // a cursor seek to 0 should resolve from cache without replaying.
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  const got = await buf.frameAt(0);
  assert.equal(got.eventId, 0);
  assert.equal(got.tasks.size, 0, "genesis has no tasks");
  assert.equal(got.blocks.size, 0);
  assert.equal(got.claims.size, 0);
});

test("ReplayBuffer: frameAt past head clamps to head (no events to replay forward)", async () => {
  // Nothing exists past head, so forward replay finds no events and
  // simply returns the anchor frame. Defensive: a stale URL with an
  // ?at= above the live head shouldn't blow up.
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  const got = await buf.frameAt(headFrame.eventId + 1000);
  // We accept "stays at head" as the contract: the task created at id 1
  // is still present.
  assert.equal(got.tasks.has("ABC12"), true);
});

test("ReplayBuffer: range(from, to) with from > to returns []", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "A",
      actor: "alice",
      event_type: "created",
      detail: { title: "A", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fakeFetcher(events).fetchEvents,
  });
  const out = await buf.range(5, 1);
  assert.deepStrictEqual(out, []);
});

test("ReplayBuffer: range past head returns []", async () => {
  const { events, headFrame } = buildEventLog([
    {
      task_id: "A",
      actor: "alice",
      event_type: "created",
      detail: { title: "A", description: "", parent_id: "", sort_key: "000000" },
    },
  ]);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fakeFetcher(events).fetchEvents,
  });
  const out = await buf.range(headFrame.eventId + 1, headFrame.eventId + 50);
  assert.deepStrictEqual(out, []);
});

test("ReplayBuffer: backward replay through `noted` falls back to forward replay from genesis", async () => {
  // `noted` is intentionally non-reversible (the description side
  // would need a description_before payload to undo exactly). When
  // the cursor walks backward across one, _replayBackward returns
  // null and the buffer falls back to forward replay from the nearest
  // earlier snapshot — genesis here. The result must still be correct,
  // including the partial notes list at the cursor position.
  const { events, headFrame } = buildEventLog([
    {
      task_id: "ABC12",
      actor: "alice",
      created_at: 1700000000,
      event_type: "created",
      detail: { title: "T", description: "", parent_id: "", sort_key: "000000" },
    },
    {
      task_id: "ABC12",
      actor: "alice",
      created_at: 1700000010,
      event_type: "noted",
      detail: { description_after: "first note", text: "first" },
    },
    {
      task_id: "ABC12",
      actor: "alice",
      created_at: 1700000020,
      event_type: "noted",
      detail: { description_after: "first note\n\n[t] second", text: "second" },
    },
  ]);
  const fetcher = fakeFetcher(events);
  const buf = new ReplayBuffer({
    headFrame,
    fetchEvents: fetcher.fetchEvents,
  });

  // Seek backward to event 1 (just after creation, before the first
  // note). Forward path requires the reverse-through-noted fallback.
  const got = await buf.frameAt(1);
  assert.equal(got.eventId, 1);
  const t = got.tasks.get("ABC12");
  assert.equal(t.notes.length, 0, "no notes yet at event 1");
  assert.equal(t.description, "");

  // Seek forward to event 2 (after first note only). The frame must
  // hold exactly that one note — proving the fallback didn't over- or
  // under-shoot the cursor.
  const got2 = await buf.frameAt(2);
  assert.equal(got2.eventId, 2);
  assert.equal(got2.tasks.get("ABC12").notes.length, 1);
  assert.equal(got2.tasks.get("ABC12").notes[0].text, "first");
});

// --- tree kind and found-in ---

test("initialFrame: hydrates kind on a root and leaves children without one", () => {
  const f = initialFrame({
    headEventId: 5,
    tasks: [
      { shortId: "ROOT1", title: "Issue root", status: "available", sortKey: "000000", kind: "issue" },
      { shortId: "ROOT2", title: "Task root", status: "available", sortKey: "000000", kind: "task" },
      { shortId: "KID12", title: "Child", status: "available", sortKey: "000000", parentShortId: "ROOT1" },
    ],
    blocks: [],
    claims: [],
  });
  assert.equal(f.tasks.get("ROOT1").kind, "issue");
  assert.equal(f.tasks.get("ROOT2").kind, "task");
  assert.equal(f.tasks.get("KID12").kind, null);
});

test("initialFrame: hydrates foundIn from the JSON island", () => {
  const f = initialFrame({
    headEventId: 5,
    tasks: [
      { shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000", foundIn: "SRC99" },
      { shortId: "SRC99", title: "Source", status: "available", sortKey: "000000" },
    ],
    blocks: [],
    claims: [],
  });
  assert.equal(f.tasks.get("ABC12").foundIn, "SRC99");
  assert.equal(f.tasks.get("SRC99").foundIn, null);
});

test("applyEvent kind_changed: sets the root's kind to detail.to", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ROOT1", title: "R", status: "available", sortKey: "000000", kind: "task" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 40,
    task_id: "ROOT1",
    actor: "alice",
    event_type: "kind_changed",
    detail: { from: "task", to: "issue" },
  });
  assert.equal(after.tasks.get("ROOT1").kind, "issue");
});

test("applyEvent kind_changed: does not mutate the prior frame", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ROOT1", title: "R", status: "available", sortKey: "000000", kind: "task" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 40,
    task_id: "ROOT1",
    actor: "alice",
    event_type: "kind_changed",
    detail: { from: "task", to: "issue" },
  });
  assert.equal(after.tasks.get("ROOT1").kind, "issue");
  assert.equal(before.tasks.get("ROOT1").kind, "task");
});

test("reverseEvent kind_changed: restores detail.from", () => {
  const frame = initialFrame({
    headEventId: 40,
    tasks: [{ shortId: "ROOT1", title: "R", status: "available", sortKey: "000000", kind: "issue" }],
    blocks: [],
    claims: [],
  });
  const back = reverseEvent(frame, {
    id: 40,
    task_id: "ROOT1",
    actor: "alice",
    event_type: "kind_changed",
    detail: { from: "task", to: "issue" },
  });
  assert.notEqual(back, null);
  assert.equal(back.tasks.get("ROOT1").kind, "task");
  assert.equal(frame.tasks.get("ROOT1").kind, "issue", "prior frame must be untouched");
});

test("reverseEvent kind_changed without detail.from: returns null", () => {
  // Guards the breadcrumb check specifically, not the absence of a
  // handler: the same event *with* `from` must reverse cleanly.
  const frame = initialFrame({
    headEventId: 40,
    tasks: [{ shortId: "ROOT1", title: "R", status: "available", sortKey: "000000", kind: "issue" }],
    blocks: [],
    claims: [],
  });
  const event = {
    id: 40,
    task_id: "ROOT1",
    actor: "alice",
    event_type: "kind_changed",
    detail: { to: "issue" },
  };
  assert.equal(reverseEvent(frame, event), null);
  assert.notEqual(
    reverseEvent(frame, { ...event, detail: { from: "task", to: "issue" } }),
    null,
    "the breadcrumb-bearing form must still reverse",
  );
});

test("applyEvent found_in_set: sets task.foundIn to the source id", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 41,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_set",
    detail: { task_id: "ABC12", source_id: "SRC99" },
  });
  assert.equal(after.tasks.get("ABC12").foundIn, "SRC99");
  assert.equal(before.tasks.get("ABC12").foundIn, null, "prior frame must be untouched");
});

test("applyEvent found_in_set: replacing an edge keeps the new source", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000", foundIn: "OLD11" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 42,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_set",
    detail: { task_id: "ABC12", source_id: "SRC99", previous_source_id: "OLD11" },
  });
  assert.equal(after.tasks.get("ABC12").foundIn, "SRC99");
});

test("reverseEvent found_in_set: clears an edge that had no predecessor", () => {
  const frame = initialFrame({
    headEventId: 41,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000", foundIn: "SRC99" }],
    blocks: [],
    claims: [],
  });
  const back = reverseEvent(frame, {
    id: 41,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_set",
    detail: { task_id: "ABC12", source_id: "SRC99" },
  });
  assert.notEqual(back, null);
  assert.equal(back.tasks.get("ABC12").foundIn, null);
  assert.equal(frame.tasks.get("ABC12").foundIn, "SRC99", "prior frame must be untouched");
});

test("reverseEvent found_in_set: restores the displaced source", () => {
  const frame = initialFrame({
    headEventId: 42,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000", foundIn: "SRC99" }],
    blocks: [],
    claims: [],
  });
  const back = reverseEvent(frame, {
    id: 42,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_set",
    detail: { task_id: "ABC12", source_id: "SRC99", previous_source_id: "OLD11" },
  });
  assert.notEqual(back, null);
  assert.equal(back.tasks.get("ABC12").foundIn, "OLD11");
});

test("applyEvent found_in_cleared: clears task.foundIn", () => {
  const before = initialFrame({
    headEventId: 0,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000", foundIn: "SRC99" }],
    blocks: [],
    claims: [],
  });
  const after = applyEvent(before, {
    id: 43,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_cleared",
    detail: { task_id: "ABC12", source_id: "SRC99" },
  });
  assert.equal(after.tasks.get("ABC12").foundIn, null);
  assert.equal(before.tasks.get("ABC12").foundIn, "SRC99", "prior frame must be untouched");
});

test("reverseEvent found_in_cleared: restores the source it cleared", () => {
  const frame = initialFrame({
    headEventId: 43,
    tasks: [{ shortId: "ABC12", title: "Defect", status: "available", sortKey: "000000" }],
    blocks: [],
    claims: [],
  });
  const back = reverseEvent(frame, {
    id: 43,
    task_id: "ABC12",
    actor: "alice",
    event_type: "found_in_cleared",
    detail: { task_id: "ABC12", source_id: "SRC99" },
  });
  assert.notEqual(back, null);
  assert.equal(back.tasks.get("ABC12").foundIn, "SRC99");
});

test("applyEvent kind_changed / found_in_set: unknown task is a no-op, not a throw", () => {
  const before = emptyFrame(0);
  const afterKind = applyEvent(before, {
    id: 44,
    task_id: "GONE1",
    actor: "alice",
    event_type: "kind_changed",
    detail: { from: "task", to: "issue" },
  });
  assert.equal(afterKind.tasks.size, 0);
  const afterEdge = applyEvent(before, {
    id: 45,
    task_id: "GONE1",
    actor: "alice",
    event_type: "found_in_set",
    detail: { task_id: "GONE1", source_id: "SRC99" },
  });
  assert.equal(afterEdge.tasks.size, 0);
});
