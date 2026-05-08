---
title: Machine interface
weight: 5
---

How agents and other programs talk to Jobs. Two surfaces:

- **CLI JSON.** Every read verb (and several writes) accepts `--format=json`. `tail` emits one JSON object per line — a streaming, line-oriented contract.
- **HTTP `/events`.** The web dashboard's data source, but reachable directly: SSE for live tail, JSON for replay. Loopback by default once `job serve` is running.

{{< cards >}}
  {{< card link="json-output" title="CLI JSON output" subtitle="Which verbs accept `--format=json`, the shape they return, and the JSON-lines contract on `tail`." >}}
  {{< card link="http-api" title="`/events` HTTP API" subtitle="Query params, SSE framing, JSON replay, and `curl` examples that work as-is." >}}
{{< /cards >}}

The two surfaces share a wire shape — the event object served by `/events` matches the per-line objects from `job tail --format=json`. Code that consumes one will consume the other with no translation.
