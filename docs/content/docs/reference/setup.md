---
title: Setup
weight: 1
---

The three verbs that bring a database into existence and govern who is allowed to write to it: `init`, `identity`, and `schema`. None of them touch tasks.

## `init`

Creates a `.jobs.db` in the current directory and records a default writer identity.

```sh
job init                                  # uses $USER as the default
job init --default-identity claude        # pin a specific name
job init --strict                         # no default; every write needs --as
job init --gitignore                      # also append SQLite WAL/SHM lines
job init --force                          # overwrite an existing .jobs.db
```

A few things worth knowing that the help text doesn't dwell on:

- The `(from $USER)` suffix in the ack is your hint that the default came from the environment. Once recorded, the database is the source of truth — `$USER` is never re-consulted on later writes. Move the project to another machine and the original default holds.
- `--strict` is the only mode that accepts no default. It's the right choice when several agents share a checkout and you want every event attributable to whoever is actually working — there's no ambient identity to forget.
- `--gitignore` is idempotent and additive. It appends only the entries that aren't already present, so it's safe to re-run.

## `identity`

Two subcommands, both writes, both require `--as`.

```sh
job identity set claude --as ben          # change the default identity
job identity strict on  --as claude       # require --as on every write
job identity strict off --as claude       # restore default-identity convenience
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
