---
title: Observation
weight: 4
---

The reads. Six verbs — `ls`, `show`, `log`, `status`, `next`, `tail` — and none of them write. None require `--as`, and every one accepts `--format=json` for machine consumption.

## `ls`

Lists tasks as a tree. Default scope is the whole forest; pass a parent to scope to a subtree (recursively, not just direct children).

```sh
job ls                                     # actionable tasks across all roots
job ls abc12                               # full subtree under abc12
job ls --all                               # include claimed, blocked, done, canceled
job ls --open                              # anything not done or canceled
job ls --status claimed                    # one specific status
job ls --label p0                          # filter to tasks carrying a label
job ls --mine                              # tasks claimed by --as / default identity
job ls --claimed-by alice                  # tasks claimed by a specific agent
job ls --grep "auth"                       # case-insensitive substring on title
job ls --mine --label p0                   # filters compose
```

What's worth knowing:

- The default ("actionable") view is the work *you can pick up right now*: available, unblocked, unclaimed. Use `--all` when you want the whole picture, `--open` when you want everything still in flight.
- **Recently closed footer.** Closed tasks render inline under their open parent when the local context is small; otherwise they collect into a flat "Recently closed (N of M)" footer below the tree, capped at 10. Widen with `--since 2h`, `--since 50` (count), or `--no-truncate` for the full closed history. `--since` and `--no-truncate` are mutually exclusive.
- **`tree` and `list` are aliases for `ls`.** Type whichever your fingers prefer.
- **`--format=json` returns the full closed history with no cap.** When you're driving `ls` from a script, JSON is usually what you want.

## `show`

Prints the full briefing for one or more tasks: id, title, description, status, claim info, blockers, children summary, criteria, notes, and creation time.

```sh
job show abc12
job show abc12 abc34 abc56                 # variadic, blank line between blocks
job show abc12 --ancestors                 # prepend the parent chain
job show abc12 --format=json               # array of one or more task records
```

`show --ancestors` is the move when you've been handed an id with no plan context: it prints root → … → parent → node, each with title and description, so you have the full ancestry above the task itself.

`info` is an alias.

## `log`

Event history for a task and its descendants — and, with no positional argument, every top-level task in the database.

```sh
job log                                    # every event in the database
job log abc12                              # subtree under abc12
job log all                                # explicit form of the no-arg case
job log abc12 --since 2h                   # relative duration
job log abc12 --since 2026-04-28T10:00:00Z # RFC3339 timestamp
job log abc12 --actor alice                # only events emitted by alice
job log --format=json --since 1d           # for grepping or piping
```

A subtle distinction: the global `--as` is the *writer* identity; `log --actor` is the filter on emitted events. They name the same thing in different roles, hence the different flag names.

## `status`

Without an argument, prints the **session preamble** — open / claimed / done counts, time since last event, identity — followed by the forest-level rollup with one row per top-level task. With an id, scopes to that subtree and *skips* the preamble (the preamble is database-wide metadata and doesn't belong on a subtree view).

```sh
job status                                 # preamble + per-root rollup
job status abc12                           # subtree-only
```

`status` is the right command to open every session with — identity check and landscape briefing in one call. The "Next:" hint at the bottom of the global view names the leaf the system would hand you if you ran `claim --next` next.

`summary` is a deprecated alias and emits a stderr notice on every call.

## `next`

Shows the next leaf the planner would hand you, without claiming it.

```sh
job next                                   # next leaf across the whole tree
job next abc12                             # next leaf inside the abc12 subtree
job next all                               # full claimable frontier (multiple ids)
job next abc12 all                         # entire frontier inside abc12
job next --label p0                        # restrict to a label
job next --include-parents                 # widen to non-leaf availables
```

Two facts to keep straight:

- `next` returns *leaves* by default. A task with open children is descended through, never returned. `--include-parents` is the legacy "any available" behavior — useful if you genuinely need to claim a parent task, otherwise leave it off.
- `all` (in either position) returns the whole frontier instead of the single next leaf. Pair with `--format=json` to feed a fanout script that spawns one agent per id.

## `tail`

Streams events as they happen, JSON-lines optional. Default scope is the entire database; pass an id to scope to a subtree.

```sh
job tail                                   # everything, all roots, indefinite
job tail abc12                             # subtree
job tail abc12 --format=json               # one event per line, JSON
job tail abc12 --until-close abc12         # block until abc12 closes, exit 0
job tail --until-close=_ abc12             # shorthand: watch the positional id
job tail --until-close abc12 --until-close abc34   # block until BOTH close
job tail abc12 --timeout 5m                # exit 2 if no close in 5 minutes
job tail abc12 --quiet --until-close abc12 # suppress events; keep close + timeout messages
job tail --events done,canceled            # only those event types
job tail --users alice,bob                 # only events from these actors
```

A few non-obvious facts:

- The **default event filter excludes heartbeats**, which would otherwise flood any long-running tail. Add `heartbeat` to `--events` if you want them.
- `--until-close` is **repeatable** and the call only exits when *every* named task has closed. This is the building block for "wait until this whole batch finishes" workflows.
- `--until-close=_` is the literal shorthand that says "use the positional id." It errors in global scope, where there is no positional id to default to.
- `--timeout` distinguishes "everything finished cleanly" (exit 0) from "we waited long enough" (exit 2). Useful in CI.
- `--quiet` is for the wait-until-close pattern when you don't care about the events themselves — exit code is the signal.
