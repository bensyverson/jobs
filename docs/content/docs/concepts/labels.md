---
title: Labels
weight: 6
---

Tasks carry free-form, flat **labels** — a simple slicing mechanism for filtering the frontier by area, priority, owner, or any other dimension you find useful.

Labels are local to each task. There is no inheritance, no namespacing, no schema. They're strings you attach.

## Adding and removing

```sh
job label add <id> <name> [<name>...]
job label remove <id> <name> [<name>...]
```

Both verbs are variadic, idempotent, and atomic. Adding a label that's already on the task is a no-op (no error). Removing one that isn't there is a no-op too. Multi-label calls run in a single transaction.

Labels can also be set at import time via the YAML `labels: [...]` key:

```yaml
- title: Ship v1
  labels: [release, p0]
```

## Filtering with labels

The frontier verbs all accept `-l, --label <name>`:

```sh
job ls --label release
job next --label release           # next available leaf carrying the label
job next all --label release       # whole frontier scoped to the label
```

`job ls --label <name>` is the workhorse for "show me everything in this slice." Composes with `--mine`, `--all`, `--since`, etc.

The web dashboard's filter chips are also keyed on labels — clicking a chip is the visual equivalent of `--label`.

## Where labels surface

- On `job show <id>` as a `Labels:` line.
- Inline in `job ls` output, in parentheses alongside blocker / claim annotations.
- As filter chips in the web dashboard.
- In the `/events` API, queryable via the `label` parameter.

## The `decision` convention

The label `decision` is a **convention**, not a feature flag. Tasks labeled `decision` represent questions that must be answered before work can proceed. The convention is honored in two places:

- `job status` (global and scoped) surfaces open `decision` tasks as `Decision: <id> "<title>"` lines, alongside `Next:` and `Stale:`. Pending human decisions become visible in the same briefing as available work.
- The dashboard's Decision strip pulls from the same query.

```sh
job add "Should we adopt the new auth library?" -l decision
```

Treat `decision` as a flag you attach when an agent has reached a fork it can't resolve unilaterally. The human notices via `job status`, makes the call, and removes the label (or just closes the task with a note). No new verbs, no new state machine — labels carrying the load.

This pattern composes with anything else you might label: `decision` plus `urgent`, `decision` plus `architecture`, `decision` plus the team area. The convention is one label among many.
