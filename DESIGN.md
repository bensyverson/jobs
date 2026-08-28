---
version: alpha
name: Jobs
description: Design system for the Jobs web dashboard — a read-only, live observability view onto a hierarchical task store used primarily by AI agents. Local-first, desktop-first, dark-by-default.

colors:
  # Surface tiers — tonal layering, not shadows
  background:               '#0b1113'
  surface:                  '#11181a'
  surface-raised:           '#171f21'
  surface-popover:          '#1d262a'
  outline:                  '#2a3438'
  outline-strong:           '#3b474d'

  # Text
  on-surface:               '#e6ecea'
  on-surface-muted:         '#a8b4b2'
  on-surface-dim:           '#6f7b79'

  # Primary accent — chrome only (tabs, focus, primary buttons, heartbeat)
  primary:                  '#3cddc7'
  primary-dim:              '#1fa997'
  on-primary:               '#00201c'

  # Status — semantic; always paired with icon + text
  status-todo:              '#8b9a97'
  status-active:            '#3dd280'
  status-blocked:           '#e8b14a'
  status-done:              '#5a6967'

  # Signals — aging / stuck warnings; distinct from the formal blocked state
  signal-warn:              '#e8865c'
  signal-alert:             '#e84d4d'

  # Error — retained per spec recommendation (validation, connection loss)
  error:                    '#ff9b94'
  on-error:                 '#3e0000'

typography:
  display:
    fontFamily: system-ui
    fontSize: 28px
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: -0.02em
  heading-lg:
    fontFamily: system-ui
    fontSize: 20px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: -0.01em
  heading-md:
    fontFamily: system-ui
    fontSize: 15px
    fontWeight: 600
    lineHeight: 1.3
  body-md:
    fontFamily: system-ui
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.5
  body-sm:
    fontFamily: system-ui
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.4
  label-caps:
    fontFamily: system-ui
    fontSize: 11px
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: 0.06em
  data-md:
    fontFamily: ui-monospace
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.5
  data-sm:
    fontFamily: ui-monospace
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.4
  data-id:
    fontFamily: ui-monospace
    fontSize: 11px
    fontWeight: 500
    lineHeight: 1.3

rounded:
  none: 0
  sm: 3px
  md: 6px
  lg: 10px
  xl: 16px
  full: 9999px

spacing:
  unit: 4px
  xs: 4px
  sm: 8px
  md: 12px
  lg: 16px
  xl: 24px
  xxl: 40px
  gutter: 16px
  container-padding: 24px

# Extension: motion tokens (not in the base spec; preserved as domain-specific).
motion:
  duration-fast: 120ms
  duration-base: 200ms
  duration-slow: 320ms
  ease-out: cubic-bezier(0.2, 0.8, 0.2, 1)
  ease-in-out: cubic-bezier(0.4, 0, 0.2, 1)

components:
  # Avatars — the canonical actor primitive, five sizes
  avatar-dot:
    size: 6px
    rounded: '{rounded.full}'
  avatar-sm:
    size: 20px
    rounded: '{rounded.full}'
    typography: '{typography.data-id}'
  avatar-md:
    size: 24px
    rounded: '{rounded.full}'
    typography: '{typography.data-id}'
  avatar-lg:
    size: 32px
    rounded: '{rounded.full}'
    typography: '{typography.data-sm}'

  # Status pill — icon + text, 10%-opacity fill tinted by status color
  status-pill:
    rounded: '{rounded.full}'
    padding: 4px 10px
    typography: '{typography.label-caps}'

  # Signal card — home page; thin colored progress underline
  signal-card:
    backgroundColor: '{colors.surface}'
    rounded: '{rounded.lg}'
    padding: 20px
  signal-card-underline:
    height: 2px
    rounded: '{rounded.full}'

  # Graph node — 32px avatar disk, ring = status
  graph-node:
    size: 32px
    rounded: '{rounded.full}'

  # Peek sheet — slides from right, URL ?preview=<id>
  peek-sheet:
    backgroundColor: '{colors.surface-popover}'
    width: 440px
    padding: 24px

  # Scrubber — collapsed pill near footer, expands into full scrubber
  scrubber-pill:
    backgroundColor: '{colors.surface-raised}'
    rounded: '{rounded.full}'
    padding: 6px 12px
    typography: '{typography.label-caps}'

  # Buttons — read-only UI needs only two variants
  button-primary:
    backgroundColor: '{colors.primary}'
    textColor: '{colors.on-primary}'
    rounded: '{rounded.md}'
    padding: 8px 14px
    typography: '{typography.label-caps}'
  button-primary-hover:
    backgroundColor: '{colors.primary-dim}'
  button-ghost:
    backgroundColor: transparent
    textColor: '{colors.on-surface-muted}'
    rounded: '{rounded.md}'
    padding: 8px 14px
    typography: '{typography.label-caps}'
  button-ghost-hover:
    backgroundColor: '{colors.surface-raised}'
    textColor: '{colors.on-surface}'

  # Input (search)
  input:
    backgroundColor: '{colors.surface}'
    textColor: '{colors.on-surface}'
    rounded: '{rounded.md}'
    padding: 8px 12px
    typography: '{typography.body-md}'

  # Data row — dense tables in Active Claims, Recent Completions, Log
  data-row:
    height: 36px
    padding: 0 12px
    typography: '{typography.body-sm}'
  data-row-hover:
    backgroundColor: '{colors.surface-raised}'

  # ID pill — monospace short ID rendered as a compact chip
  id-pill:
    backgroundColor: '{colors.surface-raised}'
    textColor: '{colors.on-surface}'
    rounded: '{rounded.sm}'
    padding: 2px 6px
    typography: '{typography.data-id}'

  # Footer metric strip
  metric-strip:
    backgroundColor: '{colors.background}'
    height: 36px
    padding: 0 24px
    typography: '{typography.label-caps}'
---

## Overview

Jobs is a read-only live dashboard onto a hierarchical task store. The CLI serves agents; this dashboard serves the humans watching them. The design aims to be **focused and quietly polished** — a window onto work, not a tool for directing it.

The aesthetic is calm, precise, unobtrusive. Dense when you look at it, invisible when you don't. Typography carries rhythm, motion is fast and purposeful, color is reserved and earned. The interface should feel like a well-built native app: restrained surfaces, confident spacing, no decorative weight.

Dark mode is the default — this is a tool you leave open on a second monitor through a long session. Light mode exists but is not the primary target.

The personality is that of a precise instrument, not an enterprise dashboard. No "system status" chrome, no versioned runtime labels, no generic admin sidebars.

## Colors

Three color axes are kept independent and must not be confused:

1. **Chrome** — the single primary accent, a teal (`primary #3cddc7`). It drives active tab indicators, focus rings, primary button fills, the live-heartbeat pulse, and the selected-tab bar. Chrome never encodes semantic state.
2. **Status** — four semantic colors for `todo`, `active`, `blocked`, `done`. Always paired with an icon and a text label; never used alone.
3. **Actor identity** — deterministic hash-derived HSL, fixed at S 65% and L 55% for stable vibrance across hues. Used *only* inside avatar circles. Because an actor's hash may land on any hue (including green), status **always** occupies a different visual axis — the ring/outline around the avatar — never its fill.
4. **Label identity** — deterministic hash-derived HSL on the same hue space as actors, but with lower saturation (S 40%, L 50%) and rendered as a 15%-opacity fill with the full-chroma value as a 1px outline. Lower saturation keeps labels from competing visually with actor avatars on the same row. Like actors, the same label name always renders the same color everywhere.

A **signal palette** separates aging / stuck warnings from the formal blocked state:

- `signal-warn` (warm orange) — idle actors, oldest todos, longest claims, and any ambient progress bar approaching a threshold.
- `signal-alert` (red) — reserved for extreme cases; should appear rarely. If `signal-alert` is filling the screen, something is genuinely wrong.

**Surface tiers** are tonal, not shadowed:

- `background` — the canvas.
- `surface` — cards, panels, table backgrounds.
- `surface-raised` — hover and active rows, slightly elevated blocks.
- `surface-popover` — the peek sheet, popovers, menus. Only this level uses a shadow.

Each step is a small lightness increase against a cool-neutral base. Outlines (`outline`, `outline-strong`) are hairline borders that define structure without visual noise.

## Typography

A dual-font strategy distinguishes orchestration from payload, using **system fonts** so the dashboard feels native on every platform and the binary stays lean (no font embedding, no network fetch).

- **`system-ui`** (UI): all navigation, headings, body copy, labels, button text. Resolves to SF Pro on macOS, Segoe UI on Windows, platform default on Linux. Tracking is tightened at display and heading levels (−0.01 to −0.02em) for a controlled, grid-aligned feel.
- **`ui-monospace`** (data): task IDs, timestamps, event payloads, and the bodies of notes — which render as preformatted code blocks per the vision doc. Resolves to SF Mono on macOS, Consolas on Windows, platform default on Linux. A monospace is required here because tabular alignment and preserved whitespace matter.

`label-caps` uses letter-spaced sentence case, not shouty all-caps. Use full uppercase only for short anchor labels (single-word section headers); default to letter-spaced sentence case elsewhere.

Line height is generous (1.4–1.5) in body and data styles to support scanning long lists and multi-line notes. Heading line heights are tighter (1.2–1.3) for typographic density.

## Layout

The dashboard follows a **top-navigation-only** model. No sidebars. The structure is:

- **Header** (slim): tabs (Home / Plan / Actors / Log), global search (`/`-focus), theme toggle, notification bell.
- **Main** (flexible): view-specific content on a content-driven column layout. Home is three-panel; Plan is a single indented tree; Actors is column-per-actor with a timeline strip; Log is a single dense list.
- **Footer** (thin, persistent): metric strip, heartbeat, connection status, and the collapsed scrubber pill. Every view has this footer.

4px baseline rhythm governs all spacing. The spacing scale is deliberately small — a dashboard rarely needs anything above 40px.

- Container padding: 24px.
- Gutter between major blocks: 16px.
- Internal padding within dense components (table rows, signal card internals): 8–12px.

Information density is **dense but breathable**: tight internal padding within components, generous margins between blocks. Row heights hold to 32px or 36px depending on metadata weight. Wide viewports are the primary target; mobile degrades to a single-column status view.

## Elevation & Depth

Depth is achieved through **tonal layering** and **hairline outlines**. Shadows are reserved for true overlays.

- **Level 0 — canvas.** `background`. The page behind everything.
- **Level 1 — cards, panels.** `surface` with a 1px `outline` border. No shadow.
- **Level 2 — hover, raised rows, elevated sections.** `surface-raised`. No shadow; the lightness shift alone does the work.
- **Level 3 — peek sheet, popovers, menus.** `surface-popover` with a large-radius ambient shadow (`0 8px 32px rgba(0, 0, 0, 0.5)`). Level 3 is the only tier that floats over the view.

Hover on list rows and cards brightens the background one step (Level 1 → Level 2). Focus outlines the element with `primary` at 2px and a soft outer ring at 40% opacity.

## Shapes

The shape language is **soft-technical**: rounded enough to feel calm, sharp enough to feel engineered.

- `sm` (3px) — inline pills, chips, ID pills.
- `md` (6px) — buttons, inputs, most cards.
- `lg` (10px) — signal cards, major panels.
- `xl` (16px) — reserved for prominent feature containers; used sparingly.
- `full` — avatars at every size, the collapsed scrubber pill, status pills, and the selected-tab underline cap.

A single view should not mix `md` and `xl` radii. `sm` may accompany `md` for small details inside a `md` container (an ID pill inside a button-sized card, for example).

## Components

**Avatars — the canonical actor primitive.** Every representation of an actor in the UI is a size of the same atom: a circular disk filled with the actor's hash color, containing the first letter of the actor's name in white. Five sizes:

- `avatar-dot` (6px) — inline attribution in tight spaces.
- `avatar-sm` (20px) — next to names in tables, logs, event rows.
- `avatar-md` (24px) — inline with names at body size.
- `avatar-lg` (32px) — column headers in the Actors view and graph nodes.
- **Avatar stack** — 8px overlap when multiple actors have touched a task.

Never render an actor as a naked name; always include at least the dot form.

**Status pills.** Icon (inline SVG) + short text ("Active", "Blocked", "Done", "Todo") + a 10%-opacity fill tinted by the status color and a 1px border of the same color at full opacity. Typography is `label-caps`. Used on every task row, task card, and task detail.

**Signal card.** The four home-page cards (activity histogram, newly-blocked, longest-claim, oldest-todo). Internal layout: icon + uppercase label + `display` value + `body-sm` context line, with the context line pinned to the bottom so cards align flush across the grid regardless of value height. A 2px colored underline (`signal-card-underline`) sits at the bottom of the card, tinted by the card's signal color (`signal-warn`, `signal-alert`, or `primary`). The underline is also a progress bar: it fills as the metric approaches its threshold. Ambient — you don't notice it on first read — but it adds a second dimension of information without chrome.

**Activity histogram.** Occupies the first signal card (replaced "Idle actors" — idleness isn't meaningful when agents come and go). 60 bars, one per minute over the last hour, each stacked by event type (`created` / `claimed` / `done` / `blocked`) using the status palette. Bar height is the minute's total event count normalized to the max minute in the window; segments within each bar are `flex: N` weighted by per-type count. The card's context line carries a swatch-keyed legend, each legend item a no-wrap unit so swatch and label never break mid-phrase. Empty state: a single flat 1px rule and "No events in the last hour."

**Graph node.** The 32px avatar disk reused as a graph node. Two independent axes:

- **Fill** = identity. Actor color + letter for claimed/active nodes. Neutral surface color for unclaimed. Muted gray for done (optionally with a subtle check glyph).
- **Ring** = status. A 2px outline colored by status. Selected nodes also carry a monospace ID pill immediately below; unselected labels appear on hover.

Edges: solid curves for parent/child, dashed curves for blocker relationships (overlay arcs on top of the tree layout). Graph direction is left-to-right; dependency flows rightward.

**Peek sheet.** Slides from the right at 440px wide at Level 3 elevation. URL state: `?preview=<task-id>`. Reloading the URL reopens the sheet. Escape key or clicking the dimmed overlay closes. The sheet contains the task's status, labels, notes (as code blocks), blockers, blocked-by, event history, and a notification bell. A prominent "Open full page" link navigates to `/tasks/<id>` for the full view.

**Scrubber pill.** Collapsed by default as a small `full`-radius pill in the footer showing `● Live` (small `status-active` dot + label). Clicking expands it into the full scrubber strip (see below), and the `?at=<event>` URL parameter pins the view to that event.

**Scrubber strip (expanded).** Full-width bar above the footer. Contains a meta line (`Scrubbing` + `?at=` event id + `Ns ago` + a hint on gestures), a 24-hour density track (one bar per 15-minute bucket, height proportional to event count, bar color `outline-strong`), a time axis (24h / 18h / 12h / 6h / now), and a `signal-warn` cursor line with a round grip positioned via `--x`. A Level-3 popover tooltip above the cursor shows wall-clock + `N ago` + actor avatar + verb + id-pill + title for the event at the cursor. **The cursor is the only selection** — a scrubbed view is always a single point in time; zoom is purely a viewport gesture (⌘-scroll / keyboard) and does not create a range. A small `status-active` outline pill labelled `● Return to live` floats right in both the strip's meta line and the history banner, keeping the escape hatch in eye-view at both ends of the screen.

**History banner.** One-line amber strip below the header whenever `?at=` is set. `signal-warn` pulse dot + `Viewing history` (label-caps) + `?at=<event> · N ago · wall-clock` in mono + spacer + the `● Return to live` pill at the right edge. The banner is the "you are not live" affordance; the strip is how you navigate within history.

**Buttons.** Two variants only.

- **Primary**: solid `primary` fill, `on-primary` text. Reserved for singular affordances where they exist.
- **Ghost**: transparent fill, muted text; hover brightens to `surface-raised` with full-color text. Use for secondary actions (theme toggle, dismiss, navigation affordances).

No tertiary, no destructive, no large/small variants unless a view genuinely needs them. This is a read-only UI; button surface area is intentionally minimal.

**Input (search).** The global search field sits in the header. Transparent background, hairline bottom border. Focus: border transitions to `primary` with a soft 2px outer ring. Placeholder text uses `on-surface-dim`. The "/" keyboard shortcut focuses it from anywhere.

**Data rows.** 36px tall, 12px horizontal padding. Monospace (`data-sm`) for IDs, timestamps, and numeric values; sans (`body-sm`) for titles, actor names, and labels. Hover state uses `surface-raised`. Selected rows carry a 3px vertical `primary` bar on the far left.

**Log row.** Single-column chronological event row for the Log view. Grid columns: time (mono, right-aligned) · actor (avatar + name) · verb · id-pill · title + optional note. Verb is `label-caps` colored by event type — `created` uses primary, `claimed` / `unblocked` use `status-active`, `done` / `released` / `canceled` use `text-done`, `blocked` uses `status-blocked`, `noted` uses `on-surface-dim`. The row also takes a `c-log-row--<verb>` modifier that tints the *row's* title + id-pill using the same text-state tokens as Plan mode — so a glance down the feed reads as the task's state-change history without having to parse each verb. Actor column is tight (90–120px) so the verb sits close to the actor label. Most-recent rows carry a `c-log-row--new` modifier with a slide-in keyframe.

**Filter bar + filter chip.** Stacked rows of chips above a list view. Each row is one filter axis (Events / Actor / Label) with a right-aligned `label-caps` label at a fixed min-width so axis labels align vertically. Chips are small `full`-radius pills — transparent fill, hairline `outline` border, `label-caps` text. Active state uses a 1px `primary` border + 10% primary fill + `primary` text. Label chips can carry an actor dot (inline `avatar-dot`) for per-actor filter rows. Chips wrap within their row; whole axes never break across rows.

**Row-link (stretched link).** Pattern used across Home panel rows, Log rows, and Actor cards: one anchor element (`c-row-link`) positioned `inset: 0` at `z-index: 1` turns the entire row/card into a single link to the task without nested `<a>`. Any interactive child (actor avatar, blocker pill) bumps to `z-index: 2` so it keeps its own click target. Cmd/middle/right-click all work because the overlay is a real anchor. Text selection is the known tradeoff — the row is a *navigation surface*, not a readable transcript.

**ID pill.** A compact monospace chip used wherever a 5-character task ID appears. `surface-raised` background, `data-id` typography, `sm` radius. Auto-links to the task via the peek sheet.

**Task page issue variant (`p-task--issue`).** The task page's root element is `.p-task`. When the task's *root* is an issue-tree, it also carries `.p-task--issue`. The variant is deliberately thin: it adds one `label-caps` kind marker — the word `issue` in `on-surface-dim` — immediately after the short id in the header row, and changes nothing structural. There is one task route (`/tasks/{id}`) for both kinds, so this marker is the whole visual difference; kind is a property of the root, so a child of an issue root carries the variant too. Word, not color — an issue must still be legible in greyscale.

**Label/tag pill.** Same shape as the ID pill but with `body-sm` typography. Deterministically colored per the label-identity rule in §Colors: a 15%-opacity fill and a full-chroma 1px outline in the label's hashed hue. Lower saturation than actor avatars so labels read as supporting metadata, not identity.

**Footer metric strip.** Thin horizontal bar (36px tall), persistent across every view. Left: metric cluster (active actors, WIP, events/min, throughput). Center: the scrubber pill. Right: heartbeat (small pulse dot + "last event Ns ago") + connection status (SSE connected / reconnecting / offline). The metric strip is the single place raw counts live — home-page cards carry signals, not restatements of these numbers.

**Notification bell.** Icon button in the header. On a task's peek sheet or detail page, the bell toggles a per-tab browser-notification subscription for that task's completion. Toggled state uses `primary` fill.

**Favicon.** Dynamic and readable at 16px. Idle = monochrome dot on background. Active = `primary` pulse. Stuck = `signal-warn` tint. Supports the pinned-tab use case.

**Actor board (Actors view).** Horizontally-scrolling board of fixed-width actor columns (~320px). Each column is a tonal card with a non-scrolling header (`avatar-lg` + name + one-line status: claim count + last-seen) and a vertically-scrolling stream below. `flex-direction: column-reverse` on the stream anchors the viewport to the bottom: current claims dock at the bottom, history scrolls up off-screen. Idle actors (`c-actor-col--idle`) fade to 0.72 opacity. The board scrolls horizontally with `scroll-snap-type: x proximity` when there are more actors than columns fit.

**Actor card (Trello-style task card).** One card per `(actor, task)` pair — **not** per event. The card's tint + verb reflect the latest *state-changing* event for that actor on that task; notes collapse into an inline `· N notes` badge on the parent card rather than duplicating it. Card tints: `claimed` / `blocked` use the status tokens and add a matching tinted border; `created` uses a `primary` 35% tint for net-new tasks; `done` / `released` / `canceled` / `noted` use the default `on-surface` so completion cards read as real content rather than faded noise. Layout: meta row (verb + optional notes badge + timestamp, timestamp right-aligned via `margin-left: auto`), title row (id-pill + task title, title truncates with ellipsis), 2-line clamped description.

Each column is the actor's own little world — the same task can appear in two columns if two actors have touched it, each showing its own relationship to the task. Not an event log; the Log view owns that.

**Timeline strip (single-actor view).** Per-lane event-density visualization on the expanded single-actor view. Five lanes (created / claimed / done / blocked / noted), each a 14px track; colored marks at `--x` percent of the 24h window. Complements the scrubber's cross-actor density strip with a per-actor read.

**Tabs (top nav).** `label-caps` typography. Active tab shows a 2px `primary` underline with a `full`-radius cap. Inactive tabs use `on-surface-muted` text; hover brings them to `on-surface`.

## Do's and Don'ts

- **Do** layer depth tonally. Reserve shadows for Level 3.
- **Do** pair every status color with an icon and a word.
- **Do** treat the avatar circle as the canonical actor primitive at every scale.
- **Do** keep fill and ring on separate semantic axes in graph nodes: fill = who, ring = what state.
- **Do** render all notes as code blocks (monospace, preserved whitespace).
- **Do** keep the footer thin and persistent on every view — it is the "is it alive" affordance.
- **Do** use the accent sparingly. A screen should rarely show more than a handful of `primary` instances.
- **Do** use LTR flow for graph views and TTB indented flow for the plan view. The difference reinforces what each view is *for*.

- **Don't** introduce sidebars. Top-nav only.
- **Don't** add write actions — no create, edit, claim, close, or new-job buttons anywhere. The dashboard is read-only.
- **Don't** mix `md` and `xl` corner radii in the same view.
- **Don't** let `status-blocked` amber and `signal-warn` orange sit adjacent. They are distinct semantic categories and must not read as synonyms.
- **Don't** encode meaning in color alone. Every color-coded state needs a glyph or a word.
- **Don't** use decorative motion. Transitions exist to confirm state changes — fast, ease-out, never longer than `duration-slow`.
- **Don't** use shadows to layer Level 1 or 2 surfaces.
- **Don't** default headings to full uppercase. `label-caps` is letter-spaced sentence case; reserve full uppercase for single-word anchors.
- **Don't** duplicate raw metrics between the header and the footer. Home-page cards show *signals*; the footer strip owns the counts.
- **Don't** version the UI with "v2.4.0-stable" chrome or label the dashboard with "SYSTEM OK" status. This is not a monitoring console.
