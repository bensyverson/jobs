---
title: Concepts
weight: 2
---

The ten ideas that make Jobs work. Each page is a tight reference, not a tutorial — read top-to-bottom for the full mental model, or jump to whichever concept brought you here.

{{< cards >}}
  {{< card link="identity" title="Identity" subtitle="`--as`, default identity, strict mode, attribution." >}}
  {{< card link="leaves-and-claims" title="Leaves and claims" subtitle="Leaf-frontier semantics, claim TTL, auto-extend, auto-release, auto-close." >}}
  {{< card link="criteria" title="Acceptance criteria" subtitle="Lifecycle, short ids, `--criterion` / `--all-passed` / `--force-close-with-pending`." >}}
  {{< card link="blockers" title="Blockers" subtitle="`block add`/`remove`, cycle detection, auto-unblock on done." >}}
  {{< card link="found-in" title="Found-in" subtitle="Provenance without blocking: `job found-in`, `add --found-in`." >}}
  {{< card link="labels" title="Labels" subtitle="Free-form labels and the `decision` convention." >}}
  {{< card link="tree-kinds" title="Tree kinds" subtitle="Task-trees vs issue-trees, `job kind`, and the `--issues` readers." >}}
  {{< card link="events" title="The event log" subtitle="Append-only, replayable, the source of truth behind every other surface." >}}
  {{< card link="the-store" title="The store" subtitle="`.jobs/log` is the record, `.jobs.db` is a cache: what to commit, and how two machines merge." >}}
  {{< card link="prose" title="Descriptions and notes" subtitle="Markdown paragraph rules: hard-wrapped text reflows, lists and fences survive." >}}
{{< /cards >}}

Once you have the model, [Recipes](../recipes/) shows the patterns these primitives compose into.
