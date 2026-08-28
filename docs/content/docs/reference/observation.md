---
title: Observation
weight: 4
---

The reads. Seven verbs — `ls`, `show`, `log`, `status`, `next`, `orient`, `tail` — and none of them write. None require `--as`, and every one offers machine-readable output: `--format=json` on all but `orient`, which is YAML-native (it emits a structured plan dump for a fresh agent).

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
- **Issue roots are tagged.** A root marked as an [issue-tree](../../concepts/tree-kinds/) renders as `- [ ] `abc12` Bugs (issue-tree)`, in the same parenthesised group as claims, blockers and labels. `ls` still lists them — it is the "show me everything" view; it is `next`, `orient` and `claim --next` that skip them.
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

Without an argument, prints the **session preamble** — open / claimed / done counts, time since last event, identity — followed by the forest-level rollup with one row per top-level task. With an id, scopes to that subtree and *skips* the preamble (the preamble is database-wide metadata and doesn't belong on a subtree view).

```sh
job status                                 # preamble + per-root rollup
job status abc12                           # subtree-only
job status --format=json                   # machine-parsable form
```

`status` is the right command to open every session with — identity check and landscape briefing in one call. The "Next:" hint at the bottom of the global view names the leaf the system would hand you if you ran `claim --next` next. With a [focus](../execution/#focus) set, the global view adds a `Focus:` line and the Next: hint resolves *inside* the focused root — when that root is exhausted, the hint spells out the escapes instead of pointing at another tree. The per-root rollup rows and pending decisions always stay forest-wide; focus narrows the hint, never the landscape. Unfocused, the hint skips [issue-trees](../../concepts/tree-kinds/) so it agrees with `next` and `claim --next`.

**Issue-tree roots don't get a rollup row.** The per-root rollup lists task-tree roots only — an [issue-tree](../../concepts/tree-kinds/) root is demoted to one summary line below it: `Issues: 3 open (1 claimed) · next rqWzZ`. `open` counts every non-closed task under an issue root; `claimed` is the subset currently claimed (scoped to the caller with `--as`, exactly like the preamble's own claimed count); `next` names the leaf `job next --issues` would hand out, and the `· next …` tail drops off when nothing is claimable. The whole line is omitted when the database has no issue-tree root.

`--format=json` mirrors the human output's structure. Forest scope returns `{identity, counts, last_activity_unix, roots, next, focus, stale, decisions, issues}`; `roots` lists task-tree roots only, and `issues` is `{open, claimed, next}` shaped like `next` — `null` when there is no issue-tree root. Subtree scope swaps the preamble for `{target, children, …}`. See [the JSON output reference](../machine-interface/json-output/) for the per-field shape.

`summary` is a deprecated alias and emits a stderr notice on every call.

### `--usage`

`--usage` switches `status` into an **activity report** mode. The briefing and rollup are replaced by a compact report — for a human glancing at a repo's amount of activity, not for orchestrating the next claim. Read-only and fast; reuses the same indexes as `log`.

```sh
job status --usage                         # all-time activity report
job status --usage --since 7d              # last 7 days
job status --usage --since 30d            # last 30 days
job status --usage abc12                  # scoped to a subtree
job status --usage --format=json          # machine-parsable form
```

The md report (omitting any status whose count is zero) looks like:

```
Usage (all-time)
  open 5 · done 378 · canceled 17 · blocked 2
  completion 95% · cancellation 4%

Activity
  events 1,204 · first 2026-04-29 · last 17d ago
  velocity 3.2/day (over 118d)
  db 412 KB
```

- **Default window is all-time.** Any `--since <duration>` enters windowed mode and the header becomes `Usage (last 7d)` (or whatever duration). `--since` accepts the same RFC3339 or relative-duration grammar as `job log` (`5m`, `2h`, `7d`, `30d`, or `2026-04-28T10:00:00Z`).
- `--since` without `--usage` is a no-op on the default briefing and errors with a pointer to add `--usage`.
- **Status taxonomy** matches the data model: open (= `available`), claimed, done, canceled, and a separate `blocked` count derived from the `blocks` table (an available task with ≥1 non-done blocker). Zero counts are suppressed in the md output but kept in JSON.
- **Velocity is `done events / calendar days`** — the numerator counts every `done` event in scope (a task done → reopened → re-done counts each completion), and the denominator is the calendar span from the first event to now (all-time) or the window length (windowed: literal days, e.g. `--since 30d` → divide by 30). Idle days are not excluded; calendar span is the honest, simple metric. We do not average per-day rates then mean them — that collapses to the same number as `total / N`.
- **JSON shape** carries every status count (zeros preserved), completion/cancellation rates as percentages, the event span as both unix timestamps and ISO strings, and a `velocity` object with `{rate, denominator_days, window, window_days}`. Forest scope omits `scope_task_id`; subtree scope sets it.

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
- **An empty tree is a valid answer, not an error.** When there's nothing available to target — the focused root is exhausted, or the whole repo is (with no focus) — orient still exits **0**. It prints the same guidance `next` would have failed with (naming the root and the escapes when a focus is exhausted) under `orient.target: null` and `orient.message`, in place of the usual synthesized header, and `tasks:` still renders whatever's in scope: the focused root's tree, or every root in the forest with no focus. `next` and `claim --next` keep their non-zero exit for this same condition — there the caller explicitly asked for a task and didn't get one.
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
