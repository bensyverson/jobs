---
title: Setup
weight: 1
---

The verbs that bring a database into existence, govern who is allowed to write to it, and put one back together after it has been copied: `init`, `gitignore`, `identity`, `schema`, and `merge`. Only the last of them touches tasks, and only to reconcile two copies of the same ones.

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

Appends the entries Jobs needs (`.jobs.db`, `.jobs.db-shm`, `.jobs.db-wal`) to `.gitignore` in the database's directory, creating the file if it doesn't exist.

```sh
job gitignore
```

Idempotent and additive — it appends only the patterns that aren't already present, so it's safe to re-run. No `--as` (it isn't a database write) and no requirement that `.jobs.db` exist yet, so it works before or after `init`.

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

Two promises worth relying on. **The other file is never written** — it is copied somewhere disposable before it is opened, so even the migration that opening a database runs lands on the copy. And **merging the same pair twice changes nothing**: the second run reports no changes and leaves the database's content as it was.

`merge` needs no `--as` and records no event of its own. It transcribes history rather than making it, so there is no actor to attribute — the events it copies keep the actors they already had.

Two things `merge` deliberately leaves alone. **Machine-local settings don't travel**: the default identity and strict mode belong to the checkout that set them, so `merge` never overwrites them from the other file. And **unions do not remember deletions**: a label removed or a blocker cleared on one side comes back if the other side still holds it, because a union has no way to tell "never had it" from "had it and dropped it". Re-run `job label remove` or `job block remove` after a merge if that matters.
