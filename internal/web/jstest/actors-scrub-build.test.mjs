// Tests for internal/web/assets/js/actors-scrub-build.mjs.
//
// Ports loadActorColumns (internal/web/handlers/actors.go) to JS so the
// /actors board can rebuild itself from the scrubber's in-memory event
// log. Pure data layer — events + frame in, ActorColumn[] out. The
// driver wires this to the scrubber's CustomEvent and the renderer.
//
// Event shape mirrors the wire format from /events: { id, actor,
// event_type, created_at, task_id }. created_at is unix seconds (the
// scrubber-bootstrap layer normalizes RFC3339 → seconds).

import { test } from "node:test";
import assert from "node:assert/strict";

import { initialFrame } from "../assets/js/replay.mjs";
import {
  buildActorColumns,
  noteCountLabel,
  actorStatusText,
  COLUMN_CARD_LIMIT,
} from "../assets/js/actors-scrub-build.mjs";

// --- helpers ---

function frameWithTasks(tasks) {
  return initialFrame({ headEventId: 0, tasks, blocks: [], claims: [] });
}

function evt(id, actor, type, taskID, createdAt = 1700000000) {
  return { id, actor, event_type: type, task_id: taskID, created_at: createdAt };
}

// --- noteCountLabel / actorStatusText ---

test("noteCountLabel: empty for 0, '1 note' for 1, 'N notes' for >1", () => {
  assert.equal(noteCountLabel(0), "");
  assert.equal(noteCountLabel(1), "1 note");
  assert.equal(noteCountLabel(7), "7 notes");
});

test("actorStatusText: idle / 1 claim / N claims with last seen suffix", () => {
  assert.equal(actorStatusText(0, "5m"), "idle · last seen 5m");
  assert.equal(actorStatusText(1, "1h"), "1 claim · last seen 1h");
  assert.equal(actorStatusText(3, "2d"), "3 claims · last seen 2d");
});

// --- buildActorColumns ---

test("buildActorColumns: single actor, single created event → one history card", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Build it", description: "do the thing", status: "available", sortKey: "000000" },
  ]);
  const events = [evt(1, "alice", "created", "T0001")];

  const cols = buildActorColumns(events, frame, 1700000060);
  assert.equal(cols.length, 1);
  assert.equal(cols[0].name, "alice");
  assert.equal(cols[0].cards.length, 1);
  assert.equal(cols[0].cards[0].verb, "created");
  assert.equal(cols[0].cards[0].isClaim, false);
  assert.equal(cols[0].claimCount, 0);
  assert.equal(cols[0].idle, true);
});

test("buildActorColumns: claim then done → card ends as 'done', not IsClaim, in history band", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Task", status: "done", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "created", "T0001", 1700000000),
    evt(2, "alice", "claimed", "T0001", 1700000010),
    evt(3, "alice", "done", "T0001", 1700000020),
  ];
  const cols = buildActorColumns(events, frame, 1700000099);
  assert.equal(cols[0].cards[0].verb, "done");
  assert.equal(cols[0].cards[0].isClaim, false);
  assert.equal(cols[0].claimCount, 0);
});

test("buildActorColumns: active claim → IsClaim true, claimCount 1, idle false", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Task", status: "claimed", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "created", "T0001"),
    evt(2, "alice", "claimed", "T0001"),
  ];
  const cols = buildActorColumns(events, frame, 1700000060);
  assert.equal(cols[0].cards[0].verb, "claimed");
  assert.equal(cols[0].cards[0].isClaim, true);
  assert.equal(cols[0].claimCount, 1);
  assert.equal(cols[0].idle, false);
});

test("buildActorColumns: notes fold into noteCount; do not create separate cards", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "claimed", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "claimed", "T0001"),
    evt(2, "alice", "noted", "T0001"),
    evt(3, "alice", "noted", "T0001"),
  ];
  const cols = buildActorColumns(events, frame, 1700000060);
  assert.equal(cols[0].cards.length, 1);
  assert.equal(cols[0].cards[0].noteCount, 2);
  assert.equal(cols[0].cards[0].noteText, "2 notes");
});

test("buildActorColumns: claim --force override moves IsClaim to the new claimer", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "claimed", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "claimed", "T0001"),
    evt(2, "bob", "claimed", "T0001"),
  ];
  const cols = buildActorColumns(events, frame, 1700000060);
  const alice = cols.find((c) => c.name === "alice");
  const bob = cols.find((c) => c.name === "bob");
  assert.equal(alice.cards[0].isClaim, false);
  assert.equal(bob.cards[0].isClaim, true);
  assert.equal(bob.claimCount, 1);
});

test("buildActorColumns: claim_expired clears claimer; system 'Jobs' actor gets its own column", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "claimed", "T0001", 1700000000),
    evt(2, "Jobs", "claim_expired", "T0001", 1700000999),
  ];
  const cols = buildActorColumns(events, frame, 1700001000);
  const alice = cols.find((c) => c.name === "alice");
  assert.equal(alice.cards[0].isClaim, false);
  assert.equal(alice.claimCount, 0);
});

test("buildActorColumns: columns ordered by lastSeen desc, name asc tiebreak", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "created", "T0001", 1700000000),
    evt(2, "bob", "claimed", "T0001", 1700000050),
    evt(3, "carol", "released", "T0001", 1700000050), // tie with bob
  ];
  const cols = buildActorColumns(events, frame, 1700000099);
  assert.deepStrictEqual(
    cols.map((c) => c.name),
    ["bob", "carol", "alice"], // bob/carol tie at 1700000050 → name asc; alice older
  );
});

test("buildActorColumns: state-changing event sets stateClass / verb / verbClass / cardKey", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  const events = [evt(1, "alice", "blocked", "T0001")];
  const card = buildActorColumns(events, frame, 1700000099)[0].cards[0];
  assert.equal(card.stateClass, "c-actor-card--blocked");
  assert.equal(card.verb, "blocked");
  assert.equal(card.verbClass, "c-log-row__verb--blocked");
  assert.equal(card.cardKey, "alice:T0001");
  assert.equal(card.taskShortID, "T0001");
  assert.equal(card.taskURL, "/tasks/T0001");
  assert.equal(card.taskTitle, "T");
});

test("buildActorColumns: ageText is computed against the cursor's nowSec, not wall clock", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  // Event 5 minutes before the cursor → '5m'.
  const events = [evt(1, "alice", "created", "T0001", 1700000000)];
  const card = buildActorColumns(events, frame, 1700000300)[0].cards[0];
  assert.equal(card.ageText, "5m");
});

test("buildActorColumns: pure-noted (no state-changer) does not create a card", () => {
  // A task that has only `noted` events for an actor doesn't surface
  // a card — there's no state to tint. Mirrors the server's
  // p.hasState gate.
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  const events = [evt(1, "alice", "noted", "T0001")];
  const cols = buildActorColumns(events, frame, 1700000099);
  // alice still appears as an actor (lastSeen tracking) but with 0 cards.
  assert.equal(cols.length, 1);
  assert.equal(cols[0].cards.length, 0);
});

test("buildActorColumns: claim band orders newest-first; history band orders newest-first", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "A", status: "claimed", sortKey: "000000" },
    { shortId: "T0002", title: "B", status: "claimed", sortKey: "000000" },
    { shortId: "T0003", title: "C", status: "available", sortKey: "000000" },
    { shortId: "T0004", title: "D", status: "available", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "alice", "claimed", "T0001", 1700000010),
    evt(2, "alice", "claimed", "T0002", 1700000020), // newer claim
    evt(3, "alice", "done", "T0003", 1700000030), // older history
    evt(4, "alice", "done", "T0004", 1700000040), // newer history
  ];
  const cards = buildActorColumns(events, frame, 1700000099)[0].cards;
  // Claim band first (newest claim first), then history band (newest first).
  assert.deepStrictEqual(
    cards.map((c) => c.taskShortID),
    ["T0002", "T0001", "T0004", "T0003"],
  );
  // Claim band cards have isClaim true; history don't.
  assert.deepStrictEqual(
    cards.map((c) => c.isClaim),
    [true, true, false, false],
  );
});

test("buildActorColumns: per-column cap retains all claims; history truncated to fill remainder", () => {
  // 2 claims + (LIMIT) history → keep all claims, history truncated
  // so total === LIMIT.
  const tasks = [];
  const events = [];
  for (let i = 1; i <= 2; i++) {
    const id = "C" + String(i).padStart(4, "0");
    tasks.push({ shortId: id, title: "claim " + i, status: "claimed", sortKey: "000000" });
    events.push(evt(events.length + 1, "alice", "claimed", id, 1700000000 + i));
  }
  // Add LIMIT+5 history-band cards so truncation is visible.
  for (let i = 1; i <= COLUMN_CARD_LIMIT + 5; i++) {
    const id = "H" + String(i).padStart(4, "0");
    tasks.push({ shortId: id, title: "hist " + i, status: "done", sortKey: "000000" });
    events.push(evt(events.length + 1, "alice", "done", id, 1700001000 + i));
  }
  const frame = frameWithTasks(tasks);
  const col = buildActorColumns(events, frame, 1700099999)[0];
  assert.equal(col.cards.length, COLUMN_CARD_LIMIT);
  assert.equal(col.cards.filter((c) => c.isClaim).length, 2);
});

test("buildActorColumns: empty actor names are skipped (matches the SQL filter)", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "T", status: "available", sortKey: "000000" },
  ]);
  const events = [
    evt(1, "", "created", "T0001"),
    evt(2, "alice", "created", "T0001"),
  ];
  const cols = buildActorColumns(events, frame, 1700000099);
  assert.deepStrictEqual(
    cols.map((c) => c.name),
    ["alice"],
  );
});

// --- range cutoff (?range=) --------------------------------------------

test("buildActorColumns: cutoff drops actors whose only events predate it", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Fresh", description: "", status: "available", sortKey: "000000" },
    { shortId: "T0002", title: "Stale", description: "", status: "available", sortKey: "000001" },
  ]);
  const now = 1700000000;
  const day = 86400;
  const events = [
    evt(1, "stale", "created", "T0002", now - 10 * day),
    evt(2, "fresh", "created", "T0001", now - 1 * day),
  ];

  const cutoff = now - 7 * day;
  const cols = buildActorColumns(events, frame, now, cutoff);
  assert.equal(cols.length, 1);
  assert.equal(cols[0].name, "fresh");
});

test("buildActorColumns: cutoff trims out-of-range cards from a surviving column", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Recent", description: "", status: "available", sortKey: "000000" },
    { shortId: "T0002", title: "Ancient", description: "", status: "available", sortKey: "000001" },
  ]);
  const now = 1700000000;
  const day = 86400;
  const events = [
    evt(1, "alice", "created", "T0002", now - 20 * day),
    evt(2, "alice", "created", "T0001", now - 2 * day),
  ];

  const cols = buildActorColumns(events, frame, now, now - 7 * day);
  assert.equal(cols.length, 1);
  assert.equal(cols[0].cards.length, 1);
  assert.equal(cols[0].cards[0].taskShortID, "T0001");
});

test("buildActorColumns: cutoff 0 (or omitted) keeps every event — the 'all' range", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Fresh", description: "", status: "available", sortKey: "000000" },
    { shortId: "T0002", title: "Stale", description: "", status: "available", sortKey: "000001" },
  ]);
  const now = 1700000000;
  const day = 86400;
  const events = [
    evt(1, "stale", "created", "T0002", now - 90 * day),
    evt(2, "fresh", "created", "T0001", now - 1 * day),
  ];

  assert.equal(buildActorColumns(events, frame, now, 0).length, 2);
  assert.equal(buildActorColumns(events, frame, now).length, 2);
});

test("buildActorColumns: an event exactly on the cutoff second is inside the window", () => {
  const frame = frameWithTasks([
    { shortId: "T0001", title: "Edge", description: "", status: "available", sortKey: "000000" },
  ]);
  const now = 1700000000;
  const cutoff = now - 7 * 86400;
  const events = [evt(1, "alice", "created", "T0001", cutoff)];

  assert.equal(buildActorColumns(events, frame, now, cutoff).length, 1);
});
