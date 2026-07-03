---
title: The event log
weight: 6
---

Jobs is an **event-sourced** system. Every state change is recorded as an immutable event in an append-only log; the current shape of the world is a projection of those events. The `tasks`, `claims`, `blockers`, and other tables are caches — useful for queries, but the events are the truth.

## Why this matters

- **Replayable history.** `job log <id>` shows the full transcript of a task, including who did what and when. Nothing is lost.
- **Multi-agent attribution.** Every event records its actor. Coordination is auditable after the fact.
- **No destructive edits.** `edit` writes a new event; the previous title and description are still in the log. `cancel` records intent rather than deleting (though `cancel --purge` is available when you really want erasure).
- **Live observability.** The web dashboard, `job tail`, and the `/events` API all consume the same event stream — there's no separate notification pipeline.

## Event shape

Every event has at minimum:

- `id` — monotonic integer, unique across the database.
- `task_id` — internal numeric task id.
- `short_id` — the 5-char public id of the task it concerns.
- `event_type` — see the catalogue below.
- `actor` — the writer's identity at the time.
- `created_at` — Unix epoch seconds in `--format=json`; rendered as local time `[YYYY-MM-DD HH:MM]` in plain output.
- `detail` — opaque JSON whose schema varies per `event_type`. Common keys include `note` (done / cancel body), `text` (noted body), and structural metadata for blocker / move events.

Treat `detail`'s unknown keys as forward-compatible.

### Event types

State changes:

- `created` — task added.
- `claimed` — claim acquired.
- `released` — claim relinquished or auto-released when an open child was added.
- `claim_expired` — claim TTL elapsed without renewal.
- `done` — task closed. Auto-close cascades use `done` with `auto_closed: true` and a `triggered_by: <child-id>` detail.
- `canceled` — task canceled. Cancel-triggered auto-close cascades likewise use `canceled` with `auto_closed: true`.
- `reopened` — closed or canceled task returned to `available`.
- `purged` — task and its events erased via `cancel --purge` (the surviving event lives on the parent's log).

Structural changes:

- `edited` — title or description rewritten.
- `noted` — free-text note recorded; no state change.
- `moved` — sibling order changed.
- `reparented` — task moved under a different parent.
- `labeled` / `unlabeled` — label added or removed.
- `blocked` / `unblocked` — blocker edge added or removed. Auto-removal on blocker close writes `unblocked` with `reason: blocker_done`.

Criteria:

- `criteria_added` — criterion list authored on a task.
- `criterion_state` — single criterion transitioned (`pending` → `passed` / `skipped` / `failed`).

Focus:

- `focus_set` — the actor's [focus](../../reference/execution/#focus) moved to a root, almost always as a side effect of claiming outside it (`detail.via = "claim"` names the claimed task). Same-root claims emit nothing.
- `focus_released` — the actor's focus cleared: manually (`job focus --clear`) or automatically when the focused root closed (`detail.via = "root_closed"`, one event per focused actor).

Liveness:

- `heartbeat` — explicit `job heartbeat` call. Hidden by default in `tail` output; pass `--events heartbeat` to see them.

## Reading the log

```sh
job log <id>            # transcript for a task and its descendants
job log all             # every top-level tree (the whole DB)
job log <id> --since 2026-04-01T00:00:00Z
job log <id> --format=json
```

Without `--format=json`, the output is a human-readable transcript. With it, you get a pretty-printed JSON array suitable for `jq`. Same data either way.

## Tailing the stream

```sh
job tail <id>                       # poll every second, follow until Ctrl-C
job tail all --format=json          # JSON-lines, one event per line
job tail <id> --events done,noted   # display filter
job tail <id> --users alice,bob     # actor filter
```

`tail`'s JSON-lines mode is the canonical way for an outside process to subscribe to event changes — it composes cleanly with `jq -c`, `xargs`, or any line-oriented pipeline.

By default, `heartbeat` events are hidden in tail output (they're noisy). Pass `--events heartbeat` if you want to see them.

## Synchronous waits

`tail --until-close` blocks until the watched task(s) reach `done` or `canceled`:

```sh
job tail <id> --until-close --timeout 5m --quiet
```

The `--events` / `--users` display filters are orthogonal to `--until-close`: filters hide events from the stream, but terminal `done` / `canceled` events always trigger exit. Exit code 0 on a clean close, 2 on `--timeout` expiry, 1 on any other error.

## The `/events` HTTP API

The same event stream is exposed as an HTTP endpoint when `job serve` is running. JSON replay or SSE live-tail, with `since`/`actor`/`task`/`label`/`type` query parameters. See [Machine interface — HTTP API](../../machine-interface/http-api/) for the wire details.

## What you don't pay for

There's no separate audit table, no notification queue, no eventual-consistency window. The same SQLite write that flips a task to `done` produces the `done` event in the same transaction. Every reader — `log`, `tail`, the dashboard, the HTTP API — is reading from the same authoritative table.

Append-only event logs are usually a sophisticated architectural choice. Here it's load-bearing for some of Jobs' simplest features (replay, attribution, live tail) — pay attention to the log, and the rest of the system explains itself.
