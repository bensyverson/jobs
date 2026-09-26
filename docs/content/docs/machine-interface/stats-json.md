---
title: job stats JSON
weight: 3
---

`job stats --format=json` emits the whole [stats report](../../reference/observation/#stats) as one JSON document: the resolved window, the headline figures, and the full burn-up series. It is the contract for tools that chart or aggregate Jobs data outside it — read it with `job stats`, not by querying `.jobs.db`, so the [counting rules](../../reference/observation/#what-the-numbers-count) are applied once, in the core, and never re-derived.

## The `schema` version

Every document starts with `"schema": 1`, the version of this shape.

- **Adding a field does not change it.** Ignore fields you don't know.
- **Renaming a field, removing one, or changing what one means bumps it.**
- **Refuse a version you don't know.** A consumer that reads `schema: 2` as if it were `1` would report numbers under the wrong meaning without noticing; failing loudly is the correct behavior.

The [JSON Schema](#json-schema) below is the machine-checkable definition; `job schema stats` prints the same document from the binary you have installed.

## An example

A scratch store with one imported plan of four leaves, one of them blocked by another, and a fifth leaf added mid-plan behind a blocker. `bob` closed one leaf, `alice` reopened it and closed it again, one leaf was canceled, and the rest were closed, which closed the plan. Captured with explicit bounds, so the window is exactly two one-minute buckets:

```sh
job stats --format=json --since 2026-09-26T18:49:00-05:00 --until 2026-09-26T18:51:00-05:00
```

```json
{
  "schema": 1,
  "window": {
    "since": "2026-09-26T18:49:00-05:00",
    "until": "2026-09-26T18:51:00-05:00",
    "bucket": "minute",
    "timezone": "America/Chicago"
  },
  "leaves": {
    "created": 5,
    "done": 4,
    "canceled": 1,
    "open": 0,
    "blocked": 0
  },
  "plans": {
    "imported": 1,
    "first_import_at": "2026-09-26T18:49:13.641-05:00",
    "closed": 1,
    "open": 0,
    "median_import_to_close_seconds": 98
  },
  "pace": {
    "done_per_week": 20160,
    "median_created_to_done_seconds": 40,
    "median_claimed_to_done_seconds": 0
  },
  "done_by_actor": [
    {
      "actor": "alice",
      "done": 2
    },
    {
      "actor": "bob",
      "done": 2
    }
  ],
  "series": [
    {
      "end": "2026-09-26T18:50:00-05:00",
      "scope": 4,
      "done": 2,
      "open": 2,
      "blocked": 0,
      "canceled": 1,
      "plans_done": 0
    },
    {
      "end": "2026-09-26T18:51:00-05:00",
      "scope": 4,
      "done": 4,
      "open": 0,
      "blocked": 0,
      "canceled": 1,
      "plans_done": 1
    }
  ],
  "activity": [
    {
      "start": "2026-09-26T18:49:00-05:00",
      "end": "2026-09-26T18:50:00-05:00",
      "created": 6,
      "claimed": 4,
      "done": 3,
      "blocked": 2
    },
    {
      "start": "2026-09-26T18:50:00-05:00",
      "end": "2026-09-26T18:51:00-05:00",
      "created": 0,
      "claimed": 1,
      "done": 3,
      "blocked": 0
    }
  ],
  "imports": [
    {
      "at": "2026-09-26T18:49:13.641-05:00",
      "task_id": "grakKQ",
      "title": "Checkout flow",
      "source": "checkout.md"
    }
  ]
}
```

Worth reading closely:

- **Five leaves created, scope four.** The plan's root is a parent, so it never counts; the canceled leaf left scope and is in `canceled`.
- **Four done, not five.** The reopened leaf was closed twice, but it is one leaf, counted once at its last close. The activity histogram, which counts *events*, shows every `done` — including the parent's automatic close — which is why `activity` sums to six.
- **`done_by_actor` is two each.** `bob` closed the reopened leaf first, but `alice` ran the close that stuck.
- **`done_per_week` is 20,160** because the window is two minutes long: four leaves per two minutes, extrapolated to a week.

## Fields

Times are RFC3339 with the offset of `window.timezone`, with a fractional second whenever the instant has one. Counts are non-negative integers and durations are whole seconds. Lists are `[]` when empty, never `null`.

**`window`** — what the report covers, after defaults resolved.

| Field | Meaning |
|-------|---------|
| `scope` | Short id of the subtree the report covers. **Absent** for the whole forest. |
| `since` | Window start; the first event in scope when `--since` was not given. |
| `until` | Window end; the moment the report was built when `--until` was not given. |
| `bucket` | Width of one sample: `minute`, `hour`, `6h`, `day` or `week`. |
| `timezone` | IANA zone the buckets align to. Days start at local midnight, weeks on Monday. |

**`leaves`** — `created`, `done` and `canceled` are transitions inside the window, each leaf counted once by where it stands at `until`; `open` and `blocked` are the state at `until`.

**`plans`** — plans are non-issue roots (with a `scope`, the scoped task is the only plan).

| Field | Meaning |
|-------|---------|
| `imported` | `imported` events in the window. |
| `closed` | Plans closed in the window. |
| `open` | Plans open at `until`. |
| `median_import_to_close_seconds` | Median over imported tasks closed in the window; `null` when there are none. |
| `first_import_at` | The earliest `imported` event in scope up to `until`, inside the window or before it; `null` when there is none. It tells "no imports ever recorded" from "none in this window". |

Import figures come from [`imported` events](../../concepts/events/#event-types), which began on 2026-09-26 with no backfill. A store with older imports undercounts rather than guessing, and `first_import_at: null` is how to tell.

**`pace`** — over leaves closed in the window. `done_per_week` is done ÷ the window's length in weeks. `median_created_to_done_seconds` and `median_claimed_to_done_seconds` are `null` when no leaf qualifies; claimed→done runs from the last claim before the final close and leaves out leaves closed without a claim.

**`done_by_actor`** — `[{actor, done}]`, leaves closed in the window by the identity that ran the final `done`, most first, ties by name. It measures identity hygiene as much as who did the work.

**`series`** — one sample per bucket, oldest first; the last sample's `end` is `window.until`. Each is the store's state as of `end`:

| Field | Meaning |
|-------|---------|
| `end` | The instant this sample is the state as of. |
| `scope` | Leaves that exist and are not canceled — the burn-up's upper line. |
| `done` | Leaves done — the lower line. It dips when a leaf is reopened. |
| `open` | `scope` minus `done`. |
| `blocked` | The part of `open` with a blocker that is not done. |
| `canceled` | Leaves canceled as of `end`; they have left scope. |
| `plans_done` | Plans in the done state as of `end`. |

**`activity`** — `[{start, end, created, claimed, done, blocked}]`, one per bucket, aligned with `series`: the number of events of each type on any task in scope in `[start, end)`, parents included. These are events, not leaves.

**`imports`** — `[{at, task_id, title, source}]`, each `imported` event in the window, oldest first: the top-level task the import created, its title now, and the base name of the plan file.

## CSV

`job stats --format=csv` is the series in long form: a header row, then one row per bucket, oldest first, with that bucket's activity joined on:

```text
end,scope,done,open,blocked,canceled,plans_done,activity_created,activity_claimed,activity_done,activity_blocked
2026-09-26T18:50:00-05:00,4,2,2,0,1,0,6,4,3,2
2026-09-26T18:51:00-05:00,4,4,0,0,1,1,0,1,3,0
```

The activity columns carry an `activity_` prefix because `done` and `blocked` already name the sample's state. `end` is RFC3339 to the second, in the report's zone. The headline figures are not in the CSV; use JSON for them.

## JSON Schema

> Generated from `job schema stats` — do not edit. Run `make docs-schema` to refresh after any change to the report's shape; a test fails while this copy is stale.

{{< include-schema "content/docs/machine-interface/_stats_schema.json" >}}
