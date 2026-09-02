---
title: Setup
weight: 1
---

The verbs that bring a store into existence, govern who is allowed to write to it, and put one back together: `init`, `gitignore`, `identity`, `schema`, `merge`, `rebuild` and `rekey`. Only the last three touch tasks, and only to rebuild them from the log or reconcile two copies of the same ones. [The store](../../concepts/the-store/) is the model these last three assume.

## `init`

Creates a `.jobs.db` in the current directory and records a default writer identity. `--as <name>` or `--strict` is required — there's no fallback identity to fall back to.

```sh
job init --as claude                      # pin a specific name as the default
job init --strict                         # no default; every write needs --as
job init --force                          # overwrite an existing .jobs.db
```

A few things worth knowing that the help text doesn't dwell on:

- Without `--as` or `--strict`, `init` refuses before creating anything: `identity required. Pass --as <name> (writes without --as are attributed to it), or --strict to require --as on every write.`
- There is no `$USER` fallback, ever. The database only ever holds what you pass to `--as`. Move the project to another machine and the recorded default still holds.
- `--strict` is the only mode that accepts no default. It's the right choice when several agents share a checkout and you want every event attributable to whoever is actually working — there's no ambient identity to forget.
- When the current directory is a git repository and `.gitignore` is missing an entry Jobs needs, `init` prints a copy-pasteable hint after the identity line, and points at `job gitignore` to write it for you.

## `gitignore`

Appends the two entries Jobs needs — `.jobs.db*` and `.jobs/local.json` — to `.gitignore` in the database's directory, creating the file if it doesn't exist.

```sh
job gitignore
```

Idempotent and additive — it appends only the patterns that aren't already present, so it's safe to re-run. No `--as` (it isn't a database write) and no requirement that `.jobs.db` exist yet, so it works before or after `init`.

Two patterns, not three, and the `*` is doing the work. `.jobs.db` is a disposable cache rebuilt from `.jobs/log`, and everything that sits beside it under the same name — the WAL sidecars `-shm` and `-wal`, the store lock `.jobs.db.lock`, the adoption backup `.jobs.db.pre-adopt` — is local too. `.jobs/local.json` holds this machine's replica id, clock, default identity, strict flag and focus, and no other checkout should ever see it. Everything else under `.jobs/` — the log files that are the actual record — is meant to be committed.

A `.gitignore` written by an older Jobs carries `.jobs.db`, `.jobs.db-shm` and `.jobs.db-wal` instead. Nothing rewrites those: `job gitignore` appends the two current patterns alongside them, and the old lines are harmless.

## `identity`

Two subcommands, both writes, both require `--as`.

```sh
job identity set claude --as ben          # change the default identity
job identity strict on  --as claude       # require --as on every write
job identity strict off --as claude       # restore the convenience of a default identity (still unset until `identity set`)
```

The `--as` requirement is bootstrap discipline: the change is itself a write, so it needs attribution. Otherwise an unattributed `identity set` could quietly relabel every following commit.

Toggling strict *off* does **not** revive a prior default — it leaves the default unset until you run `identity set` explicitly. Plan accordingly.

## `schema`

Prints the live JSON Schema that `job import` validates against — the canonical answer to "what can a plan file contain?"

```sh
job schema | less
job schema > schema.json                  # pin a copy for editor tooling
```

The schema is generated from the same Go types that drive the importer, so it's never stale. When the [Plan grammar](../../plan-grammar/) page disagrees with `job schema`, trust the schema. Pipe it through `jq` to extract a single keyword or property.

`schema` is a read; no `--as` required, no events emitted.

## `merge`

Folds a second, diverged copy of this database into the local one — the repo whose `.jobs.db` was copied to another machine and then written on both.

```sh
job merge ~/laptop/.jobs.db --dry-run     # see what it would do
job merge ~/laptop/.jobs.db               # do it
job merge ~/laptop/.jobs.db --format=json # the same report, machine-parsable
```

The two files have to be two copies of one database, and `merge` proves it by walking both event logs from the beginning: they share a prefix up to the moment one was copied, and everything after it on either side is that side's tail. Two databases whose logs differ from the very first event are unrelated, and merging them would interleave two histories that were never one — so `merge` refuses.

What happens then is decided per table, on **short ids**, which are the only identity two SQLite files share (row ids are per-file and never travel):

- **A task only one side has** is copied over whole — row, labels, blocks, criteria, `found in` provenance and events.
- **A task both sides hold** merges field group by field group: the task row from the side with the later `updated_at`, labels and blocks as a union, criteria matched by short id with the later edit winning, and notes and events as a union deduplicated on `(task, type, actor, timestamp, detail)`. An event either side holds twice stays there twice.
- **Claims** are leases, not fields: a live claim on either side survives even when that side's row lost — unless the other side closed the task, in which case the close wins and the claim is dropped. An expired lease is never resurrected.

The report names every task that existed on one side only and which side that was, every task both sides touched with the winner for each part of it, and every claim it dropped and why. `--dry-run` (`-n`) prints exactly that and writes nothing.

**The other file is never written** — it is copied somewhere disposable before it is opened, so even the migration that opening a database runs lands on the copy.

`merge` predates the store and works on two `.jobs.db` caches, which is what makes it the recovery path when the log is gone. Two consequences worth knowing. It writes the merged result into the local cache rather than through the log, so the next `job` command [adopts](#adoption) it — appending the arriving events and a snapshot to this replica's log file, and keeping `.jobs.db.pre-adopt`. And because that changes the local event history, **re-running the same merge afterwards refuses** with `these databases are unrelated`: the merge is applied once and is not repeatable. Check the report with `--dry-run` first; if a merge went wrong, `.jobs.db.pre-adopt` is the state before it.

Two clones of one repo do not need `merge` at all. They share the log, so `git pull` and a rebuild are the whole story — see [The store](../../concepts/the-store/).

`merge` needs no `--as` and records no event of its own. It transcribes history rather than making it, so there is no actor to attribute — the events it copies keep the actors they already had.

Two things `merge` deliberately leaves alone. **Machine-local settings don't travel**: the default identity, strict mode and focus live in each checkout's own `.jobs/local.json`, not in the database, so `merge` never touches them. And **unions do not remember deletions**: a label removed or a blocker cleared on one side comes back if the other side still holds it, because a union has no way to tell "never had it" from "had it and dropped it". Re-run `job label remove` or `job block remove` after a merge if that matters.

## `rebuild`

Throws the cache away and replays the log into a fresh one.

```sh
job rebuild
```

```text
Rebuilt the cache from 2 log file(s), 10 event(s). Replica 6oDqmc.
```

`.jobs/log/*.jsonl` is the record; `.jobs.db` is a cache of it that can be deleted at any time. The cache records a **watermark** for each log file — the byte offset it has applied — and every `job` command checks it: sizes equal everywhere and no unknown file means there is nothing to do, which costs one `stat` per file. Anything else rebuilds automatically. So there is no `job sync`: sync is `git pull`, and the next command notices.

`rebuild` forces that replay. Reach for it after a crash, or when you suspect the cache. It cannot lose anything, because the cache holds nothing the log does not.

**Reconcile.** A rebuild that ingested another replica's events also repairs the invariants a single machine would have kept. Applying an event never *derives* anything — a cascade close is an explicit event the handler emitted — so a trigger split across two machines leaves the invariant broken, because neither machine saw the other's half. Reconcile finds those and appends the repairing events, which propagate like any others:

- a parent whose children have all closed, but whose last child closed on the other machine, is closed;
- a task whose parent was purged elsewhere is purged;
- two claims made on one task while the machines were apart leave the **earlier** one standing, and the later is released with reason `lost-merge`.

Every repair is printed, and the losing claimant is named.

`rebuild` needs no `--as`: it records nothing except the repairs, which are attributed to `reconcile`.

### Adoption

A `.jobs.db` written before the store existed has no log to rebuild from: its event rows carry payloads that were never replayable, and the state they produced lives only in the cache. There is no verb for converting one — it happens on the first `job` command that opens it, the same way a schema migration does, and prints one line saying what it did:

```text
note: adopted this database into the store: 2 events carried across as history, a snapshot of 3 tasks written, replica LBq6oh. The previous cache is at /path/to/project/.jobs.db.pre-adopt.
```

Every old event row becomes a log line marked `legacy`, which is recorded and applied: `job log`, `job show`, `job tail` and the dashboard's scrubber see the same history they always did, and nothing about it touches the state tables. One `snapshot` line carries the state itself — every task, block, label, criterion, provenance row and user. The cache is then rebuilt from the result and compared, table by table, against the original. **Any difference aborts**: no log line is appended, no file is renamed, and the difference is written to `.jobs.db.adopt-failed` so you can see it. On success the original cache is kept as `.jobs.db.pre-adopt`, which the `.jobs.db*` ignore pattern covers, and you can delete it once you are satisfied.

Set `JOBS_NO_ADOPT=1` to read a legacy database exactly as it is for one command, without converting it.

## `rekey`

Resolves a short id that two replicas minted independently.

```sh
job rekey k7Qx2m:VBF5uQ --as ben
```

Task ids are six random base62 characters minted locally, so two machines working apart can, rarely, mint the same one. That is not something to merge silently: both sides have already written the id into notes and commit messages. The rebuild fails instead, naming both replicas and both titles, and printing the exact `job rekey` command to run.

`rekey` mints a fresh id for the named replica's task and records a `rekeyed` event in this replica's log. Every machine that pulls the log applies the same rename, so nobody decides twice. The **earlier** task keeps the id — it is the one the existing notes point at — and the log names both, so a reader can tell what happened.

It reads `.jobs/log` directly rather than the cache, since the cache is what refused to build, and rebuilds when it is done. Commit `.jobs/log` afterwards to carry the rename to the other machine.
