// Tests for internal/web/assets/js/log-row.mjs.
//
// Pure verb/metadata mapping + HTML emitter mirroring the "log-row"
// block in internal/web/templates/html/pages/log.html.tmpl. The
// cross-language parity check (Go template vs this emitter, same
// event) lives in internal/web/handlers/log_row_parity_test.go; these
// tests pin the mapping itself and walk KNOWN_EVENT_TYPES so a new
// server-side event type can't land without a client-side answer.

import { test } from "node:test";
import assert from "node:assert/strict";

import {
  KNOWN_EVENT_TYPES,
  SYSTEM_ACTOR,
  isSystemEvent,
  metadataFor,
  renderLogRow,
  verbFor,
} from "../assets/js/log-row.mjs";

// One representative detail payload per event type. Every entry in
// KNOWN_EVENT_TYPES must appear here or the walk below fails.
const DETAILS = {
  created: '{"title":"Root task"}',
  claimed: '{"duration":"30m"}',
  done: '{"note":"shipped it"}',
  blocked: '{"blocker_id":"AbC12"}',
  unblocked: '{"blocker_id":"AbC12"}',
  noted: '{"text":"a note body"}',
  criteria_added: '{"criteria":[{"label":"tests pass"},{"label":"docs updated"}]}',
  criterion_state: '{"label":"tests pass","state":"passed"}',
  found_in_set: '{"source_id":"QnB2g"}',
  found_in_cleared: '{"source_id":"QnB2g"}',
  kind_changed: '{"from":"task","to":"issue"}',
  released: "{}",
  canceled: '{"reason":"obsolete"}',
};

test("KNOWN_EVENT_TYPES matches the server's ordered filter-chip set", () => {
  assert.deepEqual(KNOWN_EVENT_TYPES, [
    "created", "claimed", "done", "blocked", "unblocked",
    "noted", "criteria_added", "criterion_state",
    "found_in_set", "found_in_cleared", "kind_changed",
    "released", "canceled",
  ]);
});

test("verbFor collapses multi-word event types to a single verb", () => {
  assert.equal(verbFor("criteria_added"), "criteria");
  assert.equal(verbFor("criterion_state"), "criterion");
  assert.equal(verbFor("found_in_set"), "found in");
  assert.equal(verbFor("found_in_cleared"), "found in");
  assert.equal(verbFor("kind_changed"), "kind");
  assert.equal(verbFor("claim_expired"), "expired");
});

test("verbFor passes unmapped event types through unchanged", () => {
  for (const t of ["created", "claimed", "done", "released", "canceled", "noted", "moved"]) {
    assert.equal(verbFor(t), t);
  }
});

test("isSystemEvent flags only claim_expired", () => {
  assert.equal(isSystemEvent("claim_expired"), true);
  assert.equal(SYSTEM_ACTOR, "Jobs");
  for (const t of KNOWN_EVENT_TYPES) assert.equal(isSystemEvent(t), false);
});

test("metadataFor: claimed surfaces the claim duration", () => {
  assert.deepEqual(metadataFor("claimed", DETAILS.claimed), { text: "30m", pillId: "", state: "", prefix: "" });
});

test("metadataFor: noted surfaces the full note body, untruncated", () => {
  const long = "x".repeat(300);
  assert.equal(metadataFor("noted", JSON.stringify({ text: long })).text, long);
});

test("metadataFor: done and canceled drop empty bodies", () => {
  assert.equal(metadataFor("done", '{"note":""}').text, "");
  assert.equal(metadataFor("canceled", '{"reason":""}').text, "");
  assert.equal(metadataFor("done", DETAILS.done).text, "shipped it");
  assert.equal(metadataFor("canceled", DETAILS.canceled).text, "obsolete");
});

test("metadataFor: labeled joins the label names", () => {
  assert.equal(metadataFor("labeled", '{"names":["web","bug"]}').text, "web, bug");
  assert.equal(metadataFor("labeled", '{"names":[]}').text, "");
});

test("metadataFor: criteria_added joins the criterion labels", () => {
  assert.equal(metadataFor("criteria_added", DETAILS.criteria_added).text, "tests pass, docs updated");
});

test("metadataFor: criterion_state carries label plus state", () => {
  assert.deepEqual(metadataFor("criterion_state", DETAILS.criterion_state), {
    text: "tests pass", pillId: "", state: "passed", prefix: "",
  });
});

test("metadataFor: blocked/unblocked yield a blocker pill", () => {
  assert.deepEqual(metadataFor("blocked", DETAILS.blocked), { text: "", pillId: "AbC12", state: "", prefix: "" });
  assert.deepEqual(metadataFor("unblocked", DETAILS.unblocked), { text: "", pillId: "AbC12", state: "", prefix: "" });
});

test("metadataFor: found_in_set yields a source pill, with a prefix on replacement", () => {
  assert.deepEqual(metadataFor("found_in_set", DETAILS.found_in_set), {
    text: "", pillId: "QnB2g", state: "", prefix: "",
  });
  assert.deepEqual(metadataFor("found_in_set", '{"source_id":"QnB2g","previous_source_id":"zzTop"}'), {
    text: "", pillId: "QnB2g", state: "", prefix: "replacing zzTop, now",
  });
  assert.deepEqual(metadataFor("found_in_set", '{"source_id":""}'), {
    text: "", pillId: "", state: "", prefix: "",
  });
});

test("metadataFor: found_in_cleared says what happened to the id", () => {
  assert.deepEqual(metadataFor("found_in_cleared", DETAILS.found_in_cleared), {
    text: "", pillId: "QnB2g", state: "", prefix: "cleared, was",
  });
});

test("metadataFor: kind_changed reads as a tree-kind transition", () => {
  assert.equal(metadataFor("kind_changed", DETAILS.kind_changed).text, "task-tree → issue-tree");
  assert.equal(metadataFor("kind_changed", '{"from":"task"}').text, "");
});

test("metadataFor: absent or malformed detail is an empty cell", () => {
  for (const bad of ["", null, undefined, "not json", "[]"]) {
    assert.deepEqual(metadataFor("noted", bad), { text: "", pillId: "", state: "", prefix: "" });
  }
});

test("metadataFor: unknown event types render an empty cell", () => {
  assert.deepEqual(metadataFor("teleported", '{"text":"whee"}'), { text: "", pillId: "", state: "", prefix: "" });
});

test("every KNOWN_EVENT_TYPE renders a verb, a meta cell and a peek link", () => {
  for (const t of KNOWN_EVENT_TYPES) {
    assert.ok(t in DETAILS, `no fixture detail for event type ${t}`);
    const html = renderLogRow(event({ event_type: t, detail: DETAILS[t] }));
    assert.match(html, new RegExp(`class="c-log-row c-log-row--${t}"`), t);
    assert.match(html, new RegExp(`c-log-row__verb--${t}`), t);
    assert.match(html, new RegExp(`data-event-meta="${t}"`), t);
    assert.match(html, /data-peek class="c-row-link"/, t);
  }
});

test("renderLogRow: metadata-bearing types are not empty cells", () => {
  const withPayload = ["claimed", "done", "blocked", "unblocked", "noted",
    "criteria_added", "criterion_state", "found_in_set", "found_in_cleared",
    "kind_changed", "canceled"];
  for (const t of withPayload) {
    const html = renderLogRow(event({ event_type: t, detail: DETAILS[t] }));
    assert.doesNotMatch(html, new RegExp(`data-event-meta="${t}"></span>`), `${t} rendered an empty metadata cell`);
  }
});

test("renderLogRow: pill metadata links to the referenced task", () => {
  const html = renderLogRow(event({ event_type: "found_in_cleared", detail: DETAILS.found_in_cleared }));
  assert.match(html, /cleared, was <a class="c-id-pill" href="\/tasks\/QnB2g">QnB2g<\/a>/);
});

test("renderLogRow: criterion_state paints the state on the cell", () => {
  const html = renderLogRow(event({ event_type: "criterion_state", detail: DETAILS.criterion_state }));
  assert.match(html, /data-event-meta="criterion_state" data-state="passed">tests pass<\/span>/);
});

test("renderLogRow: claim_expired renders as a system row, no actor link", () => {
  const html = renderLogRow(event({ event_type: "claim_expired", actor: "alice", detail: "{}" }));
  assert.match(html, /<span class="c-log-row__actor c-log-row__actor--system"><span>Jobs<\/span><\/span>/);
  assert.doesNotMatch(html, /href="\/actors\//);
  assert.match(html, />expired</);
});

test("renderLogRow: escapes hostile titles, actors and note bodies", () => {
  const html = renderLogRow(event({
    actor: '<img src=x>',
    task_title: '"><script>alert(1)</script>',
    event_type: "noted",
    detail: JSON.stringify({ text: "<b>bold</b>" }),
  }));
  assert.doesNotMatch(html, /<script>/);
  assert.doesNotMatch(html, /<img/);
  assert.doesNotMatch(html, /<b>bold<\/b>/);
  assert.match(html, /&lt;b&gt;bold&lt;\/b&gt;/);
});

test("renderLogRow: a class-injecting event type is sanitised", () => {
  const html = renderLogRow(event({ event_type: 'x" onload="boom' }));
  // The class token is stripped to a safe alphabet, and the raw type
  // survives only inside an attribute whose quotes are escaped — so it
  // can never break out and become an event handler.
  assert.match(html, /class="c-log-row c-log-row--xonloadboom"/);
  assert.doesNotMatch(html, / onload="/);
  assert.match(html, /data-event-meta="x&#34; onload=&#34;boom"/);
});

test("renderLogRow: relative time comes from created_at, not a hardcoded label", () => {
  const nowSec = Date.parse("2026-08-28T12:00:00Z") / 1000;
  const html = renderLogRow(
    event({ created_at: "2026-08-28T11:00:00Z" }),
    { nowSec },
  );
  assert.match(html, /<time class="c-log-row__time" datetime="2026-08-28T11:00:00Z">1h<\/time>/);
});

test("renderLogRow: an event at the current instant reads 'just now'", () => {
  const nowSec = Date.parse("2026-08-28T12:00:00Z") / 1000;
  const html = renderLogRow(event({ created_at: "2026-08-28T12:00:00Z" }), { nowSec });
  assert.match(html, />just now</);
});

function event(over = {}) {
  return {
    id: 42,
    task_id: "AbC12",
    task_title: "Some task",
    event_type: "created",
    actor: "alice",
    detail: "{}",
    created_at: "2026-08-28T12:00:00Z",
    ...over,
  };
}
