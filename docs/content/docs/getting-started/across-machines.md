---
title: Across machines
weight: 4
---

A project's tasks live in `.jobs/log/*.jsonl`, which is text and belongs in git. Commit it and the plan travels with the repo: clone it somewhere else and the work is there, `git pull` is sync, and two machines can write at the same time without a merge conflict.

Output below is captured from a clean run. Task ids and replica ids are random — **yours will differ; the shapes won't.**

## 1. Commit the log

`job gitignore` has already excluded the cache and this machine's own state, so `git add -A` picks up exactly the right thing:

```sh
job gitignore
git add -A
git commit -m 'Plans the healthz endpoint'
```

```text
 .gitignore             | 5 +++++
 .jobs/log/785RLT.jsonl | 3 +++
 2 files changed, 8 insertions(+)
```

One file, named for this checkout's replica id. No `.jobs.db` — that is a cache, and it is in `.gitignore`.

## 2. Clone, and just run `job status`

There is no setup step on the other machine. A clone carries `.jobs/log/` and nothing else Jobs needs:

```sh
git clone <repo> desktop
cd desktop
ls .jobs
```

```text
log
```

```sh
job status
```

```text
3 open, 0 done (last activity: 5s ago)
Identity: none set · --as required on writes
Store: replica unFNeC "desktop:~/src/desktop" · 1 log file, 4 events · cache rebuilt on open

  Add /healthz endpoint (1MkrBb): 0 of 2 done · next 9FZ08F
Next: 9FZ08F "Write the handler"
```

Three things happened on that one command. A replica id was minted for this checkout and written to `.jobs/local.json`, and the first line this checkout appends to the log is a `replica` event naming it — `desktop:~/src/desktop` by default, from the hostname and the path. `job replica rename <label>` changes it; `job replicas` lists every machine the store has seen. `.jobs.db` was built from the log — that is the `cache rebuilt on open`. And the plan is there, with the same task ids the other machine sees.

`Identity: none set` is the one thing that does not travel, by design: the default identity is per checkout, so pass `--as` or run `job identity set <name> --as <name>` here.

## 3. Work, and commit again

```sh
job claim --next --as alice
```

```text
Claimed: 9FZ08F "Write the handler" (expires in 30m) as=alice

ID:           9FZ08F
Title:        Write the handler
Status:       claimed
Claim:        claimed by alice, expires in 30m
Parent:       1MkrBb (Add /healthz endpoint)
Created:      2026-09-01 21:31
```

```sh
job done 9FZ08F --as alice -m 'Returns 200 OK.'
git add .jobs/log
git commit -m 'Closes the handler task'
```

```text
 .jobs/log/unFNeC.jsonl | 2 ++
 1 file changed, 2 insertions(+)
```

This checkout appends only to **its own** file. The other machine's file is never touched, which is why git never has a conflict to resolve in the log.

## 4. Pull it back

```sh
cd ../laptop
git pull ../desktop
job status
```

```text
2 open, 1 done (last activity: 0s ago)
Identity: alice (default) · strict mode off
Store: replica 785RLT "laptop:~/src/laptop" · 2 log files, 7 events · cache rebuilt on open

  Add /healthz endpoint (1MkrBb): 1 of 2 done · next s50oFD
Next: s50oFD "Wire it into the router"
```

Two log files now, and the close made on the other machine is here. There is no `job sync` verb — `git pull` is the sync, and the next `job` command notices the file grew and rebuilds.

## When the machines were working on the same thing

Most concurrent work composes without a decision: notes, closes on different tasks, labels, new subtasks. Where it doesn't, replay applies a fixed rule and says so. If a cascade was split across the two machines — the last child of a parent closed over there — the rebuild repairs it and prints what it did:

```text
reconcile: closed WwFZ9Z as done — every child closed, split across replicas
```

If both machines claimed the same task while apart, the earlier claim stands and the later is released with reason `lost-merge`. The full table of rules is in [The store](../../concepts/the-store/).

## What's next

- [The store](../../concepts/the-store/) — the record, the cache, and every merge rule.
- [Setup reference](../../reference/setup/) — `rebuild`, `rekey`, and `merge` for a `.jobs.db` that was copied rather than cloned.
