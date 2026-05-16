# `job` CLI feedback — 2026-05-07

Date: 2026-05-07
Author: Claude (Opus 4.7) — observed while writing the five remaining Documentation site content sections (`zwrjL` plan)
Scope: ergonomics that surfaced while driving a docs-writing session through the `job` CLI. Three friction points worth filing; the rest of the surface felt clean.

## What worked

Documenting the positives so they don't get refactored away.

- **`job status` as the session opener.** Per-root rollup + `Next:` line gave me everything I needed to start cold. I didn't reach for a follow-up `show` until I needed task-specific detail.
- **`job done <id> --claim-next`.** The killer flag for the close-and-advance loop. The ack ending with the same briefing as `claim`/`show` meant I never composed a follow-up command between tasks.
- **`--all-passed`.** Closed five tasks with criteria today; never had to think about per-criterion id syntax.
- **`-m @<path>` and `-m -` (stdin) on `note` / `done`.** Made writing long completion notes painless. Wouldn't have shipped them as inline shell strings.

## Friction I hit

Three papercuts. Each verified by re-running the failing command before drafting.

### `job claim` rejects `-m`

First thing I tried at the start of each task was a starting note:

```sh
job claim wAZz0 -m "Writing the five reference pages: setup, planning, …"
# Error: unknown shorthand flag: 'm' in -m
```

`release` accepts `-m` (parting note); `done` accepts `-m` (completion note); `claim` is the asymmetry. The natural counterpart to `release -m` is "what I'm about to try" captured at claim time — same shape, opposite end of the lifecycle. I worked around it by burying the same context in the close note instead, but it landed on the wrong end of the timeline.

### `job status --format=json` fails loud with no hint

```sh
job status --format=json
# Error: unknown flag: --format
```

`status` is deliberately human-only — I documented that on the Machine interface page. But an agent reflexively trying JSON on a read verb gets no signal about *what to use instead*. Either accept `--format=json` and emit a JSON shape mirroring the human output (preamble + per-root rollup as fields), or fail with a directive message: `status is human-only; use 'ls', 'show', or 'next' for JSON output.`

### `job add` positional order is `<parent> <title>`, not `<title> <parent>`

```sh
job add "Child A" jTzON
# Error: task "Child A" not found
```

Help is unambiguous — `Usage: job add [parent] <title>` — so this is a user-error story, not a CLI bug. But the error message ("task 'Child A' not found") sent me looking for a missing task instead of recognizing I'd transposed the args. When the trailing positional is missing and the leading positional doesn't resolve as a short id, the parser could plausibly say:

```
task "Child A" not found. If you meant `add <parent> <title>`, the
parent argument comes first; or use --parent to disambiguate.
```

That's the smallest change that would have caught my mistake without a second read of `--help`.

## Suggested changes

YAML below conforms to `job schema`. Titles complete "This task…" in present tense per the project commit style.

```yaml
tasks:
  - title: Smooth three CLI papercuts surfaced in the docs-writing session
    desc: |
      Umbrella task gathering three small ergonomics issues hit while
      writing the five remaining Documentation site content sections on
      2026-05-07. See project/2026-05-07-opus-cli-feedback.md for the
      full report.
    labels: [cli, dx]
    ref: cli-feedback-2026-05-07
    children:
      - title: Accept -m on `job claim` (parity with `done` and `release`)
        desc: |
          `job done <id> -m "..."` and `job release <id> -m "..."` both
          attach a note as part of the lifecycle transition; `job claim
          <id> -m "..."` errors with `unknown shorthand flag: 'm'`. The
          natural use case is a starting note ("what I'm about to try")
          symmetric with release's parting note. Add -m to `claim`; have
          it append a `noted` event before recording the claim so the
          context lands at the start of the timeline rather than the end.
          Keep the @path / stdin shorthands consistent across all three
          verbs.
        labels: [cli, dx]
      - title: Make `job status --format=json` either work or fail with a directive message
        desc: |
          `status` is deliberately human-only, but `--format=json` fails
          with the generic `unknown flag: --format` — no hint about what
          to reach for instead. Two acceptable resolutions:

          A. Accept `--format=json` and emit a JSON shape that mirrors
             the human output: { identity, counts: {open, claimed, done,
             canceled}, last_activity_unix, roots: [...rollup rows],
             next: {id, title}, stale: [...] }. Preferred — script-driving
             agents would use it.

          B. Reject with `status is human-only; use 'ls', 'show', or
             'next' for JSON output.` Cheap to ship, fixes the
             discoverability problem if (A) is out of scope.

          Pick one; the current state is the worst of both.
        labels: [cli, dx]
      - title: Direct the user toward the right fix when `job add` positional args look wrong
        desc: |
          Today's failure modes both produce misleading or silent results:

          1. `job add "Child A" jTzON` (title-then-parent, transposed) errors
             with `task "Child A" not found` — accurate but misdirecting; the
             user goes looking for a missing task instead of recognizing the
             arg order.
          2. `job add jTzON` (parent only, title forgotten) silently creates a
             root task literally titled `jTzON` — no error at all, and the
             slip only surfaces later when the stray root task appears in
             `job status`.

          Keep the positional order strict (`add [parent] <title>`) — auto-
          detecting which arg is which based on "looks like a short id" is
          DWIM that fails on short titles that happen to collide with the
          short-id shape. Instead, branch the error on what's actually wrong:

          - Two args, leading arg doesn't resolve as a short id → `no such
            parent "Child A"; positional order is 'add <parent> <title>', or
            use --parent to disambiguate.`
          - One arg, and that arg DOES resolve as an existing task's short
            id → `"jTzON" is an existing task — did you mean 'add jTzON
            <title>'? (To create a root task literally named "jTzON", pass
            --parent="" to disambiguate.)` Refuse the create until the user
            confirms intent.

          The common case — one arg that doesn't resolve as a short id — stays
          frictionless: a root task is created with that title.
        labels: [cli, dx]
        criteria:
          - "Two-arg call with an unresolved leading positional errors with a hint naming the correct order and `--parent` as the disambiguator"
          - "Single-arg call where the arg matches an existing short id refuses the create and prompts the user to either supply a title or pass `--parent=\"\"` for the literal-title intent"
          - "Single-arg call with a non-short-id string still creates a root task with that title — no new friction for the common case"
          - "Two-arg call with a valid leading parent short id still creates the child as before — no behavior change on the happy path"
          - "Each new error message has a stable, greppable leading clause so retry automation can branch on it"
```
