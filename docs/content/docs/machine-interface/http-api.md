---
title: /events HTTP API
weight: 2
---

`job serve` exposes one machine-facing endpoint: `GET /events`. It speaks two protocols, chosen by the request's `Accept` header:

- `Accept: text/event-stream` → **Server-Sent Events** live tail, with backfill spliced in.
- Anything else (or no header) → **JSON replay**: a single array of recent events.

Both share the same query params, the same wire shape, and the same filter semantics.

The endpoint is read-only. Writes happen through the CLI or the SQLite store; the HTTP surface never accepts a `POST`.

## Endpoint summary

```
GET /events?since=<id>&limit=<n>&actor=<name>&task=<short-id>&label=<name>&type=<event-type>
```

| Param   | Default        | Meaning                                                                |
|---------|----------------|------------------------------------------------------------------------|
| `since` | `0`            | Only return events with `id > since`. Use the last event's `id` to resume. |
| `limit` | `500`          | JSON replay only. Cap on returned events. SSE is unbounded.            |
| `actor` | `""` (any)     | Only events whose actor matches.                                       |
| `task`  | `""` (any)     | Only events whose target task has this short id.                       |
| `label` | `""` (any)     | Only events on tasks carrying this label.                              |
| `type`  | `""` (any)     | Only events of this event type (`created`, `claimed`, `done`, `noted`, `labeled`, `heartbeat`, …). |

Empty filters match everything. Filters compose via AND.

## The wire shape

The same object is used for SSE `data:` payloads and for entries in the JSON replay array:

```json
{
  "id": 7,
  "task_id": "bBE83",
  "task_title": "Child B",
  "event_type": "done",
  "actor": "test",
  "detail": "{\"cascade\":false,\"note\":\"done\"}",
  "created_at": "2026-05-07T18:31:49Z"
}
```

Field notes:

- **`id`** is the monotonically increasing event-log id. Use it for `since=` to resume cleanly across reconnects.
- **`task_id`** is the public **short id** (the same five-character handle the CLI uses). Internal numeric ids are not exposed.
- **`task_title`** is denormalized onto every event so a thin client can render a row without a second lookup.
- **`detail`** is an **opaque JSON string** — its inner shape varies per `event_type` and matches the `detail` payload from `job log --format=json`. Parse it on demand.
- **`created_at`** is RFC3339 in UTC.

This is the same shape `job tail --format=json` emits; a consumer can read either source with one parser.

## The dashboard's initial frame

Every dashboard page embeds a head-frame snapshot as a JSON island so the
time-travel scrubber has a state to fold events onto without replaying the
whole log:

```html
<script type="application/json" id="initial-frame">{"headEventId":42,"tasks":[…],"blocks":[…],"claims":[…]}</script>
```

`headEventId` is the event id the snapshot is current as of — the same id
space as `/events`, so a client hydrates from the island and resumes with
`?since=<headEventId>`. Each entry in `tasks` carries `shortId`, `title`,
`description`, `status`, `parentShortId`, `sortOrder`, `labels`, `criteria`,
and two fields that describe the [issue](../../concepts/tree-kinds/) surface:

- **`kind`** — the tree kind, `"task"` or `"issue"`. Carried on **roots
  only** and absent on children, so a consumer can tell a root from a child
  of one without walking up the tree. (This differs from `job ls
  --format=json`, which omits the default `"task"` everywhere; the frame is
  read by one client that needs the root/child distinction.)
- **`foundIn`** — the short id of the task that [surfaced](../../concepts/found-in/)
  this one, absent when there is no edge. One source per task.

Both names match `job show --format=json`, so one parser reads either source.

The corresponding events fold into that frame:

| `event_type` | `detail` keys | Effect on the frame |
|--------------|---------------|---------------------|
| `kind_changed` | `from`, `to` | Sets the root's `kind` to `to`; reversing restores `from`. |
| `found_in_set` | `task_id`, `source_id`, `previous_source_id` (only when a different source was displaced) | Sets `foundIn` to `source_id`; reversing restores `previous_source_id`, or clears the edge when there was none. |
| `found_in_cleared` | `task_id`, `source_id` (the source that was cleared) | Clears `foundIn`; reversing restores `source_id`. |

## JSON replay

Default mode. Useful for cold-loading recent history or polling.

```sh
curl -s http://127.0.0.1:7823/events
```

Resume from a known id (e.g., the last one your client has seen):

```sh
curl -s 'http://127.0.0.1:7823/events?since=12345&limit=200'
```

Filter to one actor and one event type:

```sh
curl -s 'http://127.0.0.1:7823/events?actor=alice&type=done' \
  | jq '.[] | {id, task_id, task_title, at: .created_at}'
```

Last 10 closes today:

```sh
curl -s 'http://127.0.0.1:7823/events?type=done&limit=10' | jq .
```

The replay endpoint returns a single JSON array. Empty result is `[]`, never an error.

## SSE live tail

Set `Accept: text/event-stream` to switch modes. The server flushes headers immediately, splices in any backfill events newer than `since=`, and then streams live events as they happen.

```sh
curl -N -H 'Accept: text/event-stream' http://127.0.0.1:7823/events
```

`-N` disables curl's output buffering — without it, you won't see frames until the connection closes.

Resume a live tail across a disconnect:

```sh
curl -N -H 'Accept: text/event-stream' \
  'http://127.0.0.1:7823/events?since=12345'
```

The backfill ensures no event between `since` and the live cursor is dropped. Browsers using the `EventSource` API get this for free; the `Last-Event-ID` header is honored as `since`.

### Frame format

Each event is one SSE frame:

```
id: 7
event: done
data: {"id":7,"task_id":"bBE83","task_title":"Child B","event_type":"done","actor":"test","detail":"{\"cascade\":false}","created_at":"2026-05-07T18:31:49Z"}

```

(blank line terminates the frame). The SSE `id:` field is the event log id and matches `data.id`; the SSE `event:` field is the event type and matches `data.event_type`.

A small bash live-printer:

```sh
curl -sN -H 'Accept: text/event-stream' \
  'http://127.0.0.1:7823/events?type=done' \
  | awk -F'data: ' '/^data: / { print $2 }' \
  | jq -r '"\(.created_at) \(.actor)\tdone\t\(.task_title) (\(.task_id))"'
```

The same plumbing in JavaScript:

```js
const es = new EventSource('/events?type=done');
es.addEventListener('done', (ev) => {
  const e = JSON.parse(ev.data);
  console.log(e.created_at, e.actor, 'done', e.task_title, e.task_id);
});
```

## Reconnect and backoff

The SSE connection is long-lived. The server holds it open as long as the broadcaster has events to send and the client stays connected; either side closing terminates cleanly. A client should:

1. Track the highest `id` it has processed.
2. On reconnect, pass `?since=<that-id>`. The server's backfill restores everything missed.
3. Apply backoff between retries — `EventSource` does this natively (browsers retry every ~3 seconds); custom clients should add jitter.

The dashboard's own `live-region` web component implements this contract; if you're writing a third-party tool, that source is the reference implementation.

## Bind address and exposure

`job serve` listens on `127.0.0.1:7823` by default — loopback only. To make `/events` reachable from another machine, pass `--bind` explicitly:

```sh
job serve --bind 0.0.0.0:7823
```

There is no authentication on the endpoint. Treat it as you would any local-only tool: bind to `0.0.0.0` only behind a trusted network or reverse proxy, and prefer `127.0.0.1` for everything else.
