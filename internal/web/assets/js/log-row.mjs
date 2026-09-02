/*
  Log-row HTML emitter — the client mirror of the "log-row" block in
  internal/web/templates/html/partials/log_row.html.tmpl.

  A row on /log and on /actors/{name} is rendered twice: server-side on page load, and here
  when the same event arrives over SSE. The two must agree, or a live
  row reads differently from the row it replaces on the next reload
  (task vz1tg: live found_in_set rows showed FOUND_IN_SET and no
  payload). The mapping — verb text, system-actor handling, and the
  trailing metadata cell — is pure and exported so it can be tested on
  its own; internal/web/handlers/log_row_parity_test.go renders the
  same event through the Go template and through renderLogRow and
  diffs the normalised markup.

  Input is the SSE frame shape (handlers.eventJSON): id, task_id,
  task_title, event_type, actor, detail (a JSON *string*), created_at
  (RFC3339), and replica_label — the name of the machine the event was
  written on, sent only for a foreign replica.
*/

import { escapeHTML, relativeTime } from "./scrub-util.mjs";

// KNOWN_EVENT_TYPES mirrors handlers.knownEventTypes — the canonical
// ordered set of event types the filter bar offers. Duplicated here so
// the JS tests can walk the server's vocabulary; a Go test reads this
// array back out of the file and fails if the two lists drift.
export const KNOWN_EVENT_TYPES = [
  "created", "claimed", "done", "blocked", "unblocked",
  "noted", "criteria_added", "criterion_state",
  "found_in_set", "found_in_cleared", "kind_changed",
  "released", "canceled",
];

// SYSTEM_ACTOR is the name shown as the doer of housekeeping events —
// the Jobs runtime, not whoever held the claim.
export const SYSTEM_ACTOR = "Jobs";

// isSystemEvent mirrors handlers.isSystemEventType.
export function isSystemEvent(eventType) {
  return eventType === "claim_expired";
}

// verbFor mirrors handlers.logRowVerb: multi-word event types collapse
// to a single verb so the verb column stays the width of DONE /
// CLAIMED, with the detail folded into the metadata cell. Unmapped
// types render their raw event type (the CSS uppercases it).
export function verbFor(eventType) {
  switch (eventType) {
    case "claim_expired":
      return "expired";
    case "criteria_added":
      return "criteria";
    case "criterion_state":
      return "criterion";
    case "found_in_set":
    case "found_in_cleared":
      return "found in";
    case "kind_changed":
      return "kind";
    default:
      return eventType || "";
  }
}

const EMPTY_META = Object.freeze({ text: "", pillId: "", state: "", prefix: "" });

function meta(over) {
  return { ...EMPTY_META, ...over };
}

// metadataFor mirrors handlers.buildLogRowMetadata: one summary value
// per event type, pulled from the event's detail JSON. `detail` may be
// the raw JSON string that arrives on the SSE frame or an already-
// parsed object. Absent, malformed or non-object detail — and any
// event type without a payload worth showing — yields an empty cell.
export function metadataFor(eventType, detail) {
  const d = parseDetail(detail);
  if (!d) return meta();

  switch (eventType) {
    case "claimed":
      // The CLI string ("30m", "2h", …) is recorded on the event, so
      // we surface what the actor chose at claim time.
      return typeof d.duration === "string" ? meta({ text: d.duration }) : meta();
    case "noted":
      return typeof d.text === "string" ? meta({ text: d.text }) : meta();
    case "done":
      return typeof d.note === "string" && d.note !== "" ? meta({ text: d.note }) : meta();
    case "canceled":
      return typeof d.reason === "string" && d.reason !== "" ? meta({ text: d.reason }) : meta();
    case "labeled": {
      if (!Array.isArray(d.names)) return meta();
      const names = d.names.filter((n) => typeof n === "string" && n !== "");
      return names.length > 0 ? meta({ text: names.join(", ") }) : meta();
    }
    case "criteria_added": {
      if (!Array.isArray(d.criteria)) return meta();
      const labels = [];
      for (const c of d.criteria) {
        if (c && typeof c === "object" && typeof c.label === "string" && c.label !== "") {
          labels.push(c.label);
        }
      }
      return labels.length > 0 ? meta({ text: labels.join(", ") }) : meta();
    }
    case "criterion_state":
      return meta({
        text: typeof d.label === "string" ? d.label : "",
        state: typeof d.state === "string" ? d.state : "",
      });
    case "blocked":
    case "unblocked":
      return typeof d.blocker_id === "string" && d.blocker_id !== ""
        ? meta({ pillId: d.blocker_id })
        : meta();
    case "found_in_set": {
      // The source id is the whole payload; a replacement also records
      // the displaced source. The pill is the link, so the row shows
      // the current source and the prefix says it replaced one.
      const id = typeof d.source_id === "string" ? d.source_id : "";
      if (id === "") return meta();
      const prev = typeof d.previous_source_id === "string" ? d.previous_source_id : "";
      return prev !== ""
        ? meta({ pillId: id, prefix: `replacing ${prev}, now` })
        : meta({ pillId: id });
    }
    case "found_in_cleared":
      return typeof d.source_id === "string" && d.source_id !== ""
        ? meta({ pillId: d.source_id, prefix: "cleared, was" })
        : meta();
    case "kind_changed": {
      // Mirrors `job log`: "kind task-tree → issue-tree".
      const from = typeof d.from === "string" ? d.from : "";
      const to = typeof d.to === "string" ? d.to : "";
      return from !== "" && to !== "" ? meta({ text: `${from}-tree → ${to}-tree` }) : meta();
    }
    default:
      return meta();
  }
}

function parseDetail(detail) {
  if (detail == null || detail === "") return null;
  let parsed = detail;
  if (typeof detail === "string") {
    try {
      parsed = JSON.parse(detail);
    } catch (_) {
      return null;
    }
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  return parsed;
}

// renderLogRow returns the markup for one row, byte-for-byte equal to
// the server's "log-row" block once inter-tag whitespace is collapsed.
// The row is the same on every view that shows it: a page where a cell
// is redundant (the actor cell on /actors/{name}) hides it from the
// list's own modifier, so nothing here varies by surface.
// Options:
//   nowSec  — the instant the relative time is measured from
//             (defaults to the wall clock), in unix seconds.
export function renderLogRow(ev, { nowSec = Date.now() / 1000 } = {}) {
  const type = ev.event_type || "";
  const typeClass = safeClass(type);
  const shortID = ev.task_id || "";
  const system = isSystemEvent(type);
  const actor = system ? SYSTEM_ACTOR : ev.actor || "";
  const iso = ev.created_at || "";
  const thenSec = iso ? Date.parse(iso) / 1000 : nowSec;

  return (
    `<div class="c-log-row c-log-row--${typeClass}" role="listitem" data-event-id="${escapeHTML(ev.id)}" data-event-position="${escapeHTML(ev.position)}">` +
    `<time class="c-log-row__time" datetime="${escapeHTML(iso)}">${escapeHTML(relativeTime(nowSec, thenSec))}</time>` +
    renderActor(actor, system) +
    `<span class="c-log-row__verb c-log-row__verb--${typeClass}">${escapeHTML(verbFor(type))}</span>` +
    `<span class="c-id-pill">${escapeHTML(shortID)}</span>` +
    `<span class="c-log-row__detail">` +
    (ev.task_title ? `<span class="c-log-row__title">${escapeHTML(ev.task_title)}</span>` : "") +
    (ev.replica_label ? `<span class="c-log-row__replica">${escapeHTML(ev.replica_label)}</span>` : "") +
    `</span>` +
    renderMeta(type, metadataFor(type, ev.detail)) +
    `<a href="/tasks/${escapeHTML(shortID)}" data-peek class="c-row-link" aria-label="Open task ${escapeHTML(shortID)}"></a>` +
    `</div>`
  );
}

function renderActor(actor, system) {
  if (system) {
    return `<span class="c-log-row__actor c-log-row__actor--system"><span>${escapeHTML(actor)}</span></span>`;
  }
  return (
    `<a href="/actors/${escapeHTML(encodeURIComponent(actor))}" class="c-log-row__actor" data-actor="${escapeHTML(actor)}">` +
    `<span class="c-avatar c-avatar-sm" data-actor="${escapeHTML(actor)}"></span>` +
    `<span>${escapeHTML(actor)}</span>` +
    `</a>`
  );
}

function renderMeta(type, m) {
  const t = escapeHTML(type);
  if (m.pillId) {
    const prefix = m.prefix ? `${escapeHTML(m.prefix)} ` : "";
    return (
      `<span class="c-log-row__meta" data-event-meta="${t}">${prefix}` +
      `<a class="c-id-pill" href="/tasks/${escapeHTML(m.pillId)}">${escapeHTML(m.pillId)}</a></span>`
    );
  }
  if (m.state) {
    return `<span class="c-log-row__meta" data-event-meta="${t}" data-state="${escapeHTML(m.state)}">${escapeHTML(m.text)}</span>`;
  }
  return `<span class="c-log-row__meta" data-event-meta="${t}">${escapeHTML(m.text)}</span>`;
}

// safeClass sanitizes an event type before it is inlined into a class
// token. The server only ever produces lowercase ASCII here, but a
// malformed frame must never become a class-injection vector.
function safeClass(s) {
  return String(s == null ? "" : s).replace(/[^a-zA-Z0-9_-]/g, "");
}
