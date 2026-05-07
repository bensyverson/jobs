---
title: Initialize a database
weight: 2
---

Jobs stores everything — tasks, events, claims, the lot — in a single SQLite file called `.jobs.db`. One file per project. No server, no daemon, no shared state.

## `job init`

Run from the project root:

```sh
job init
```

```text
Initialized /path/to/project/.jobs.db
Default identity: ben (from $USER)

Recommended .gitignore entries:
  .jobs.db-shm      # SQLite WAL index (always local)
  .jobs.db-wal      # SQLite WAL journal (always local)

To also keep the tracker local (don't check in the tree):
  .jobs.db

Or run: job init --gitignore  to write these for you.
```

`init` always creates the database in the current directory, even if an ancestor directory already has one — there is no silent no-op. Pass `--force` to overwrite an existing `.jobs.db` in this directory.

After init, `job` walks up from your CWD looking for an ancestor `.jobs.db` (the same way `git` finds `.git`), so you can run any verb from anywhere inside the project. `--db <path>` and the `JOBS_DB` environment variable both override the walk.

## `--default-identity`

Every write to the database — `add`, `done`, `claim`, `note`, etc. — is attributed to a named identity. By default, `init` records `$USER` as the writer. To pin a specific name (typical when an agent will be the primary writer):

```sh
job init --default-identity claude
```

The name is a label, not a credential. Anyone with file access can write as anyone — it's there so the event log says *who* did *what*.

After init, change the default with `job identity set <name> --as <name>`. The `--as` is required because the change itself is a write that needs attribution (bootstrap discipline).

## `--strict`

Strict mode opts out of the default-identity entirely. Every write must carry an explicit `--as <name>`:

```sh
job init --strict
```

```text
Initialized /path/to/project/.jobs.db
Strict mode: writes require --as <name> (no default identity).
```

Try a write without `--as` and the call refuses cleanly:

```sh
$ job add "Ship v1"
Error: identity required. Pass --as <name> before the verb.
```

```sh
$ job --as alice add "Ship v1"
ChvF2
```

Reach for `--strict` when multiple agents share one repository and unattributed writes would muddle the log. Toggle it after init with `job identity strict on|off --as <name>`.

## `--gitignore`

Whether or not you check `.jobs.db` itself into source control, the SQLite write-ahead log files (`.jobs.db-shm`, `.jobs.db-wal`) are always local — they should never be committed. `--gitignore` writes the right entries for you:

```sh
job init --gitignore
```

```text
Initialized /path/to/project/.jobs.db
Default identity: alice (from --default-identity)
Wrote 2 entries to .gitignore: .jobs.db-shm, .jobs.db-wal
```

If your `.gitignore` already has them, the line reads `.gitignore already includes .jobs.db-shm and .jobs.db-wal` and nothing is rewritten.

Whether to commit `.jobs.db` itself is a project call: a single-developer repo benefits from a shared event log; a multi-tenant SaaS template probably doesn't. The default is to leave it out of source control by adding `.jobs.db` to `.gitignore` manually — `--gitignore` deliberately stops short of that decision.

## What's next

You have an empty database and a known identity. Next: [author a plan, import it, and walk it from claim to done](../first-plan/).
