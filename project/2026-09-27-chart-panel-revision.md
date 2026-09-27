# Chart panel revision: resolution, side-by-side charts, hover and motion

*2026-09-27. Filed as root `6lCfgP`. Discussed with Ben after review of the landed panel (root `LfUoov`, `project/2026-09-26-reporting.md`). Several decisions below correct that doc; each correction is also marked there in place.*

## Why

The first cut of the Home chart panel works, and review turned up four families of problems:

- **Fidelity.** The burn-up drew one point per activity bucket, because the series and the histogram shared a bucket (decision 11 of the reporting doc). 14D had 14 points and visibly lost detail against 7D's 28; every range looked faceted.
- **Reading the chart.** The dark band on the done line is the *blocked* share of open work, but it was drawn gray and read as canceled; canceled work was not drawn at all. The axis had five labels, no tick marks, and no way to tell where the window started or stopped. The end labels ("358 scope / 341 done") were absolute totals on every range, so 1D and 7D said the same thing. The import tick had a tooltip no pointer could reach.
- **Layout.** At full width the vertical movement is hard to read, and a fixed 112px end-label column left dead space on the right.
- **No enhancement.** Nothing happens on hover, and a range switch is a hard swap.

## Decisions

1. **The burn-up and the histogram get separate resolutions.** The report carries a fine *trace* for drawing alongside the per-bucket `Series`/`Activity`, from one replay. The trace starts with a sample at `Since` (so window-relative deltas have a baseline) and ends at `Until`, at roughly 300 samples: 1H every minute, 1D every 5 minutes, 7D every 30 minutes, 14D hourly, 30D every 2 hours, All by span. It is opt-in on `ReportQuery` — the dashboard asks for it; `job stats` output does not carry it.
2. **Histogram bars per range:** 1H → 5 minutes (12 bars), 1D → 1 hour (24), 7D → 6 hours (28), 14D → 12 hours (28), 30D → 1 day (30), All → the unit that lands about 25–30 bars. This adds 5-minute and 12-hour buckets and replaces the bands in `BucketForSpan`; `job stats` buckets the same way (decision 11 of the reporting doc still holds: one rule, in `internal/job`).

   > **Correction (2026-09-27, at integration of `SAwXI3`):** "about 25–30 bars" for All cannot be met with calendar units — neighbours differ ×6 to ×12. The rule that shipped is the narrowest unit keeping a span to at most 45 bars (`BucketForSpan`); it lands every named key exactly as above and typical histories at 25–30, but a history just past 45 days goes weekly (about 7 bars), and one past 45 weeks draws more than 45 bars, there being no month bucket.
3. **Burn-up and activity sit side by side**, each half the panel, under one range selector. The Burn-up · Activity toggle and `?chart=` are removed. Below 720px they stack, burn-up first. The plots grow to about 140px tall.
4. **The headline figures are the window's**: the burn-up's end labels read **+N created** and **+N done** (`LeafFigures.Created` / `.Done`, already window transitions), with the absolute total beneath in small type ("of 358"). "Scope" is renamed; the line is still scope, the label is the window's creations. The caption's canceled count is the window's too.
5. **Blocked takes `--color-status-blocked`**, the same as the histogram's blocked segment.
6. **Canceled is drawn as a band on top of the scope line** — scope that was cut — measured from zero at `Since` (canceled in the window only), in a canceled color added as a token in DESIGN.md. The y-domain includes it.
7. **The axis gets minor tick marks and labelled edges.** Unlabelled marks at 5 or 10 minutes (1H), 1 hour (1D), 1 day (7D, 14D), 1 week (30D); labels on the subset that fits; the left edge labels its moment and the right edge reads "Now" live or the cursor's time under the scrubber.
8. **Import ticks are links** with a real hit target: hover names the plan, click opens it in the peek sheet.
9. **Hover and motion are progressive enhancement.** The server still renders the whole panel (no-JS, preview catalog, goldens); the fragment also carries the trace, the buckets and the imports as a JSON island. `<chart-panel>` uses it for a crosshair spanning both charts that updates the end figures to the state at that moment, a tooltip of that slice's events (created, claimed, done, blocked), and a Plerk-style animated range switch — x and y scales interpolated over the data, gridlines held still, instant under `prefers-reduced-motion`. Live refreshes and scrubber moves swap without animating. Touch: drag moves the crosshair, release clears it. This does not breach reporting decision 12: the browser draws server-counted samples and re-derives no count. It does mean JS draws the same geometry the Go layout draws; a shared fixture keeps the two from drifting.

10. **The histogram counts what the labels count.** *(Added 2026-09-27, after review of the side-by-side panel.)* Its created and done segments are the window's `LeafFigures` transitions attributed to the bucket they fell in: leaves (as of Until) created there, and leaves done at Until whose final close fell there. Its claimed and blocked segments count those events on the same leaves. So the legend's totals equal the burn-up's "+N created" and "+N done" by construction; parents, and reopen→close cycles, no longer inflate the histogram. `job stats` shares the rule.

## Plan

```yaml
tasks:
  - title: Chart panel revision
    desc: |
      Act on project/2026-09-27-chart-panel-revision.md: separate
      resolutions for burn-up and histogram, side-by-side charts,
      window-relative figures, blocked/canceled colours, a real axis,
      and progressive-enhancement hover and motion.
    labels: [reporting, web]
    children:
      - title: Fine burn-up trace and per-range histogram buckets in internal/job
        ref: core
        labels: [reporting]
        desc: |
          Decisions 1 and 2. Add 5-minute and 12-hour buckets; rework
          BucketFor / BucketForSpan for the bar counts in decision 2; add an
          opt-in fine trace (Report.Trace, sample at Since through Until,
          ~300 samples at the per-range steps in decision 1) computed in the
          same replay. job stats --by accepts the new buckets. Update the
          reporting doc's decision 11 correction and the stats reference.
        criteria:
          - Each range key's activity bucket and bar count matches decision 2, one test per key
          - All picks a unit landing about 25-30 bars across short, medium and long histories
          - Trace is empty unless requested, starts at Since and ends at Until
          - Trace samples agree with Series samples at shared instants
          - job stats --by accepts 5m and 12h; docs updated
      - title: Side-by-side panel, axis, labels and colours
        ref: layout
        labels: [web]
        desc: |
          Decisions 3, 4, 5, 7, 8 and the non-trace parts of the layout.
          Remove the toggle and ?chart=; burn-up and activity side by side
          (stacked under 720px); taller plots; end-label column sized to its
          content; +N created / +N done with "of N" totals and more space
          between the blocks; blocked in the status colour; minor axis ticks
          and labelled edges ("Now" or the cursor time); import ticks as
          peek links. Preview states, DESIGN.md spec and web-dashboard.md
          follow. Contact sheet in both schemes.
        criteria:
          - No ?chart= toggle; both charts render, stacked under 720px
          - End labels are window deltas with absolute totals beneath
          - Axis has minor ticks per decision 7 and labelled start and end
          - Import ticks open the imported task in the peek sheet
          - Blocked band uses --color-status-blocked
          - Preview catalog, DESIGN.md and docs updated; sleepy contact sheet reviewed
      - title: Draw the burn-up from the fine trace, with the canceled band
        ref: trace
        blockedBy: [core, layout]
        labels: [web]
        desc: |
          Decisions 1 and 6 on the web side: LayoutBurnup reads Report.Trace;
          canceled-in-window drawn as a band above scope in a new canceled
          token; the y-domain includes it; the fragment carries a JSON island
          (trace, buckets, imports, window) for the enhancement leaf.
        criteria:
          - Burn-up draws from the trace; 14D is as smooth as 7D
          - Canceled band starts at zero at Since and sits above scope
          - JSON island carries everything the hover needs, typed on the Go side
      - title: Hover crosshair, slice tooltip and animated range switch
        ref: hover
        blockedBy: [trace]
        labels: [web]
        desc: |
          Decision 9. Crosshair across both charts, end figures follow the
          cursor, slice tooltip, Plerk-style animated range switch (see
          ../plerk/internal/web/static/chart.js), reduced motion instant,
          touch drag. JS tests under internal/web/jstest, including a parity
          fixture against the Go layout. Browser-checked with sleepy and once
          in Chrome for Testing.
        criteria:
          - Crosshair spans both charts and updates the figures to that moment
          - Slice tooltip lists that bucket's created, claimed, done and blocked
          - Range switch animates; reduced motion switches instantly
          - Parity test pins JS geometry to the Go layout
          - make test-js passes; sleepy and one Blink check done
```
