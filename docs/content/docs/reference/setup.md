---
title: Setup
weight: 1
---

The verbs that bring a database into existence and govern who is allowed to write to it: `init`, `gitignore`, `identity`, and `schema`. None of them touch tasks.

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
