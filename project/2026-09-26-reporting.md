# Reporting: a burn-up series, `job stats`, and a Home chart panel

*2026-09-26. Filed as root `LfUoov`. Discussed with Ben. Replaces the two 2026-09-26 backlog entries ("Burn-up chart in the web UI" and "`job stats` or `job status --stats`").*

## Why

A client project's weekly report carried a burn-up chart (opened vs finished, weekly, 25 May – 4 Sep) that did more to show progress than anything the dashboard shows. It was built by an ad-hoc SQL query against `.jobs.db` — and the first cut was wrong: it summed every `done` event, so reopen→close cycles counted twice and the finished line ended at 1,080 instead of 1,070. Only a cross-check against `job status --usage` caught it.

That is the case for this work: **the counting rules belong in `internal/job`, defined once**, and every consumer — the CLI, the dashboard, timetattle — reads them rather than re-deriving them.

Meanwhile the Home view's top bentos (newly blocked, longest active claim, oldest todo) are about the last few minutes: they make the page look alive but say nothing about progress. The burn-up replaces them.

## Decisions

1. **The unit is a leaf.** A task counts while it has no children, issue leaves included. Parents are bookkeeping — they auto-close when their last child does — so counting them inflates every figure after a plan import. Issue roots never count. Leaf-ness is evaluated *as of t*: splitting a leaf retires it from the count and its children join from that moment.
2. **Every point is the store's state as of t.** Each sample is what `job status` would have said at that moment, computed by replaying events. Reopen→close cycles cannot double-count, and the chart agrees with the history scrubber by construction. The cost, accepted: the done line dips when a task is reopened and rises again when it closes for good.
3. **The chart is two lines and a shaded gap.** *Scope* = leaves that exist and are not canceled; *done* = leaves done. The gap is open work, with the blocked share drawn darker inside it. Canceled work leaves scope rather than drawing a third line; its count is reported alongside.
4. **Root velocity is reported separately.** Plans (non-issue roots) closed per bucket, and median import→close time.
5. **`job import` records an `imported` event** on each root it creates (and on the `--parent` target when nesting). It is what "how many plans have we imported?" counts, and it is what import→close time starts from. **No backfill**: existing stores have no such events, and figures that depend on them say so rather than guessing.

   > **Correction (2026-09-26, before implementation):** the event goes on each *top-level task the import creates*, whether it lands at the forest root or under `--parent` — not on the `--parent` target. The imported plan is what closes, so that is where import→close time must be measured. The payload names the source file (base name only), the parent if nested, and the plan's task and leaf counts.
6. **The chart's only annotations are import markers** — a tick on the timeline for each `imported` event in range. No hand-placed markers.
7. **`job stats` replaces `job status --usage`.** `status` is "what's happening now"; `stats` is "how has it gone". Pre-launch, so `--usage` is removed, not aliased.
8. **timetattle is the named external consumer**, via `job stats --format=json`, not a Go import. The JSON carries a `schema` version and a JSON Schema published on the docs site, like the plan grammar's. The timetattle side is its own work, filed in that repo.
9. **Done-by-actor is reported, with a caveat.** Nearly every close is performed by an agent, including ones running under a human's identity, so the split measures identity hygiene more than who did the work. It goes in the stats output, not on the chart.
10. **One range selector drives both Home charts** (burn-up and activity histogram): 1H · 1D · 7D · 14D · 30D · All, `?range=`. It extends the shared selector from `2026-08-28-dashboard-bounds.md` and keeps its 7D default, so every bounded view opens on the same span. (The discussion floated 14D; 7D won on consistency with the existing views.) 1H keeps today's one-minute live histogram reachable. The window ends at the scrubber cursor, not at wall-clock now.
11. **The range vocabulary and the bucket choice move into `internal/job`.** The web handlers hold the selector today (`internal/web/handlers/range.go`), but `job stats --since 7d` must produce the same buckets, and an adapter that holds a decision is a bug. Buckets aim for roughly 24–60 per window: 1H → 1 minute, 1D → 1 hour, 7D → 6 hours, 14D and 30D → 1 day, All → 1 day up to 90 days of history, else 1 week. `--by` overrides it on the CLI.
12. **Under the scrubber, the charts are a server fragment, not a JS twin.** The Home scrubber rebuilds its cards client-side (`home-scrub-build.mjs`); a JS port of the burn-up would be a second definition of the counting rules — the exact failure this work exists to prevent. The scrubbed view fetches the chart panel for `(range, at)` from the server instead.
13. **Days are local.** Bucket boundaries use the local time zone with weeks starting Monday, matching timetattle so the two tools' days line up; `--timezone` overrides.

## Headline figures in `job stats`

For the window (default all-time; optional task id to scope to a subtree):

- leaves: created, done, canceled, open now, blocked now
- plans: imported, closed, open roots, median import→close
- throughput: leaves done per week; median created→done and claim→done
- done by actor (see decision 9)
- a compact text burn-up (block or braille characters, fits the terminal width)

`--format=json` carries all of the above plus the full sample series; `--format=csv` emits the series in long form, one row per bucket.

## Open for measurement

Replaying the whole log per render is the simple design. Before choosing between that and a cache, time it on the largest real store we have and record the command and figure on the library leaf.

## Plan

```yaml
tasks:
  - title: Reporting
    desc: |
      A burn-up series and headline stats computed once in internal/job, surfaced as `job stats` (text, JSON, CSV) and as a chart panel on the dashboard's Home view. Spec, and the decisions each leaf rests on: project/2026-09-26-reporting.md — read it before starting any leaf.
    labels: [reporting]
    children:
      - title: Record an imported event
        ref: imported
        desc: |
          `job import` records an `imported` event on each root it creates, and on the `--parent` target when nesting, with a payload naming the imported root ids and the leaf count. Add the payload type in internal/job/event_payloads.go, render it in `job log`/`tail`, and add the type to the dashboard's SSE subscription list (TestLiveSubscribesToEveryEmittedEventType will fail until you do). No backfill of existing stores.
        labels: [reporting, cli]
        criteria:
          - job import records one imported event per created root, with its payload
          - A --parent import records the event on the parent
          - job log renders the imported event legibly
          - The dashboard's live list subscribes to imported
      - title: Move the range vocabulary and bucket choice into internal/job
        ref: range
        desc: |
          Move RangeKey, its durations and parsing from internal/web/handlers/range.go into internal/job; add 1h and 1d keys; add the bucket-size function from decision 11 (target 24–60 buckets per window, All switches from day to week past 90 days of history). The handlers keep the tab rendering and consume the core types. The Actors board and the Log keep their current tab lists — the per-view list of offered keys is the handler's.
        labels: [reporting]
        criteria:
          - handlers/range.go holds no durations or bucket decisions
          - 1h and 1d parse; unknown keys fall back to the default
          - Bucket size per key matches decision 11, with a test per key
          - Actors and Log selectors are unchanged
      - title: Burn-up series and headline stats in internal/job
        ref: series
        blockedBy: [imported, range]
        desc: |
          The library core. A function over (db, scope subtree or forest, window [since, until], bucket, timezone) returning typed samples — bucket end, scope, done, canceled, open, blocked — per decisions 1–3, by replaying events so each sample is the state as of that instant. Plus the headline figures listed in the doc (leaves, plans, throughput, cycle times, done by actor, import markers) and per-bucket activity counts by event type for the histogram. Test-first with hand-built stores covering: a reopen dip, a split leaf, a canceled leaf, a nested import, an issue leaf, an issue root excluded, a blocked leaf, empty store. Time it on the largest real store available (read-only, copied to the scratchpad) and record the command and figure in a note.
        labels: [reporting]
        criteria:
          - A reopened-then-closed leaf dips and recovers and is never counted twice
          - Parents and issue roots never count; issue leaves do
          - Splitting a leaf retires it and admits its children at the split
          - Canceled leaves leave scope and are counted separately
          - Plan closes and median import-to-close come from imported events
          - Buckets respect local time and Monday week starts
          - Replay time on a large real store is recorded with the command that measured it
      - title: job stats replaces job status --usage
        ref: cli
        blockedBy: [series]
        desc: |
          New `cmd/job/stats.go`: `job stats [id] [--since] [--until] [--by hour|day|week] [--timezone] [--format md|json|csv]`, a thin consumer of the series and headline stats. Text output leads with the headline figures and a compact burn-up that fits the terminal width. JSON carries a `schema` version, the figures, and the full series; CSV is the series in long form. Generate a JSON Schema for the stats output alongside the plan grammar's. Remove `--usage` and `--since` from `job status`, moving or retiring their tests (explain each in the report).
        labels: [reporting, cli]
        criteria:
          - job stats prints headline figures and a text burn-up
          - --format=json carries a schema version, the figures and the series
          - --format=csv emits one row per bucket
          - A JSON Schema for the stats output is generated and checked in
          - job status --usage is an unknown-flag error
      - title: Home chart panel with range selector
        ref: web
        blockedBy: [series]
        desc: |
          Replace the Home view's NewlyBlocked, LongestClaim and OldestTodo bentos with a chart panel: the burn-up (scope and done lines, shaded gap with the blocked share darker, import markers) and the activity histogram, both driven by one 1H·1D·7D·14D·30D·All `?range=` selector (default 7D). Server-rendered SVG from DESIGN.md tokens, light and dark; the panel works without JS and JS swaps it in place on selection. Under the scrubber the panel is fetched from the server for (range, cursor) — no JS port of the counting rules; delete the retired cards from home-scrub-build/render and signals.go. Each chart carries an accessible title and summary and a data table for assistive tech. Preview catalog states: empty store, a single day, a reopen dip, crowded history, mostly canceled. Contact sheet with sleepy in both schemes; one Blink check at the end.
        labels: [reporting, web]
        criteria:
          - The three retired bentos are gone from Go, JS and templates
          - The range selector drives both charts and works without JS
          - Scrubbing redraws the panel at the cursor from the server
          - Preview states render in light and dark
          - make test-js passes
      - title: Document reporting
        blockedBy: [cli, web]
        desc: |
          docs/content/docs: a reference page for `job stats` with the counting rules stated plainly (leaves only, state as of t, canceled leaves scope, no import backfill), the JSON Schema published beside the plan grammar's, `status --usage` removed from the status reference, and the Home panel in web-dashboard.md. DESIGN.md gains the chart panel's component spec. DOCS.md follows while it exists.
        labels: [reporting, docs]
        criteria:
          - The counting rules are stated on the stats reference page
          - No doc mentions status --usage
          - web-dashboard.md and DESIGN.md describe the chart panel
```
