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
  .jobs.db          # Jobs event store (local by default; remove this line to share it)
  .jobs.db-shm      # SQLite WAL index (always local)
  .jobs.db-wal      # SQLite WAL journal (always local)

Run: job init --gitignore  to write these for you.
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

The SQLite write-ahead log files (`.jobs.db-shm`, `.jobs.db-wal`) are always local — they should never be committed, wherever `.jobs.db` itself lands. `--gitignore` writes the right entries for you, including `.jobs.db` itself:

```sh
job init --gitignore
```

```text
Initialized /path/to/project/.jobs.db
Default identity: alice (from --default-identity)
Wrote 3 entries to .gitignore: .jobs.db, .jobs.db-shm, .jobs.db-wal
```

If your `.gitignore` already has them, the line reads `.gitignore already includes .jobs.db, .jobs.db-shm, and .jobs.db-wal` and nothing is rewritten.

`--gitignore` ignores `.jobs.db` itself by default. The database is not a shareable artifact yet: there is no way to merge two diverged copies, or for two people to work the same database independently, so a committed `.jobs.db` is a conflict waiting to happen. (The agent-worktree workflow in `project/agents/delegation.md` assumes the same — a worktree only sees committed files, so agents are pointed at one absolute `--db` path.) If a single-writer project wants its event log in history anyway, remove the `.jobs.db` line and commit it like any other file.

## What's next

You have an empty database and a known identity. Next: [author a plan, import it, and walk it from claim to done](../first-plan/).
