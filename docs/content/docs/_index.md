---
title: Documentation
weight: 1
---

Welcome to the Jobs documentation. Jobs is a hierarchical task tracker for the CLI, backed by a git-native event log, designed for agents and observed by humans.

<img src="../screenshots/home.png" alt="Jobs dashboard — Home view" width="860">

The site is organized as a depth source for `job --help`. If you've already run `job init` and want to learn the system by using it, start with **Getting started**. If you're looking for a specific verb or flag, jump to the **Command reference**. If you're an agent reading this for orientation, **Concepts** is the shortest path to a complete mental model.

{{< cards >}}
  {{< card link="getting-started" title="Getting started" subtitle="Install, initialize, walk a plan from author to done, and carry it to a second machine." >}}
  {{< card link="concepts" title="Concepts" subtitle="Identity, leaves and claims, criteria, blockers, labels, events, the store." >}}
  {{< card link="reference" title="Command reference" subtitle="Every verb, grouped by role — setup, planning, execution, observation." >}}
  {{< card link="plan-grammar" title="Plan grammar" subtitle="The YAML import format and the live `job schema`." >}}
  {{< card link="machine-interface" title="Machine interface" subtitle="JSON output, JSON-lines streams, and the `/events` HTTP API." >}}
  {{< card link="web-dashboard" title="Web dashboard" subtitle="What `job serve` shows, and who it's for." >}}
  {{< card link="recipes" title="Recipes" subtitle="Patterns that don't fit in `--help` — great plans, criteria as tests, multi-agent." >}}
  {{< card link="contributing" title="Contributing" subtitle="Package layout, migrations, test helpers, hooks." >}}
{{< /cards >}}
