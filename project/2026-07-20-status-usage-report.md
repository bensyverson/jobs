# `job usage` — activity report folded into `job status`

*Design + implementation plan · 2026-07-20*

## Context — why

We want a quick way to see, at a glance, how much activity a Jobs repo has seen.
The original spark was a simple status-count view (open / done / canceled) for a
"rough sense of activity", but the discussion widened into a broader "usage"
report before narrowing back down to a lean v1.

Constraints from the user:

- **Audience: humans.** Output is a compact table; `--format json` exists for
  scripts but is secondary.
- **Read-only & fast.** Reuse existing indexes; no expensive analytics.
- **Time-bounded when asked.** All-time is the default so the CLI contract is
  unambiguous: any `--since <dur>` means "windowed".

## Converged decisions

- **No new verb.** The report lives behind a `--usage` mode switch on
  `job status`. `status` already owns "observe the DB" identity; a separate
  verb wasn't worth the extra word.
- **Mode switch, not an overloaded filter.** `--usage` *replaces* the briefing
  and forest rollup; it doesn't reshape or filter them. `--since` on a plain
  `job status` would be a confusing no-op, so the explicit flag keeps the
  semantics clear.
- **Reuses `--since`** — same relative-duration syntax as `job log` and
  `job ls` (`5m`, `2h`, `7d`, `30d`). All-time by default; any `--since` value
  enters windowed mode.
- **Subtree scope via positional `[id]`**, mirroring `job status [id]` and
  `job log [id]`.

## v1 scope

Status counts (open / done / canceled / blocked / paused), completion rate,
cancellation rate, event count, first/last event timestamps, velocity, snapshot
count, and DB file size.

**Zero-count omission:** in the human-readable md output, any status whose
count is zero is omitted from the status row (e.g. a repo with no blocked
tasks shows `open 5 · done 378 · canceled 17`, no `blocked 0`).

## Velocity definition

- **Numerator:** done tasks only (canceled is its own signal, already in the
  status block).
- **Denominator:** calendar days.
  - All-time default: `total_done / (now − first_event)` in days.
  - Windowed (`--since 30d`): `done_in_window / 30` (the literal window size).
- **Rendering:** single number, denominator stated in parentheses so the human
  knows what they're reading — e.g. `velocity 3.2/day (over 118d)`.

We deliberately do NOT average per-day rates then mean them — that collapses
to the same number as `total / N`. We also do NOT exclude idle days from the
denominator; calendar span is the honest, simple metric.

## Deferred (v2 candidates)

Hierarchy shape (depth distribution, leaf vs internal), dependency chains,
assignee/label breakouts, velocity percentiles, "burst pace" (done / days with
activity), single-number activity index. Ship v1, see what humans actually look
at, then revisit.

## Output sketch (human, all-time)

```
Usage (all-time)
  open 5 · done 378 · canceled 17 · blocked 2
  completion 95% · cancellation 4%

Activity
  events 1,204 · first 2026-04-29 · last 17d ago
  velocity 3.2/day (over 118d)
  snapshots 38 · db 412 KB
```

With `--since 30d` the header becomes `Usage (last 30d)` and the rows reflect
the window.

## Work breakdown

```yaml
tasks:
  - title: Add `job status --usage` activity report
    desc: |
      Add a `--usage` mode switch to `job status` that replaces the default
      briefing + rollup with a compact human-first activity report. All-time
      by default; `--since <dur>` (same syntax as `job log`/`job ls`) enters
      windowed mode. Positional `[id]` scopes the report to a subtree, mirroring
      `job status [id]`. Zero-count statuses are omitted from the md output.
      Read-only and fast: reuse existing indexes; no new event types or
      migrations.
    labels: [status, observation]
    children:
      - title: Usage query layer (read-only, fast)
        ref: usage-queries
        desc: |
          In internal/job (new file, e.g. usage.go), add the read-only query
          functions that back the report. Subtree-scoped status counts
          (open/claimed/done/canceled/blocked/paused), event count with
          first/last timestamps (and windowed variants when a since time is
          provided), done-in-window count, total done count, snapshot count,
          and DB file size on disk. Velocity math lives here too:
          total_done / calendar_days(all-time) or done_in_window / window_days
          (windowed). TDD strict-red first: write tests for each shape
          (all-time vs windowed, forest vs subtree, zero-count inputs) and
          watch them fail before implementing.
        criteria:
          - All-time counts match a seeded fixture across the whole forest
          - Subtree scope (positional id) restricts counts to that subtree
          - Windowed mode (--since 7d) reports counts/events done within the window only
          - Velocity uses total_done / calendar_days for all-time and done_in_window / window_days for windowed
          - Zero-count statuses are still returned by the query layer (omission happens at render time)
          - Query paths reuse existing indexes and do not issue table scans
      - title: Render Usage md block with zero-count omission
        ref: usage-render-md
        desc: |
          Human-readable md renderer matching the agreed sketch: header line
          `Usage (all-time)` or `Usage (last 30d)` (or other window labels);
          status row with completion & cancellation rates, omitting any status
          whose count is zero; activity block with event count, first, last
          (rendered as a relative "Nd ago" when sensible), velocity, snapshot
          count, and DB file size. One-liners; reflow cleanly in a terminal.
          TDD: fixtures exercising a repo with all statuses populated, and a
          quiet repo where blocked/paused/canceled are zero, assert the
          omitted rows.
        blockedBy: [usage-queries]
        criteria:
          - Output matches the sketch for an all-time forest report
          - Output header reflects the active window when --since is set
          - Zero-count statuses do not appear in the md status row
          - Subtree-scoped output reads as a subtree report (no DB-wide preamble)
      - title: JSON shape for `--usage --format json`
        ref: usage-render-json
        desc: |
          Machine-readable shape backed by the same struct as md. Includes
          every status count (zero-counts KEPT in JSON, since omission is a
          human-output concern), rates as floats, event span as unix
          timestamps plus iso strings, velocity as {rate, denominator_days,
          window: "all-time"|"windowed", window_days}, snapshot count, and
          db_file_size_bytes. TDD: round-trip a struct through json.Marshal
          and assert the keys.
        blockedBy: [usage-queries]
        criteria:
          - JSON includes all status counts including zeros
          - Velocity object exposes rate, denominator_days, and window kind
          - Event span is represented as both unix and iso timestamps
      - title: Wire `--usage` and `--since` into `job status`
        ref: usage-cli
        desc: |
          In cmd/job/status.go, add a `--usage` bool and a `--since` string
          flag. When `--usage` is set, branch before the existing render and
          run the new report instead. Positional [id] scopes to a subtree.
          `--since` parses the same relative-duration grammar as `job log`
          (share the helper; do not duplicate). Reject `--since` without
          `--usage` with a clear error (it's a no-op on the default status
          view). Update the status long-help to document the new mode. TDD:
          cobra flag tests for every combination (all-time forest, windowed
          forest, all-time subtree, windowed subtree, json output).
        blockedBy: [usage-render-md, usage-render-json]
        criteria:
          - '`job status --usage` prints the activity report, not the briefing'
          - '`job status --usage --since 7d` scopes to the last 7 days'
          - '`job status --usage <id>` scopes to the subtree'
          - '`job status --usage --format json` emits the json shape'
          - "`job status --since 7d` (no --usage) errors with a clear message"
          - '--since parsing is shared with `job log`, not duplicated'
      - title: Document `--usage` in the existing status docs
        desc: |
          Update the existing reference and observation docs pages for
          `job status` to describe the --usage mode, --since semantics,
          all-time default, velocity definition (done / calendar days), and
          zero-count omission. No new doc files. Mention in README only if
          the status command is already called out there.
        blockedBy: [usage-cli]
        criteria:
          - Docs describe --usage, --since, and the default all-time behavior
          - Docs state the velocity definition (done / calendar days) explicitly
```