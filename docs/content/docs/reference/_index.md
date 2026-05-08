---
title: Command reference
weight: 3
---

Every verb the `job` CLI understands, grouped by the role it plays in a session — the same grouping `job --help` uses, one level deeper. Each page lists the verbs in its group, the non-obvious flags worth knowing, and the combinations that make the verb pull its weight.

This is a reference, not a tutorial. For the walk-through path see [Getting started](../getting-started/); for the model behind the verbs see [Concepts](../concepts/).

{{< cards >}}
  {{< card link="setup" title="Setup" subtitle="`init`, `identity`, `schema` — bring a database into existence and decide who writes to it." >}}
  {{< card link="planning" title="Planning" subtitle="`add`, `import`, `edit`, `block`, `move`, `label`, `split` — shape the tree before work begins." >}}
  {{< card link="execution" title="Execution" subtitle="`claim`, `release`, `note`, `done`, `reopen`, `cancel`, `heartbeat` — the active-work loop." >}}
  {{< card link="observation" title="Observation" subtitle="`ls`, `show`, `log`, `status`, `next`, `tail` — read the tree without writing to it." >}}
  {{< card link="web" title="Web" subtitle="`serve` — the read-only browser dashboard." >}}
{{< /cards >}}

A few conventions hold across every verb:

- **Identity is global.** `--as <name>` is a top-level flag on every command; it can appear before or after the verb. See [Identity](../concepts/identity/).
- **Body input has three forms.** Anywhere a verb takes a free-text body (`note`, `done -m`, `cancel -m`, `release -m`), you can pass it positionally, with `-m "<text>"`, with `-m @<path>` to read from a file, or with `-m -` (or a positional `-`) to read from stdin. The shorthands compose with everything else on the line.
- **Reads accept `--format=json`.** Every observation verb and several writes (`done`, `cancel`, `claim --next`, `next`, `tail`) emit machine-parsable JSON when asked. Markdown is the default.
- **Writes are events.** Every state change appends to the event log; nothing is destructive except `cancel --purge`. See [The event log](../concepts/events/).
