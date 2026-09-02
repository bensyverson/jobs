---
title: Initialize a database
weight: 2
---

Jobs stores everything — tasks, events, claims, the lot — in a single SQLite file called `.jobs.db`. One file per project. No server, no daemon, no shared state.

## `job init --as <name>`

Run from the project root. `--as <name>` is required — it's recorded as the default identity, so every write that follows is attributed without repeating it:

```sh
job init --as alice
```

```text
Initialized /path/to/project/.jobs.db
Default identity: alice
```

Use the name of whoever is running the command: a person's handle, or — for an automated assistant — the assistant's own name, not the account it runs under. `$USER` is the human who launched the session, which is usually not who is doing the work; an agent named `claude` should run `job init --as claude`, not inherit the operator's login.

`init` always creates the database in the current directory, even if an ancestor directory already has one — there is no silent no-op. Pass `--force` to overwrite an existing `.jobs.db` in this directory.

After init, `job` walks up from your CWD looking for an ancestor `.jobs.db` (the same way `git` finds `.git`), so you can run any verb from anywhere inside the project. `--db <path>` and the `JOBS_DB` environment variable both override the walk.

After init, change the default identity with `job identity set <name> --as <name>`. The `--as` is required because the change itself is a write that needs attribution (bootstrap discipline).

`init` also writes `.jobs/local.json` beside the database. That file holds the state that belongs to *this machine* rather than to the project: the default identity, the strict flag and your [focus](../../reference/execution/#focus). Keep it out of version control — `job gitignore` ignores the database, and `.jobs/local.json` belongs in the same list. `--force` rewrites it along with the database, so a re-init never inherits the old checkout's strict flag or focus.

## `--strict`

Strict mode opts out of a default identity entirely. Every write must carry an explicit `--as <name>`. Pass `--strict` instead of `--as`:

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

Neither `--as` nor `--strict` given? `init` refuses before touching disk — no database is created:

```sh
$ job init
Error: identity required. Pass --as <name> (writes without --as are attributed to it), or --strict to require --as on every write.
```

## The gitignore hint

`.jobs.db` is a disposable cache, and so is everything SQLite and Jobs keep beside it under the same name: the write-ahead log files `.jobs.db-shm` and `.jobs.db-wal`, the store lock, the adoption backup. None of them should ever be committed. `.jobs/local.json` is this machine's own state — its replica id, clock, default identity, strict flag and focus — and is local for a different reason. When the current directory is a git repository and `.gitignore` is missing either entry, `init` prints a hint after the identity line:

```text
Add to .gitignore (or run: job gitignore):

# Jobs cache and its sidecars — disposable, rebuilt from .jobs/log
.jobs.db*
# This machine's replica id, identity and focus
.jobs/local.json
```

The block is unindented and carries no trailing comments, so it pastes into `.gitignore` unchanged. Outside a git repository, or once every pattern is already present, `init` prints nothing extra.

`.jobs.db` is never the artifact to share: it is rebuilt from `.jobs/log/*.jsonl`, which is text, appends one file per machine, and is what belongs in git. (The agent-worktree workflow in `project/agents/delegation.md` still points agents at one absolute `--db` path, because a worktree only sees committed files.)

## `job gitignore`

Writes the hint's entries for you instead of pasting them by hand:

```sh
job gitignore
```

```text
Wrote 2 entries to .gitignore: .jobs.db*, .jobs/local.json
```

If your `.gitignore` already has them:

```text
.gitignore already includes .jobs.db* and .jobs/local.json
```

`job gitignore` is idempotent and additive — it appends only the patterns that aren't already present. It needs no `--as` (it doesn't touch the event store) and doesn't require `.jobs.db` to exist yet, so it works before or after `init`.

## What's next

You have an empty database and a known identity. Next: [author a plan, import it, and walk it from claim to done](../first-plan/).
