---
title: Web dashboard
weight: 6
---

`job serve` runs a read-only web UI for humans to watch agents work. It's a foreground process that binds `127.0.0.1:7823` by default and never accepts a write — the CLI is the only path that mutates the database. One exception: loading Plan or Issues can trigger the same read-time claim-expiry sweep the CLI runs, which records a `claim_expired` event attributed to the database's default identity rather than to a person clicking around.

<img src="../screenshots/home.png" alt="Jobs dashboard — Home view" width="860">

## Who it's for

The dashboard is for the human watching agents work. The CLI remains the surface agents reach for; the dashboard is the second monitor: ambient signal, no claims, no closes, no edits. Open it once at the start of a session, leave it visible, glance over when something changes.

## The five views

| View       | URL          | What it shows                                                                                  |
|------------|--------------|------------------------------------------------------------------------------------------------|
| Home       | `/`          | A progress chart (burn-up or activity over a range), the task map, recent completions, active claims, available and blocked work. |
| Plan       | `/plan`      | The task tree, scoped (`/plan/{id}`) or filtered by label (`?label=<name>`; `/labels/{name}` redirects there). Issue trees are not here. |
| Issues     | `/issues`    | The same tree view over your [issue trees](../concepts/tree-kinds/), scoped at `/issues/{root-id}`. |
| Actors     | `/actors`    | Column-per-actor board — one stack of cards per identity, freshest at the bottom, bounded by a range selector. Click through to `/actors/{name}` for a single actor's stream. |
| Log        | `/log`       | Linear stream of events, bounded by the same range selector and filterable by actor, label, type. |

Plan and Issues are one view split by tree kind: a plan and a bug pile are different shapes, so they get different tabs. The Issues tab carries the number of open issues after its label — nothing when that number is zero — and the page itself opens with `N open · M closed in 7d` beside the Active/Archived/All tabs.

Two things follow from the pile being a pile, and both apply on Issues only:

- **Archiving is per issue, not per pile.** An [issue root](../concepts/tree-kinds/) never closes on its own, so archiving whole roots the way Plan does would leave Issues' Archived tab empty forever. Instead the unit is each issue under the root: one whose whole subtree is closed moves to Archived, and the pile's own row stays on screen under every tab — an empty Archived tab reads as "nothing retired yet", not as an empty page. Plan still archives a root as a unit, closed children and all. A pile closed outright with `job done` on its root is archived as a unit, like a plan root.
- **An issue root shows no progress bar.** Branch rows carry an ambient `N of M tasks done` rollup; a pile has no denominator to be N of, so the root row omits it. Bugs *inside* an issue tree are ordinary decompositions and keep theirs, and the pile's throughput is the meta line above.

`/actors/{name}` opens one agent on its own: hero counters, an activity timeline, and that agent's events as the very same rows the Log renders — minus the actor column, which a page already scoped to one actor has no use for. The timeline covers the last 24 hours by default and the `24H · 7D · 30D` control in its header widens it (`?window=7d`, `?window=30d`), while the hero's `Done 24h` tile stays on the day whatever the timeline shows.

Number keys jump between the tabs in header order (`1` Home, `2` Plan, `3` Issues, `4` Actors, `5` Log); `` ` `` cycles forward through them and `~` cycles back.

Two auxiliary pages — `/tasks/{id}` (single task with peek view at `/tasks/{id}/peek`) and `/search` — round out the click paths but aren't usually entry points. The task page also carries the [found-in](../concepts/found-in/) reference in both directions: `Found in` links the task that surfaced this one, and `Surfaced` lists the issues this task produced; the peek sheet shows `Found in` only.

### Home's progress chart

Home opens with a **Progress** panel: how the work has gone over a range, rather than what happened in the last few minutes. It shows one chart at a time — `Burn-up · Activity` in its header switches between them (`?chart=activity`; the burn-up is the default).

- **The burn-up** draws two lines. *Scope* is the leaf tasks that exist and aren't canceled; *done* is the ones finished. The shaded gap between them is the open work, and the darker band inside it, sitting on the done line, is the part of that work that is blocked. The two end values are printed large beside the lines, and the header says it in words: `75 open · 12 blocked · 4 canceled`. Each point is the store's state *as of that moment* — so if a task is reopened, the done line dips, and it rises again when the task closes for good. Canceled work leaves scope instead of drawing a line of its own; its count is in the header. The vertical axis is **fitted**: it runs from the lowest value either line reaches in the range to the highest, on every range, `All` included — so a week in which scope went from 400 to 500 fills the chart rather than drawing two flat lines near the top of an axis that starts at zero. Read the gridline labels for the scale. A range in which nothing changed draws its line across the middle.
- **The activity histogram** is the other view, on the same time axis: one bar per bucket, stacked by tasks created, claimed, done and blocked, with the totals beside it. It keeps a zero baseline — its bars are counts.
- **Import ticks.** Each plan brought in with `job import` is a small tick on the time axis (hover it for the plan and its file) — the chart's only annotation. Stores from before import events existed simply show none.

The counting rules — leaves only, state as of each moment — belong to the core and are shared with `job stats`, so the chart and the CLI never disagree.

The panel has its own `1H · 1D · 7D · 14D · 30D · All` selector (`?range=`, default `1D`, so a new or freshly revived project shows movement on first load). Each range picks its natural bucket: a minute for 1H, an hour for 1D, six hours for 7D, a day for 14D and 30D, and for All whichever of those fits the store's history — so a day-old store draws hourly points, and one past 90 days draws weekly ones. `1H` is the old one-minute live view. Like every range selector here, the options — and the chart toggle — are plain links, so the chart works with JavaScript off; with it, switching range or chart redraws only the panel, in place, and keeps the URL in step. Each keeps the other's setting and any `?at=`.

### The range selector

The Actors board and the Log both open on the **last 7 days**. A `7D · 14D · 30D · All` control sits at the top of the view; picking one sets `?range=7d|14d|30d|all` and the page reloads — they are ordinary links, so the control works with JavaScript off and each range is a bookmarkable URL. An unrecognized value falls back to `7d` rather than erroring, and `7D` itself is the bare `/actors` or `/log`. Home's chart offers the same control with two shorter spans in front, `1H` and `1D`, and opens on `1D` instead (see above).

On **Actors** the range decides two things at once: an actor only gets a column when they have an event inside the window, and a column only carries the cards its in-window events produced. On a long-lived store that is the difference between a readable board and several hundred columns of agents who last ran in March.

On the **Log** it bounds the event list — "load older" pages back to the cutoff and stops — and it bounds the filter strips with it. The Actor and Label strips list only the actors and labels with an event inside the window, **most recently active first**, so the filters on offer are the ones this window can actually produce rows for. Widen to `All` when you want the whole history back.

Two details worth knowing:

- **A new actor still appears live.** An event from someone with no column or no chip adds one, whatever the range — the event just arrived, so it is inside every window.
- **The scrubber moves the window with it.** Parked in history (`?at=<position>`), the range is measured back from the moment you are parked at, not from now — so a 7-day view scrubbed to last month shows the week before *then*. On Home the chart is redrawn by the server for each position you scrub to, so the burn-up in history is exactly what the dashboard would have shown then. The `?at=` value is a **log position** (`<ts>-<replica>-<seq>`), the cursor the whole event log agrees on, so a bookmarked or shared history URL still lands on the same event after a `git pull` rebuilds the local cache.

### Capped chip strips

Each Log filter strip shows at most **24** chips and closes with a `+N more` chip. That chip is a link to the same page with `?chips=all`, which renders every chip in the window; with JavaScript it expands the strip in place instead, without a reload. Two rules keep the strip honest:

- The leading `any` chip is always first — clearing a filter is never behind the expander.
- **A filter you have selected always has a chip**, even when it falls outside the cap or outside the range. A filter in force with no chip is one you can neither see nor clear.

## Live updates

Every view subscribes to `/events` over Server-Sent Events. New events arrive in the open browser without a reload, and reconnects are seamless via `Last-Event-ID`. The same endpoint is documented as a public API on the [Machine interface](../machine-interface/http-api/) page — if you want to consume the live stream from your own tooling, that's the page to read.

## Running it

```sh
job serve                                 # 127.0.0.1:7823
job serve --bind 127.0.0.1:8080           # different loopback port
job serve --bind 0.0.0.0:7823             # expose on all interfaces (explicit opt-in)
job serve --db ./other.jobs.db            # serve a database other than ./.jobs.db
```

Worth knowing:

- **Loopback by default.** Reaching the dashboard from another machine requires `--bind 0.0.0.0:<port>` (or a specific interface). The default is local-only by design — there is no authentication on the HTTP surface.
- **Quiet port walk on the default.** When `7823` is busy and `--bind` was omitted, `serve` walks the next 20 ports upward and binds the first free one. Useful when you want a second window without thinking.
- **Loud failure on an explicit bind.** When `--bind` was passed and that port is busy, `serve` errors out — you asked for that port specifically.
- **`--db` follows the same global flag as the CLI.** Point at any `.jobs.db` file; the dashboard will serve whatever's in it. Stop with `Ctrl-C`.

For the full reference on `serve` and the global flags, see [Reference → Web](../reference/web/).
