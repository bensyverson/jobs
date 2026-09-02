---
title: CLI JSON output
weight: 1
---

`--format=json` is supported on every read verb and on the writes whose output an agent is likely to want machine-parseable. The default is Markdown for humans; JSON is one flag away.

## Verbs that accept `--format=json`

| Verb              | Shape                                                                                  |
|-------------------|----------------------------------------------------------------------------------------|
| `ls`              | Array of task objects with nested `children`. Full closed history, no pagination cap. |
| `show`            | Array of task records (one per id). Includes `description`, `labels`, `notes`, criteria, claim info, `created_at`. |
| `log`             | Array of event objects with `id`, `task_id`, `short_id`, `event_type`, `actor`, `detail`, `created_at`. `detail` is a typed object whose shape varies per event type. |
| `next`            | Single task object (or array, with `next ... all`).                                   |
| `tail`            | One event object per line (newline-delimited, **not** a JSON array). See below.        |
| `import`          | Object with the new ids assigned by the importer.                                      |
| `claim`           | Single task object including `claimed_by` and `claim_expires_at`.                     |
| `claim --next`    | Same as `claim`, plus a `next` advisory when no leaf was available.                   |
| `done`            | Object with `closed`, `already_done`, `next` (advisory), and `parent` (rollup).       |
| `cancel`          | Same shape as `done` (with cancellations under `closed`).                              |
| `heartbeat`       | Object listing the refreshed claims and their new `expires_at`.                       |
| `label add`       | Object with the labels that were newly added vs. already present.                     |
| `status`          | Forest scope: `identity`, `counts`, `last_activity_unix`, `roots`, `next`, `focus`, `stale`, `decisions`, `issues` — `roots` lists task-tree roots only; `focus` is the actor's focused root (`{short_id, title}`, null when unset) and `next` resolves inside it when set; `issues` is `{open, claimed, next}` summarizing work under issue-tree roots (`next` shaped like the top-level `next`), null when the database has no issue-tree root. Subtree scope (`status <id> --format=json`): `target`, `children`, `next`, `stale`, `decisions` — the preamble is dropped, mirroring the Markdown form. |

## Read shapes by example

A small worked database — one root with two children, one of them claimed:

```sh
job ls --format=json
```

```json
[
  {
    "id": "jTzON",
    "title": "Root",
    "status": "available",
    "description": "",
    "children": [
      { "id": "gX5Uf", "title": "Child A", "status": "claimed", "description": "", "children": null },
      { "id": "bBE83", "title": "Child B", "status": "available", "description": "", "children": null }
    ]
  }
]
```

`ls --format=json` returns the **full closed history** — the 10-item Markdown footer cap doesn't apply.

A root marked as an [issue-tree](../../concepts/tree-kinds/) carries `"kind": "issue"`. The field is **absent** on everything else — `task` is the default, so an always-present field would change every existing payload for no information. `next`, `claim` and `show` emit it on the same terms.

`job show --format=json` is richer; it adds the things `ls` deliberately omits (description, labels, notes, criteria, claim metadata):

```json
[{
  "id": "jTzON",
  "title": "Root",
  "description": "Phase root.",
  "status": "available",
  "children": [...],
  "labels": ["docs", "p0"],
  "notes": [],
  "created_at": 1778197290
}]
```

## Write-verb JSON: the `done` ack

```sh
job done gX5Uf --format=json -m "ack"
```

```json
{
  "closed": [
    { "id": "gX5Uf", "title": "Child A", "cascade_closed": [] }
  ],
  "already_done": [],
  "next": { "id": "bBE83", "title": "Child B" },
  "parent": { "id": "jTzON", "done": 1, "total": 2 }
}
```

The fields worth knowing:

- **`closed`** — every task that transitioned in this call. `cascade_closed` lists descendants closed via `--cascade`.
- **`already_done`** — ids that were already closed; idempotent re-`done` lands here, not in `closed`.
- **`next`** — the planner's hint for the next leaf to claim. Same engine as `job next`. Use this to drive the close-and-advance loop without a follow-up call.
- **`parent`** — child rollup for the closed task's parent (`done` of `total`). When `done == total`, the parent auto-closed in the same transaction.

## The `tail` JSON-lines contract

`job tail --format=json` is **not** a JSON array. It writes one JSON object per line, terminated by `\n`, and never closes a wrapping bracket — so a parser can be a `readline` loop with `JSON.parse` per line. This is the same contract as `kubectl logs --tail=-1 --output=json` or `journalctl -o json`.

```sh
job tail --format=json
```

```json
{"id":3,"task_id":3,"short_id":"bBE83","event_type":"created","actor":"test","detail":{"parent_id":"jTzON","title":"Child B","description":"","sort_key":"V00001"},"created_at":1778197309}
{"id":7,"task_id":3,"short_id":"bBE83","event_type":"done","actor":"test","detail":{"cascade":false,"note":"done","was_status":"available"},"created_at":1778197309}
```

Two things to keep in mind:

- **Control frames.** When `--until-close <id>` fires, the stream emits a control object `{"closed": "<id>", "event": "done"}` (or `"canceled"`) **before** exiting. A consumer should accept either an event object (with `id`/`event_type`/...) or a control object on the same channel.
- **Default event filter.** Heartbeats are excluded by default — they would otherwise flood any non-trivial tail. Re-include them with `--events done,heartbeat,...`.

## A jq cookbook

Once everything is line-delimited JSON, `jq` is the right hammer.

Live count of `done` events in the current session:

```sh
job tail --format=json --events done | jq -c '{id, short_id, actor, at: .created_at}'
```

Identities ranked by close count today:

```sh
job log --format=json --since 1d \
  | jq -r '.[] | select(.event_type=="done") | .actor' \
  | sort | uniq -c | sort -rn
```

Every available leaf, formatted as `id  title`:

```sh
job next all --format=json | jq -r '.[] | "\(.id)\t\(.title)"'
```

Top-level rollup of open tasks per root:

```sh
job ls --format=json \
  | jq -r '.[] | "\(.id)\t\([.. | objects | select(.status? | test("available|claimed"))] | length)\t\(.title)"'
```

The pattern across every example is the same: ask for JSON, parse it, do the work in a real language. Markdown is for humans and `less`; JSON is for everything else.
