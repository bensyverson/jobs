---
title: The store
weight: 8
---

Jobs keeps two things on disk, and only one of them is the record.

```text
.jobs/
  log/
    785RLT.jsonl      the record — one file per replica, append-only, tracked
    unFNeC.jsonl
  local.json          this machine's own state — ignored
.jobs.db              the cache — ignored, disposable
```

**`.jobs/log/*.jsonl` is the record.** One JSON object per line, one line per event, appended and never rewritten. It is text, so git can diff it, merge it and carry it between machines.

**`.jobs.db` is a cache.** It is a SQLite projection of the log, rebuilt from it whenever the log has moved. Every read verb and the whole dashboard query it, because SQL over a tree is what SQLite is good at. Nothing else depends on it: delete it and the next `job` command rebuilds it, identity and all.

The design and the reasoning behind every rule on this page are in [`project/2026-09-01-git-native-event-log.md`](https://github.com/bensyverson/jobs/blob/main/project/2026-09-01-git-native-event-log.md).

## What to commit

Commit **`.jobs/log/`**. Everything else Jobs writes is local:

| Path | Commit? | What it is |
|---|---|---|
| `.jobs/log/*.jsonl` | **yes** | The record. One file per replica. |
| `.jobs.db` | no | The cache. Rebuilt on demand. |
| `.jobs.db-wal`, `.jobs.db-shm` | no | SQLite's write-ahead sidecars. |
| `.jobs.db.lock` | no | The store lock, taken around append-then-apply. |
| `.jobs.db.pre-adopt` | no | The backup adoption keeps (see below). |
| `.jobs/local.json` | no | This machine's replica id, clock, default identity, strict flag and focus. |

`job gitignore` writes the two patterns that cover all of it:

```text
# Jobs cache and its sidecars — disposable, rebuilt from .jobs/log
.jobs.db*
# This machine's replica id, identity and focus
.jobs/local.json
```

The `*` is doing the work — one pattern for the cache and everything that sits beside it under the same name.

## Replicas

**A replica is one checkout on one machine.** Two clones of the same repo are two replicas; so is a git worktree, since it has its own working directory. A replica's id is six base62 characters, minted the first time a store is opened there and kept in `local.json`.

The id decides one thing: which file this checkout appends to. Nobody ever writes another replica's file, so two machines can work at the same time and git never sees a conflict in the log.

Losing `local.json` costs nothing but the id — the next command mints a fresh one and starts a new file. The old file stays in the log and is still part of the record.

`local.json` is per machine on purpose. The default identity, strict mode and your [focus](../../reference/execution/#focus) are facts about *this checkout*, not about the project: the same repo cloned on a colleague's laptop should not inherit your identity. Keeping them out of the cache also means `rm .jobs.db` cannot lose them.

## Rebuilds

The cache records a **watermark** for each log file: the byte offset it has applied. Every `job` command checks it before doing anything else.

- Every file's size equals its watermark and there is no unknown file: nothing to do. This is the hot path, and it costs one `stat` per file.
- Anything else — a file grew, a new file appeared, the cache is missing: **rebuild**. Drop every table, sort the union of every log file, apply in order, record the new watermarks.

`job status` says which of the two happened:

```text
Store: replica 785RLT · 1 log file, 3 events · cache in sync
Store: replica 785RLT · 2 log files, 5 events · cache rebuilt on open
```

Two other states can appear there. `log incomplete` means the cache holds events for a replica whose log file is missing and cannot be written back — replaying would lose them, so nothing is rebuilt and the cache is left as it is. `predates the store` means the database has not been adopted yet (see below).

`job rebuild` forces a rebuild. It is the recovery verb — after a crash, or when you suspect the cache — and it cannot lose anything, because the cache holds nothing the log does not.

There is no `job sync`. Sync is `git pull`; the next command notices.

## How two machines cooperate

Each machine appends to its own file. Git carries the files. Replay merges them.

Every replica sorts the union of every log file by **`(ts, rep, seq)`** — a timestamp, the replica id, and that replica's gapless sequence number — and applies the result in that order. Every machine therefore builds the same tables from the same files. That is the whole merge algorithm; there is no merge driver, no CRDT and no server.

`ts` is a hybrid logical clock: `max(wall clock, the highest timestamp this replica has seen + 1)`. It keeps cause sorting before effect even when two machines' clocks disagree.

Most concurrent work simply composes — one machine notes a task while another closes it, and both land. Where two edits genuinely collide, the rules are:

| Two machines both… | What replay does |
|---|---|
| edit the same field | the later edit wins, field by field |
| close, cancel or reopen | the latest transition stands; a repeat is a no-op |
| add or remove the same label or blocker | the latest event for that pair wins |
| move or reparent the same task | the latest wins |
| note the same task | both notes survive; notes never conflict |
| add the same criterion | applied once |
| set a criterion's state, a `found in` edge or a tree kind | the latest wins |
| purge a task | a tombstone: later events for that id apply to nothing, and its children are purged by reconcile |
| **claim the same task** | the **earlier** claim stands; the later is released with reason `lost-merge`, and the machine that lost is told |
| create a task with the same id | a collision — see below |

**Reconcile.** Applying an event never *derives* anything: if closing the last child closes its parent, that parent close is an explicit event the machine emitted. So a trigger split across two machines leaves an invariant broken, because neither machine saw the other's half. After a rebuild that ingested foreign events, a reconcile pass finds those and appends the repairing events, which propagate like any others. It prints each repair:

```text
reconcile: closed WwFZ9Z as done — every child closed, split across replicas
```

The three rules: a parent whose children have all closed, but whose last one closed elsewhere, is closed; a task whose parent was purged elsewhere is purged; the later of two live claims is released. Repairs are attributed to `reconcile`.

## Collisions and `job rekey`

Task ids are six random base62 characters minted locally, so two machines working apart can — rarely — mint the same one. Nothing merges that silently: both sides have already written the id into notes and commit messages, so a person has to say which task keeps it.

The rebuild fails instead, naming both replicas, both titles, and the command to run:

```text
Error: short id lbWO91 was created on two replicas: sABAuA holds "Ship the migration" and JE3RXW holds "Fix CI".
The earlier replica keeps the id. To give the other task a fresh one, run:
    job rekey JE3RXW:lbWO91
```

```text
$ job rekey JE3RXW:lbWO91 --as ben
Rekeyed JE3RXW's lbWO91 "Fix CI" to BoEli4. The cache is rebuilt; commit .jobs/log to carry the rename.
```

`job rekey` records a `rekeyed` event in this replica's log giving the named replica's task a fresh id. Every machine that pulls the log applies the same rename, so nobody decides twice. The earlier task keeps the id that existing notes cite, and the log names both so a reader can tell what happened. `rekey` reads `.jobs/log` directly rather than the cache, since the cache is what refused to build.

Criterion ids are three characters, and they are unique **per task** rather than across the database — every reference to one already carries its task, so no lookup needs more. That is what keeps them at three characters safely: two tasks may share a criterion id, and only two replicas authoring criteria on the *same* task while apart could collide.

## Adopting a database written before the store

A `.jobs.db` from before the log existed has no log to rebuild from: its event rows carry payloads that were never replayable, and the state they produced lives only in the cache. Converting one happens automatically, on the first command that opens it, the same way a schema migration does — there is no verb, and it prints one line saying what it did.

Every old event row becomes a log line marked `legacy`. Those are recorded and rendered exactly as before by `job log`, `job show`, `job tail` and the dashboard's scrubber, and they touch no state table. One `snapshot` line carries the state itself: every task, block, label, criterion, provenance row and user. The cache is then rebuilt from the result and compared table by table against the original.

**Any difference aborts.** No line is appended, no file is renamed, and the diff is written to `.jobs.db.adopt-failed`. The legacy database keeps working exactly as it did — a failed conversion is never an outage.

On success the original cache is kept as **`.jobs.db.pre-adopt`**, covered by the `.jobs.db*` ignore pattern. Delete it once you are satisfied. `JOBS_NO_ADOPT=1` reads a legacy database as-is for one command without converting it.

## The one hygiene rule

**Never hand-edit a log file.** It is append-only, its sequence numbers are gapless so a truncated file can be told from a complete one, and every other machine will replay exactly what you leave there. A line that fails to parse fails the rebuild, named by file and line. If you need to undo something, record the undo — `job reopen`, `job note`, `job cancel` — and let the log say what happened.

Reading it is encouraged. `git log -p .jobs/` is the tracker's history, and a pull request's diff shows in plain text which tasks it closed and why.

## See also

- [The event log](../events/) — what an event is, and the catalogue of types.
- [Across machines](../../getting-started/across-machines/) — the clone-and-pull walkthrough.
- [Setup reference](../../reference/setup/) — `gitignore`, `rebuild`, `rekey` and `merge` in full.
