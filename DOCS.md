# Jobs

Jobs is a command-line tool that keeps track of your tasks in a single file so you can organize work from the terminal. The project is **Jobs**; the binary you invoke is `job` (singular, since each invocation acts on a job).

[MIT License](LICENSE)

## Install

```sh
go install github.com/bensyverson/jobs/cmd/job@latest
```

This drops the `job` binary into `$HOME/go/bin` (or `$GOBIN` if set). Make sure that directory is on your `PATH`.

### From a local checkout

```sh
make install        # go install ./cmd/job
make build          # local binary at ./job
make run ARGS="ls --mine"
make test
```

See the [Makefile](Makefile) for every target, or run `make help`.

If you plan to contribute, point Git at the repo's vendored hooks once
per clone:

```sh
git config core.hooksPath scripts/git-hooks
```

This activates the pre-commit hook (`scripts/git-hooks/pre-commit`),
which runs `go vet`, `go fix`, `gofmt`, `go mod tidy`, the test suite,
and `go build` before every commit and aborts on any change.

## Get started

```sh
# Create a task database in the current directory
job init

# Add tasks (uses default identity)
job add "Ship v1"

# Add subtasks
job add "Ship v1" "Write tests"
job add 5xZie "Fix CI"

# See what needs doing (reads need no identity)
job ls

# Complete a task
job done <id>

# Claim a task (default 30m)
job claim <id>

# ... or with an explicit duration
job claim <id> 4h

# Close a task and all of its open subtasks in one call
job done <id> --cascade

# Close multiple tasks atomically
job done <id1> <id2> <id3>

# See the full history
job log <id>
```

## Identity

Every write is attributed to a named identity. Reads (`ls`, `show`, `next`, `log`, `tail`, `status`) work without one.

Resolution chain, first match wins:

1. `--as <name>` flag on the call
2. DB-level default identity (set at `init` time, unless strict mode is on)
3. error: `identity required. Pass --as <name> ...`

```sh
# init records $USER as the default, so subsequent writes work unadorned:
job init
#   Default identity: ben (from $USER)
job add "Write docs"                 # attributed to ben

# Override the default any time with --as:
job --as alice add "Write tests"     # attributed to alice

# Pick the default explicitly at init time:
job init --default-identity claude

# Or opt out entirely and require --as on every write:
job init --strict
job add "x"                          # → identity required. Pass --as <name> ...
job --as alice add "x"               # ok
```

Change the default after the fact with `job identity set <name>` (itself a write — requires explicit `--as`). Toggle strict mode with `job identity strict on|off`; turning strict off after a strict init leaves the default unset until `identity set` is called explicitly. There is no hidden `$USER` fallback at write time — the only source of the default is whatever's in the database.

Users are created lazily — the first time a new name writes, its row is added to the `users` table.

Multiple agents can work in the same directory simultaneously. Each passes its own `--as` (or shares the default for unrelated tasks). There is no password or key; the name is an attribution label, not a security boundary.

## Commands

**Grammar.** Multi-operation verbs (`label`, `block`) take a subcommand: `job label add ...`, `job block add ...`. Single-operation verbs take a positional id and flags: `job claim <id>`, `job done <id> -m "..."`. The legacy block shapes (`job block <blocked> by <blocker>`, `job unblock <blocked> from <blocker>`) still work and emit a one-line stderr notice naming the canonical form. `job list` and `job info` are silent aliases of `job ls` and `job show` — both names work, neither prints a warning.

**Short flags.** `-m` is the free-text body across commands that take one (`note -m`, `done -m`, `cancel -m`). Common single-letter flags follow the obvious mapping: `-d`/`-t` for `--desc`/`--title`, `-l` for `--label`, `-p` for `--parent`, `-n` for `--dry-run`, `-s` for `--since`, `-e`/`-u`/`-q` for tail's `--events`/`--users`/`--quiet`, `-y` for `--yes`. Letters with strong prior meaning (`-r` recursive, `-f` force, `-v` verbose, `-h` help) are intentionally not reused for unrelated semantics.

### Database

| Command | Description |
|---------|-------------|
| `job init [--force] [--gitignore] [--default-identity <name>] [--strict]` | Create a `.jobs.db` in the current directory. `--force` overwrites an existing one. `init` always creates the database in the current directory even if an ancestor already has one — there is no silent no-op. `--gitignore` appends recommended entries (`.jobs.db-shm`, `.jobs.db-wal`) to `./.gitignore`. `--default-identity <name>` records the writer identity (defaults to `$USER`); `--strict` opts out and requires `--as` on every write. See [Identity](#identity). |
| `job identity set <name>` | Change the default writer identity. Requires `--as <name>` on the call (bootstrap discipline — the change itself is attributed). |
| `job identity strict on\|off` | Toggle strict mode. Requires `--as`. |
| `job schema` | Print the JSON Schema for the `job import` grammar. Useful for feeding an agent the exact shape it should produce. |

Every command accepts `--db <path>` to use a different database file. You can also set `JOBS_DB`.

When neither is set, `job` walks up from the current directory looking for a `.jobs.db` in an ancestor directory — the same way `git` finds `.git`. That means you can run `job ls` from anywhere inside your project. If no ancestor has one, `job` falls back to the literal `.jobs.db` in the current directory (so `job init` keeps working unchanged).

All writes additionally require `--as <name>` (see [Identity](#identity)).

### Creating tasks

| Command | Description |
|---------|-------------|
| `job add [parent] <title>` | Add a new task. Optionally under a parent. |
| | `-d, --desc <text>` Set a description. Always literal — no `@path` expansion, so a description starting with `@` is stored as typed. |
| | `-F, --file <path>` Read the description from a file; `-F -` reads stdin. Mutually exclusive with `-d`. |
| | `-b, --before <id>` Insert before this sibling. |
| | `--found-in <id>` Record the task that surfaced this one. Provenance only — no parenting, no blocking. The source is resolved before the task is created, so a bad id leaves nothing behind. |

### Viewing tasks

| Command | Description |
|---------|-------------|
| `job ls [parent] [--all]` | List actionable tasks. `--all` (or the legacy positional `all`) shows live work plus a flat "Recently closed (N of M)" footer of the 10 most recently closed tasks; closed children of an open parent render inline under that parent so local context stays visible. `--since <window>` (e.g. `5m`, `2h`, `7d`) or `--since <count>` (e.g. `50`) widens the footer; `--no-truncate` removes the cap entirely. The two are mutually exclusive. `--format=json` returns the full closed history, bypassing the cap. Use `-l, --label <name>` to filter, `--mine` for caller-claimed only, `--claimed-by <name>` for a specific agent. `job list` and `job tree` are silent aliases. |
| `job show <id> [id ...]` | Show full details for one or more tasks, separated by a blank line. When the task has 1–10 direct children, a `Children:` section lists them inline as a markdown checklist (same shape as `job ls` rows, including blockers / claim / labels in parens); above 10 it collapses to a count line pointing at `job ls <id>`. Includes a `Notes:` section listing every `noted` event chronologically with actor and relative timestamp. Description and note bodies are unwrapped on render — author-supplied single newlines collapse to spaces, blank-line paragraph breaks and bullet lists are preserved. `--ancestors` prepends each ancestor's id, title, and description before the named node, in root → parent → node order, so a single call gives the full plan context without extra round-trips. When a found-in reference is recorded, a `Found in:` line names the source with its status, and a `Surfaced:` checklist on the source names what it produced. `--format=json` returns a JSON array; the `children` field is an array of `{id,title,status,blockers?,labels?}` objects, and `found_in` / `surfaced` carry the provenance edges. `job info <id>` is a silent alias. |
| `job next [parent]` | Show the next available leaf (a task with no open children). Pass `--include-parents` to surface any available task. `-l, --label <name>` filters. |
| `job orient [id]` | Regenerate the full plan tree around a target leaf for a fresh agent: a synthesized `orient:` header (target, blockers/blocks, criteria tally, own/weigh notes) followed by the `tasks:` tree. No id defaults to the next available leaf (respecting [focus](#identity)); `--scope <id>` narrows what's rendered; `--full` keeps done-task history (default view elides it). Read-only, never requires `--as`, YAML-only (`--format md` is planned). Unlike `next` and `claim --next`, an empty tree is a valid answer, not an error: when there's nothing to target (an exhausted focused root, or an empty repo), `orient` still exits **0**, printing the same guidance under `orient.target: null` / `orient.message` and rendering whatever's in scope. |
| `job status` | Session briefing: claimed / open / done tally + identity line, then a per-root rollup of the top-level forest (one row per top-level task with its own subtree counts). Ends with a `Next:` hint naming the globally-next claimable leaf, `Stale:` lines for any claims past their TTL, and `Decision:` lines for any open tasks labeled `decision` (human-decision pending). With `--as`, the claimed count is scoped to the caller; without, it counts all live claims. |
| `job status <id>` | Two-level rollup of a task and its direct children: headline counts (`<done> of <total> done · <N> blocked · <N> available · <N> in flight`, with zero-count tokens suppressed) plus one rollup line per direct child. When every direct child is a leaf, the per-child block collapses — the headline already says everything worth saying — and only claimed rows surface (the "who's working on what" signal). Fully-complete subtrees append `closed <timestamp>`. Ends with `Next:` / `Stale:` / `Decision:` trailers scoped to the subtree, followed by the actionable task list (identical to `job ls <id>`). `job summary [id]` is a deprecated alias. |

All support `--format=json` (except `status`, which is always plain text).

List output is GitHub-Flavored Markdown with checkbox items, so pasting `job ls --all` into a PR or issue renders as a task list:

```
- [ ] `87TNz` Phase 1 — Data model
  - [x] `s1Ut5` Write red tests for Lesson
  - [ ] `9aedB` Implement Lesson struct (claimed by claude, 25m left)
  - [ ] `2F1C1` Collapse AnalysisResult (blocked on 9aedB)
```

### Completing tasks

| Command | Description |
|---------|-------------|
| `job done <id> [<id>...]` | Mark one or more tasks done, atomically. Idempotent: already-done IDs are reported, not re-recorded. If the task carries unmarked acceptance criteria, the close refuses with a `cannot close: N pending criteria` error and lists the unmarked rows — see the Acceptance criteria section below for marking and override flags. |
| | `--cascade` Also close all open descendants. Bypasses the strict-criteria gate (children own the criteria). |
| | `-m "<note>"` Record a completion note. Also accepts `-m @path` (read from file) or `-m -` (read from stdin) for multi-line payloads. The ack echoes the stored body beneath the `Done:` line: `  note: <N> chars · "<preview>"`. |
| | `-F, --file <path>` The file form of `-m`, spelled as `git commit -F` spells it: `-F <path>` equals `-m @<path>` and `-F -` equals `-m -`. Mutually exclusive with `-m`. On a multi-id close the one body is recorded on every id, exactly as `-m` does. |
| | `--result '<json>'` Record structured JSON on the `done` event. |
| | `--claim-next` After closing, atomically claim the next available leaf. Collapses the close-then-advance flow into one call. On race (leaf grabbed by another agent between close and claim), done still succeeds and a status line names the taken leaf — claim is opportunistic, close is irreversible. |
| | `--criterion "<ref>=<state>"` Mark one criterion before close. `<ref>` may be the criterion's short_id (3-char base62) or its verbatim label; `<state>` is one of `passed`, `skipped`, `failed`. Repeatable. For multi-id closes, prefix the ref with `<task-id>:`. |
| | `--all-passed` Mark every still-pending criterion as passed before closing. Already-marked rows are left alone; the close ack reports the count touched. Composes with explicit `--criterion` overrides (the explicit row wins). |
| | `--all=<state>` Like `--all-passed` but for `skipped` or `failed`. |
| | `--force-close-with-pending` Close even when criteria remain pending. The unmarked labels are recorded as a waiver on the done event so a reviewer can see what was deferred. |
| | `--format=json` Machine-readable output. |
| `job reopen <id>` | Reopen a completed task and auto-claim it (so you can continue work immediately). `--no-claim` skips the auto-claim. `--cascade` also reopens all done descendants (auto-claim is suppressed with `--cascade` since the parent would have open children). |

After a successful `done`, the ack ends with a `Next:` hint naming the suggested next claimable leaf. The walk starts at the closed task's parent and, at each ancestor level, prefers forward siblings (later sort_order) over earlier ones before stepping up; it only crosses into a different root tree once the closed task's own root is exhausted. Agents following the hint stay inside the current plan as long as there's work there.

#### Acceptance criteria

A task can carry a list of acceptance criteria — short, testable statements
that the close should verify. Each criterion has a state (`pending`,
`passed`, `skipped`, `failed`) and a server-minted 3-char short id (e.g.
`x7e`) usable in the `--criterion <ref>=<state>` form so a label like
`Renders only when the task has a completion note (existing condition)`
survives shell quoting cleanly. Criteria are authored via the YAML import
grammar (`criteria: [...]` on a task) or `job add --criterion <label>`;
they are listed by `job show`, with the short id at the head of each row,
and tracked through their own event types (`criteria_added`,
`criterion_state`).

`job done` is strict by default: if a target carries unmarked pending
criteria, the close refuses with a stable, greppable
`cannot close: N pending criteria` error that lists the unmarked labels and
names the override. Three close shapes recover:

- Mark each row before the close (`--criterion x7e=passed`, repeatable).
- Use the bulk shorthand (`--all-passed`, or `--all=skipped`/`failed`) to
  mark every remaining pending row in one flag. The close ack reports
  `Marked N criteria <state> before closing.`
- Pass `--force-close-with-pending` to deliberately close with the
  unmarked labels recorded as a waiver on the done event.

`--cascade` closes are unaffected — the criteria belong to the children.
`job cancel` is unaffected — canceled work's criteria are moot. Tasks
without criteria see no new friction.

### Editing tasks

| Command | Description |
|---------|-------------|
| `job edit <id> [-t <title>] [-d <desc>]` | Replace title and/or description. `-d ""` clears the description. `-F, --file <path>` is the file form of `-d` (`-F -` reads stdin); it is mutually exclusive with `-d`, and `-F` alone satisfies the "at least one of `--title`, `--desc`, `-F`, `--criterion`, `--set-criterion`" requirement. As on `add`, `-d` itself stays strictly literal. |
| `job note <id> -m "<text>"` | Record a timestamped note on a task. Notes are stored as `noted` events on the event log with actor + body + timestamp; the task's `description` is never modified. Notes appear in the `Notes:` section of `job show` (chronological, with actor and relative timestamp) and remain searchable via `job search` (surface as `MatchSource="note"`). `-m` also accepts `-m @path/to/file.txt` to read from a file (handy for multi-line evidence payloads where shell quoting is painful) and `-m -` to read from stdin. `-F, --file <path>` is the same file form under git's spelling (`-F <path>` = `-m @<path>`, `-F -` = `-m -`), and is mutually exclusive with both `-m` and the positional body. The positional `job note <id> -` form for stdin is still supported. `--result '<json>'` attaches structured JSON to the event. On success the ack echoes the stored body: `Noted: <id> · <N chars> · "<preview>"` (preview snaps to a word boundary; long bodies elide with `…`). |
| `job move <id> before\|after <sibling>` | Reorder a task among its siblings. |

### Cancellation

`cancel` non-destructively stops work on a task. The task transitions to status
`canceled`, history is preserved, and any tasks blocked by it are auto-unblocked.

| Command | Description |
|---------|-------------|
| `job --as <name> cancel <id> [<id>...] -m "<reason>"` | Cancel one or more open tasks atomically. `-m, --reason` is mandatory (`-m` matches `note -m` and `done -m` as the cross-command "free-text body" short flag — `-r` is intentionally avoided to dodge "recursive" muscle memory). The ack echoes the reason in the same preview format as `note`/`done`: `  reason: <N> chars · "<preview>"`. |
| | `-F, --file <path>` Read the reason from a file; `-F -` reads stdin. Mutually exclusive with `-m`. Since `-m` now routes through the same resolver as the other body verbs, `-m @path` and `-m -` work here too. |
| | `--cascade` Also cancel every still-open descendant. |
| | `--purge` Erase the task row and its events instead of transitioning state (audit trail kept on the parent task). Requires `-m`. |
| | `--purge --cascade -y` Erase an entire subtree. `-y, --yes` is required and there is no interactive prompt. |
| | `--format=json` Machine-readable output. |

`reopen` accepts both `done` and `canceled` tasks, returning them to `available`.

### Claiming

| Command | Description |
|---------|-------------|
| `job claim <id> [duration]` | Claim a task. Duration defaults to `30m`. Units: `s`, `m`, `h`, `d`. Ack echoes the title for confirmation: `Claimed: <id> "<title>" (expires in <dur>)`. The full `show <id>` briefing follows the ack, so `claim` is also the briefing — no follow-up `show` needed. The first line stays the load-bearing scriptable signal (scripts grepping for `Claimed:` keep working); same flow for `claim-next` and `done --claim-next`. `-m "<text>"` records a starting note in the same transaction; `-F <path>` reads it from a file (`-F -` from stdin). |
| `job release <id>` | Release a claim. `-m "<text>"` records a parting note; `-F <path>` reads it from a file (`-F -` from stdin). |
| `job claim-next [parent] [duration]` | Find and claim the next available leaf in one step. Pass `--include-parents` to claim any available task. |
| `job heartbeat <id> [<id>...]` | Extend your live claim(s) by 30 minutes. Rarely needed — any write to a task you hold (`note`, `edit`, `label add`, `label remove`) auto-extends the claim. Reach for `heartbeat` only for the "I'm thinking, not writing" case. |

Claims are attributed to the `--as` name. Claims expire automatically. `--force` overrides an existing claim. Writes to a claimed task by its holder auto-extend the TTL — keep noting, editing, or labelling and the claim stays fresh without explicit heartbeats.

#### Leaf-frontier semantics

A task is "claimable" iff it has no open children. Parents with open children are descended through, not surfaced, so `next`, `next all`, and `claim-next` return the set of leaves ready to work on.

- `claim <parent-with-open-children>` is refused — the lock has no referent, since the parent's executable work is in its descendants. Claim a leaf instead, or use `--include-parents` on `next` / `claim-next` to fall back to the legacy behavior.
- When the last open child of a parent closes — whether via `done` **or** `cancel` — the parent **auto-closes**, cascading upward. The agent who closed the last child is attributed on every auto-close. The destination depends on the sibling mix: any sibling closed as `done` → the parent cascades to `done`; all siblings canceled → the parent cascades to `canceled`. Cancel-triggered cascades therefore drop "all work in this subtree was dropped" right up the tree, while done-triggered cascades behave as before.
- Adding an open child to a **claimed** parent **auto-releases** the parent's claim. The parent no longer has executable work of its own, so the lock has no referent. The `released` event records the trigger and the prior claimant.

These three behaviors together mean parents are pure scaffolding: you never explicitly claim or close them, and `next` always points at real work.

### Blocking

| Command | Description |
|---------|-------------|
| `job block add <blocked> by <blocker> [<blocker>...]` | Declare that one or more blockers prevent the task from proceeding. Multi-blocker calls run in a single transaction — all-or-nothing. Cycles are detected across the full input set; duplicates collapse to a single edge. |
| `job block remove <blocked> by <blocker> [<blocker>...]` | Remove one or more blocking relationships atomically. Blocks also auto-remove when the blocker is marked done. |

The legacy single-blocker forms `job block <blocked> by <blocker>` and `job unblock <blocked> from <blocker>` still work but emit a one-line stderr deprecation notice routing to the canonical `block add` / `block remove` form.

### Provenance

A **found-in** reference records where a task was surfaced — the leaf someone was working when a defect turned up. It is the opposite of a blocker: it links the two tasks and gates nothing.

| Command | Description |
|---------|-------------|
| `job found-in <task> in <source>` | Record that the task was found while working the source. One source per task; setting a new one replaces the old, and the event records both ids. A task cannot be found in itself; longer loops are allowed, since nothing traverses the edge. |
| `job found-in <task> --clear` | Remove the reference. Clearing a task that has none is an error, so a mistyped id is caught. |
| `job add <parent> <title> --found-in <source>` | Record it at creation time, when the issue is filed onto its own root. |

`job show` prints both ends: `Found in: <id> <title> (<status>)` on the task, and a `Surfaced:` checklist on the source. `--format=json` carries them as `found_in` and `surfaced`.

The reference survives the source being marked done, canceled, canceled by cascade, or deleted — the common case is that the plan ships while the bug does not. It does not survive `cancel --purge`, which erases the row it points at. Neither end blocks the other: both stay claimable and closable regardless of the other's state, and closing the source's tree never cascades into the task.

For the rare defect that genuinely *should* hold a plan open, state both: `job found-in` for the provenance, `job block add <leaf> by <bug>` for the gate.

### Labels

Tasks carry free-form, flat labels. Labels are local to each task — there's no inheritance — and useful for slicing the frontier by area, priority, owner, or any other dimension.

| Command | Description |
|---------|-------------|
| `job label add <id> <name> [<name>...]` | Add one or more labels. Variadic, idempotent, atomic. |
| `job label remove <id> <name> [<name>...]` | Remove one or more labels. Idempotent. |
| `job ls --label <name>` | Filter the list to tasks carrying the label. |
| `job next --label <name>` | Pick the next available task carrying the label. |
| `job next all --label <name>` | The whole claimable frontier, scoped to the label. |

Labels show up on `job show <id>` and inline in `job ls` parentheses. They can also be set at import time via the YAML `labels: [...]` key.

The `decision` label is a convention: tasks labeled `decision` represent questions that must be answered before work can proceed. Open `decision` tasks surface as `Decision: <id> "<title>"` lines in `job status` (global and scoped), making pending human decisions visible alongside `Next:` and `Stale:`.

### Planning

Instead of building up a plan one `job add` at a time, write it as a Markdown document with a fenced YAML block and import the whole tree atomically.

````markdown
# Ship v1

We need to ship a minimal v1 this quarter. Here is the plan.

```yaml
tasks:
  - title: Ship v1
    ref: ship
    labels: [release, p0]
    children:
      - title: Write tests
        desc: cover the happy path and a few edges
        labels: [tests]
      - title: Fix CI
        blockedBy: [Write tests]
  - title: Announce v1
    blockedBy: [ship]
```
````

Then:

```sh
job --as alice import plan.md
```

Every task inside the first fenced YAML block whose top-level key is `tasks:` is created in a single transaction. If anything in the plan is invalid — a missing `title`, a duplicate `ref`, an unresolvable `blockedBy` — nothing is written.

| Command | Description |
|---------|-------------|
| `job import <file.md>` | Import tasks from a Markdown plan. |
| | `-n, --dry-run` Validate without writing. |
| | `-p, --parent <id>` Import under an existing task. |
| | `--format=json` Machine-readable echo of created IDs. |

Per-task keys in the YAML:

- `title` — required. Human-readable title.
- `desc` — optional description; supports YAML block scalars.
- `ref` — optional handle used by other `blockedBy` entries in the same import. Flat namespace across the whole plan. Not persisted.
- `blockedBy` — optional list. Each entry resolves as (1) a `ref` in the plan, (2) a verbatim task title in the plan if unambiguous, or (3) a pre-existing short ID.
- `children` — optional sub-tasks, recursive.
- `labels` — optional list of free-form labels, persisted on the task. Queryable via `list --label <name>`.

Run `job schema` for the full JSON Schema.

### Event history

| Command | Description |
|---------|-------------|
| `job log [<id>\|all]` | Show full event history for a task and its descendants. With no arg (or the literal `all`), streams events from every top-level tree — effectively the whole database. |
| | `-s, --since <rfc3339>` Only events at or after the given timestamp. |
| | `--format=json` Pretty-printed JSON array. |
| `job tail [<id>\|all]` | Stream events in real-time. Polls every second until Ctrl+C. With no arg (or `all`), streams globally from every top-level tree. |
| | `--format=json` Emits one JSON object per line (JSON-lines), suitable for `jq -c` or line-based subscriber agents. |
| | `-e, --events <type,type,...>` Only emit events of the listed types. By default `heartbeat` events are hidden; pass `--events heartbeat` to opt in. |
| | `-u, --users <name,...>` Only emit events from the listed actors. |

Every event includes the actor who performed it.

### Orchestration

For multi-agent workflows, two read verbs let a parent agent dispatch and observe work:

```
job next all                 # full claimable frontier (every available, unblocked, unclaimed task)
job tail <id> --format=json  # stream events as JSON-lines, suitable for piping to subscribers
```

`next all` accepts an optional `[parent]` to scope the frontier to a subtree. The same
arg can come before or after `all`. Returns an empty array (json) or a friendly message (md)
when nothing is claimable; not an error.

#### Synchronous waits

`tail` can block until a task closes, so a parent agent can dispatch work and wait for a
child to finish without polling.

| Flag | Description |
|------|-------------|
| `--until-close=<id>` | Block until the named task reaches `done` or `canceled`. Repeatable: all listed tasks must close before exit. Use `--until-close` bare (no value) to watch the positional id. |
| `-t, --timeout <duration>` | Exit with code **2** if the watch set hasn't drained within this duration. Units: `s`, `m`, `h`, `d`. |
| `-q, --quiet` | Suppress the streamed event output while waiting; the close-transition line still prints. |

Exit codes for `tail --until-close`:

- **0** — every watched task closed cleanly.
- **1** — any other error (task not found, invalid duration, db error).
- **2** — `--timeout` expired with at least one watched task still open.

The `--events` / `--users` display filters are orthogonal to `--until-close`: filters hide
events from the stream, but terminal `done` / `canceled` events always trigger exit.

```sh
# Dispatch and wait up to 5 minutes
job --as claude claim-next phaseRoot
job tail phaseRoot --until-close --timeout 5m --quiet

# Multiple watches
job tail root --until-close=aM8eT --until-close=9aedB --timeout 10m
```

## Web dashboard

`job serve` runs a read-only HTTP dashboard for humans to watch what agents are doing. Local-first: binds `127.0.0.1:7823` by default, no auth, single `.jobs.db`, single user. Foreground process; Ctrl-C to stop. Use `--bind <host:port>` to change the address (binding all interfaces requires passing `--bind 0.0.0.0:N` explicitly).

```sh
job serve                                # loopback, default port
job serve --bind 127.0.0.1:9090          # loopback, custom port
job serve --db /path/to/.jobs.db         # custom database
```

Views:

- `/` — Home: signal cards (activity histogram + alarm cards), an active-claims table, recent completions, an Upcoming panel, a Blocked strip, and a **dependency-flow mini-graph** rendered as a [subway-system map](project/2026-04-25-graph-clarification.md) — one line per parent whose subtree contains active or imminent work, an LCA fork when two or more lines exist, closure markers (`⊘`) on edges into sequence-blocked lines, in-gap `…` dots between non-adjacent visible windows, and a `(+N)` terminal pill summarizing trailing siblings that fall outside the focal's ±N window.
- `/log` — event stream with filter chips (actor / event type / label)
- `/tasks/<id>` — task detail: status, labels, parent, blocked-by, blocks, description, completion note, history

### `/events` JSON + SSE API

`/events` is a stable HTTP API for consumers outside the dashboard (terminal TUIs, Slack bots, editor integrations). It has two modes on the same URL:

- **SSE stream** when the request includes `Accept: text/event-stream`. Replays a backfill since `?since=<id>`, then live-tails via the broadcaster. Each frame has `id:`, `event:`, and `data:` lines; `data:` is a JSON event object.
- **JSON replay** otherwise. Returns a JSON array of events matching the query. No streaming.

Query parameters (all optional, AND-composed):

| Param   | Semantics                                                                 |
|---------|---------------------------------------------------------------------------|
| `since` | Only events with `id > since`. Integer (event id).                        |
| `limit` | JSON mode only; caps the returned array. Default 500, no upper clamp.     |
| `actor` | Match on `actor` field exactly.                                           |
| `task`  | Match on the task's 5-char short id. Matches events on that task only.    |
| `label` | Match if the event's task carries this label.                             |
| `type`  | Match on `event_type` exactly (e.g. `created`, `claimed`, `done`).        |

Response event shape (same in both modes):

```json
{
  "id": 1234,
  "task_id": "aM8eT",
  "task_title": "Ship the migration",
  "event_type": "claimed",
  "actor": "alice",
  "detail": "{...}",
  "created_at": "2026-04-23T19:20:00Z"
}
```

`task_title` is omitted when the event's task has been deleted.

`detail` is an opaque JSON string whose schema varies per `event_type`; current keys include `note` (done/canceled), `text` (noted), and structural metadata for blocker/move events. Treat unknown keys as forward-compatible.

Examples:

```sh
# Backfill and live-tail everything since event 500
curl -N -H 'Accept: text/event-stream' 'http://127.0.0.1:7823/events?since=500'

# One-shot JSON of the 50 most recent `done` events by alice
curl 'http://127.0.0.1:7823/events?actor=alice&type=done&limit=50'
```

## Task IDs

IDs are 5-character, case-sensitive, alphanumeric strings (e.g. `aM8eT`). A mismatch is an error, not a fuzzy match.

## For contributors

Package layout:

- `cmd/job/` — cobra CLI. `package main`, one file per verb (`add.go`, `done.go`, `claim.go`, …) plus `commands.go` for `newRootCmd` and shared CLI helpers.
- `internal/job/` — domain. Runs (`RunAdd`, `RunDone`, `RunClaim`, …), queries, renderers, event logic. The CLI imports this as `job "github.com/bensyverson/jobs/internal/job"` and calls through `job.X`.
- `internal/migrations/` — forward-only SQL migration files (`NNNN_*.sql`). Exposed as an `embed.FS` via `migrations.FS()`. The runner (`internal/job/migrations.go`) applies unapplied migrations inside their own transactions on every `OpenDB` — fresh databases get the baseline; existing databases catch up to head automatically. Idempotent. To add a schema change, drop a new file with the next numeric prefix (e.g. `0003_add_column.sql`) and restart the server; never edit an applied migration.
- `internal/web/` — the read-only web dashboard. Subpackages: `server/` (http.Server lifecycle + mux), `handlers/` (one file per view; `Deps` bundle for DB, templates, broadcaster), `templates/` (embedded html/template engine — layout + partials + pages), `assets/` (embedded CSS/JS/fonts with a content-fingerprint manifest served under `/static/`), `render/` (shared helpers: actor/label color, relative time), `broadcast/` (1-Hz event poll + per-subscriber fanout). See [DESIGN.md](DESIGN.md) and [project/2026-04-21-web-dashboard-vision.md](project/2026-04-21-web-dashboard-vision.md).

Test helpers `job.SetupTestDB`, `job.MustAdd`, `job.MustAddDesc`, `job.MustDone`, `job.MustClaim`, `job.MustGet`, and `job.TestActor` live in `internal/job/testhelpers.go` (non-test file) so both this package's own tests and the CLI integration tests in `cmd/job/` can share them.
