---
title: Observation
weight: 4
---

The reads. Eight verbs — `ls`, `show`, `log`, `status`, `stats`, `next`, `orient`, `tail` — and none of them write. None require `--as`, and every one offers machine-readable output: `--format=json` on all but `orient`, which is YAML-native (it emits a structured plan dump for a fresh agent).

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
job ls --issues                            # only the issue-trees, none of the plan
```

What's worth knowing:

- The default ("actionable") view is the work *you can pick up right now*: available, unblocked, unclaimed. Use `--all` when you want the whole picture, `--open` when you want everything still in flight.
- **Recently closed footer.** Closed tasks render inline under their open parent when the local context is small; otherwise they collect into a flat "Recently closed (N of M)" footer below the tree, capped at 10. Widen with `--since 2h`, `--since 50` (count), or `--no-truncate` for the full closed history. `--since` and `--no-truncate` are mutually exclusive. `--all` scopes the footer to whichever kind is being shown, so `ls --all` and `ls --issues --all` never mix each other's closures.
- **Issue roots are demoted, not tagged.** A root marked as an [issue-tree](../../concepts/tree-kinds/) no longer renders inline in the default forest. When at least one exists, `ls` ends with one trailer line instead: `Issues: 3 open · job ls --issues`, where the count is the unfiltered open total across every issue tree — it doesn't shift with `--label` or `--mine` on the call that printed it. With no issue roots, there's no trailer.
- **`ls --issues` is the complementary view.** It renders only issue-tree roots, in the same row shape as the default forest, and every other filter (`--label`, `--mine`, `--claimed-by`, `--grep`, `--status`, `--all`) composes within it exactly as it does for task-trees. It prints no trailer, and it drops the `issue-tree` tag on each row — every row shown is already one, so the tag would be pure repetition. An explicit `job ls <issue-root-id>` is unaffected by `--issues` either way: naming an id already picks the tree you want.
- **`--format=json` never demotes anything.** The array always includes every root, task-tree and issue-tree alike, each carrying its `kind` — a script can split them itself. `--issues` still narrows the JSON array to issue roots, for scripts that want the same split text gets.
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

A root marked as an [issue-tree](../../concepts/tree-kinds/) carries a `Kind: issue` line under `Parent: (root)`. Task roots print nothing — `task` is the default, and stating it everywhere would be noise.

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

Without an argument, prints the **session preamble** — open / claimed / done counts, time since last event, identity, and a `Store:` line — followed by the forest-level rollup with one row per top-level task. With an id, scopes to that subtree and *skips* the preamble (the preamble is database-wide metadata and doesn't belong on a subtree view).

```sh
job status                                 # preamble + per-root rollup
job status abc12                           # subtree-only
job status --format=json                   # machine-parsable form
```

`status` is the right command to open every session with — identity check and landscape briefing in one call. The "Next:" hint at the bottom of the global view names the leaf the system would hand you if you ran `claim --next` next. With a [focus](../execution/#focus) set, the global view adds a `Focus:` line and the Next: hint resolves *inside* the focused root — when that root is exhausted, the hint spells out the escapes instead of pointing at another tree. The per-root rollup rows and pending decisions always stay forest-wide; focus narrows the hint, never the landscape. Unfocused, the hint skips [issue-trees](../../concepts/tree-kinds/) so it agrees with `next` and `claim --next`.

**Issue-tree roots don't get a rollup row.** The per-root rollup lists task-tree roots only — an [issue-tree](../../concepts/tree-kinds/) root is demoted to one summary line below it: `Issues: 3 open (1 claimed) · next rqWzZ`. `open` counts every non-closed task under an issue root; `claimed` is the subset currently claimed (scoped to the caller with `--as`, exactly like the preamble's own claimed count); `next` names the leaf `job next --issues` would hand out, and the `· next …` tail drops off when nothing is claimable. The whole line is omitted when the database has no issue-tree root.

The `Store:` line names this checkout's replica — its id and its [label](../setup/#replicas) — how much log there is, and whether the cache is current:

```text
Store: replica 6oDqmc "ben-mbp:~/src/healthz" · 2 log files, 10 events · cache in sync
Store: replica 6oDqmc "ben-mbp:~/src/healthz" · 2 log files, 10 events · cache rebuilt on open
```

`cache rebuilt on open` means a log file grew — usually a `git pull` — and `.jobs.db` was replayed from `.jobs/log` before this command ran. See [The store](../../concepts/the-store/).

`--format=json` mirrors the human output's structure. Forest scope returns `{identity, counts, last_activity_unix, roots, next, focus, stale, decisions, issues, store}`, where `store` is `{replica, label, files, events, cache}`; `roots` lists task-tree roots only, and `issues` is `{open, claimed, next}` shaped like `next` — `null` when there is no issue-tree root. Subtree scope swaps the preamble for `{target, children, …}`. See [the JSON output reference](../../machine-interface/json-output/) for the per-field shape.

`summary` is a deprecated alias and emits a stderr notice on every call.

For how the work has *gone* rather than what is happening now, use [`stats`](#stats).

## `stats`

How the work has gone: headline figures for a window of time and a burn-up of scope against done. `status` answers "what's happening now"; `stats` answers "how has it gone". It replays the event log, writes nothing, and needs no `--as`.

```sh
job stats                                  # all time, the whole forest
job stats --since 7d                       # the last week
job stats abc12 --since 30d                # one plan's subtree, last 30 days
job stats --since 14d --until 7d           # the week before last
job stats --since 2026-09-01T00:00:00Z --until 2026-09-15T00:00:00Z
job stats --since 30d --by week            # override the automatic bucket
job stats --timezone Europe/Berlin         # align days to another calendar
job stats --format=json                    # the versioned report, with the full series
job stats --format=csv                     # the series, one row per bucket
```

On this repository's own store, all time, at 80 columns:

```text
Stats  Apr 21 2026 → Sep 26 2026 · by week · America/Chicago

Leaves   created 420 · done 392 · canceled 17 · open 11 · blocked 0
Plans    imported 0 · closed 35 · open 2 · median import→close —
         no imports recorded — job import records them from 2026-09-26
Pace     17.4 done/week · median created→done 41m · claimed→done 3m
Done by  claude 342 · ben 50
         identities that ran `done`, not who did the work

Burn-up  █ done  ▒ blocked  ░ open
403 ┤                                                         ████████████
    │                                                      ███████████████
    │   ██████████████████████████████████████████████████████████████████
    │   ██████████████████████████████████████████████████████████████████
    │█████████████████████████████████████████████████████████████████████
    │█████████████████████████████████████████████████████████████████████
    │█████████████████████████████████████████████████████████████████████
    │█████████████████████████████████████████████████████████████████████
  0 └─────────────────────────────────────────────────────────────────────
     Apr 27                                                         Sep 26
```

### Flags

- **`[id]`** scopes the report to that task's subtree. Without it, the report covers the whole forest.
- **`--since`** is the window's start: a range key (`1h`, `1d`, `7d`, `14d`, `30d`, `all`), a relative duration measured back from now (`90m`, `3d`), or an RFC3339 timestamp. The default, like `all`, is the first event in scope.
- **`--until`** is the window's end: a relative duration (`7d` means a week ago) or an RFC3339 timestamp. The default is now; `all` is refused, since it names no end.
- **`--by`** sets the bucket width — `minute`, `hour`, `6h`, `day` or `week` — overriding the automatic choice below.
- **`--timezone`** is the IANA zone buckets align to (`America/Chicago`, `Europe/Berlin`); the default is the machine's local zone. Days start at local midnight and weeks on Monday, in that zone.
- **`--format`** is `md` (the default — plain text, despite the name, to match the other verbs), `json`, or `csv`. See [`job stats` JSON](../../machine-interface/stats-json/) for both machine shapes.

**Buckets are chosen by the window's span** unless `--by` says otherwise: up to 2 hours by the minute, up to 2 days by the hour, up to 10 days by 6 hours, up to 90 days by the day, and weekly beyond that. So each range key gets its natural unit — `1h` by the minute, `1d` by the hour, `7d` by 6 hours, `14d` and `30d` by the day — and `all` buckets by the span of the store's history, so a day-old store still draws hourly points and an established one reads daily or weekly. The dashboard's range tabs make the same choice. Buckets sit on calendar boundaries in `--timezone`; the first is clipped to `--since` and the last ends exactly at `--until`, so either can be partial.

The burn-up is one column per sample, eight rows tall, from zero up to the window's largest scope: done from the floor, the blocked share above it, open up to scope. It fits `$COLUMNS` when set, else the terminal's width, else 80 columns; when there are more samples than columns, each column shows the last sample it covers.

### What the numbers count

A reader will trust these figures, so here is exactly what they count. The rules live in one place in the core, and the dashboard's [Home chart panel](../../web-dashboard/) reads the same report, so the CLI and the chart cannot disagree.

- **The unit is a leaf.** A task counts while it has no children — [issue](../../concepts/tree-kinds/) leaves included. Parents never count: they are bookkeeping that closes when their last child does, and counting them would inflate every figure after a plan import. Issue roots never count, even while empty.
- **Leaf-ness is judged at each instant.** A leaf that is [split](../planning/#split) or given children stops counting at that moment, and its children count from it.
- **Every sample is the store's state as of that instant**, replayed from the log — what `job status` would have said then. A reopened task leaves the done line and rejoins it when it closes again, so the done line can dip, and no task is ever counted done twice.
- **Canceled leaves leave scope.** Scope is leaves that exist and are not canceled; a canceled leaf drops out of it and is counted separately as `canceled`. Purged tasks count at no instant at all.
- **Blocked is open leaves with an unfinished blocker** — a [blocker](../../concepts/blockers/) that is not done, as of that instant.
- **Plans are non-issue roots.** With an id, the scoped task is the only plan.
- **Imports are counted from [`imported` events](../../concepts/events/#event-types)**, which `job import` began recording on 2026-09-26. There is no backfill: a plan imported before then has no event, so it is not counted as imported and has no import→close time. The text report says `no imports recorded` when the store has none at all, and `no imports in this window` when it has some but not in the window. Every import counts, including one nested under `--parent`; import→close is measured on the task the import created.
- **Subtree membership is fixed at `--until`.** With an id, the report counts the tasks under it as of the window's end, over their whole history: a task moved into the subtree counts from its creation, and one moved out counts nowhere. Otherwise a move would read as work appearing on one side and vanishing on the other.
- **Done-by-actor counts the identity that ran `done`**, not who did the work. Nearly every close is run by an agent, some under a human's identity, so this measures identity hygiene as much as authorship.

The headline figures, line by line:

- **Leaves** — `created`, `done` and `canceled` are transitions inside the window; `open` and `blocked` are the state at `--until`. A leaf counts once, by where it stands at `--until`: closed, reopened and closed again is one close, at the last one, and a leaf closed in the window but reopened before its end was not closed in it.
- **Plans** — `imported` is `imported` events in the window; `closed` is plans done at `--until` whose last close fell in the window; `open` is plans open at `--until`; and the median import→close is over imported tasks closed in the window.
- **Pace** — leaves done per week (done ÷ the window's length in weeks, so a window of a few hours extrapolates wildly), and the median created→done and claimed→done over leaves closed in the window. Claimed→done runs from the last claim before the final close; a leaf closed without a claim is left out of it. Spans under a minute print as `<1m`, and a median with nothing to measure prints `—`.
- **Done by** — leaves closed in the window by the identity that ran the final `done`, most first; past five identities the rest fold into one count.

The JSON and CSV also carry an **activity** count per bucket — events, not leaves: every `created`, `claimed`, `done` and `blocked` event on any task in scope, parents included. A leaf closed twice is two `done` events there and one done leaf everywhere else.

## `next`

Shows the next leaf the planner would hand you, without claiming it.

```sh
job next                                   # next leaf across the whole tree
job next abc12                             # next leaf inside the abc12 subtree
job next all                               # full claimable frontier (multiple ids)
job next abc12 all                         # entire frontier inside abc12
job next --label p0                        # restrict to a label
job next --include-parents                 # widen to non-leaf availables
job next --issues                          # walk the issue-trees instead
```

Four facts to keep straight:

- `next` returns *leaves* by default. A task with open children is descended through, never returned. `--include-parents` is the legacy "any available" behavior — useful if you genuinely need to claim a parent task, otherwise leave it off.
- With a [focus](../execution/#focus) set, bare `next` stays inside your focused root and fails loudly (naming the escapes) when it's exhausted; an explicit parent argument bypasses focus.
- **Issue-trees are skipped by default.** `next` answers "what is next in my plan", so roots marked as [issue-trees](../../concepts/tree-kinds/) are not walked. `--issues` asks the opposite question, scoped to your [issue focus](../execution/#focus) when you have one and forest-wide when you don't. Focus is per kind, so a focused issue root scopes `--issues` and nothing else; an explicit parent overrides either default on its own.
- `all` (in either position) returns the whole frontier instead of the single next leaf. Pair with `--format=json` to feed a fanout script that spawns one agent per id.

## `orient`

The worker's session-opener. Where `status` hands the orchestrator a forest-wide landscape, `orient` regenerates the full plan tree around a *single leaf* — the one you're about to work on — with live nodes carrying their complete descriptions, substantive notes, and criteria as a checklist. It replaces the old habit of pasting a whole plan doc at a fresh agent: the tree is live, so the current state of the plan comes along for free.

```sh
job orient                                 # target the next available leaf
job orient abc12                           # target a specific task
job orient abc12 --scope def34             # render only the def34 subtree
job orient --full                          # keep done-task history (unelided)
job orient --format yaml                   # explicit default
job orient --issues                        # target the next open issue instead
```

Output is a top-level `orient:` header followed by the `tasks:` tree. The header is the synthesized punchline — what an agent would otherwise compute by hand before starting:

- `target` / `title` / `root` / `status` — which leaf you're oriented on, and the root of its tree.
- `blockedBy` / `blocks` — what must finish before this task, and what finishing it unblocks (each `blocks` entry carries `{id, title}`).
- `criteria` — a `{passed, total}` tally over the target's acceptance criteria.
- `own_notes` — the target's *own* prior progress notes, inlined for primacy (often empty on a fresh leaf).
- `weigh_notes` — a pointer list of node ids whose notes bear on this task: the target's same-parent sibling leaves that carry notes. Their bodies stay folded in the tree; the header just points at them.

Six things to keep straight:

- **The default target skips issue-trees.** With no id, orient targets the next available leaf in a *task-tree*; `--issues` targets the [issue-tree](../../concepts/tree-kinds/) frontier instead. An explicit id always wins over both.
- **The default target respects your focus.** With no id and a [focus](../execution/#focus) set, orient targets the next available leaf *inside your focused root* — your task focus by default, your issue focus with `--issues`. An explicit id ignores focus entirely.
- **An empty tree is a valid answer, not an error.** When there's nothing available to target — the focused root is exhausted, or the whole repo is (with no focus) — orient still exits **0**. It prints the same guidance `next` would have failed with (naming the root and the escapes when a focus is exhausted) under `orient.target: null` and `orient.message`, in place of the usual synthesized header, and `tasks:` still renders whatever's in scope: the focused root's tree, or every root in the forest with no focus. `--issues` scopes this the same way it scopes the target — the focused *issue* root, or every issue root with no issue focus, never a task-tree. `next` and `claim --next` keep their non-zero exit for this same condition — there the caller explicitly asked for a task and didn't get one.
- **Target and scope are orthogonal.** The positional id (or the next leaf) is *what you're working on*; `--scope` only bounds *what gets rendered*. By default the scope is the target's whole root tree — the full context the plan doc used to supply. `--scope <id>` narrows it to a subtree for very large plans, but `root` in the header still names the true root.
- **Done tasks are elided to stay in context.** A plan accumulates history monotonically, and re-emitting all of it eventually pushes orient output past what an agent can read in one gulp (a real 90-task tree measured 231KB, 91% of it done-task history). So the default view reduces done tasks to `title / id / status / closed`: their notes and criteria are dropped, and their `desc` is dropped too *unless the done task has children* — container descriptions carry the slice-level plan narrative and are kept. The shape and order of finished work stays visible; any elided history is one `job show <id>` away. One breadcrumb survives: the most recently closed task with a completion note carries it as `completion_note`, so a fresh agent sees what just happened. `--full` restores the unelided view.
- **Notes are filtered to substance.** Each live node folds in its `noted` events and completion note; churn — heartbeats, claims, releases, moves, label edits, block/unblock — is excluded. The raw trail is always there in `job log`.

`orient` is read-only and never requires `--as`. `--format` defaults to `yaml`; a leaner `--format md` (YAML front-matter plus a markdown checklist tree) is planned and currently returns a not-yet-implemented message.

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
