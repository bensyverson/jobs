---
title: Identity
weight: 1
---

Every write is attributed to a named identity. Reads (`ls`, `show`, `next`, `log`, `tail`, `status`) work without one.

## Resolution chain

When a write happens, the writer's name is resolved in this order — first match wins:

1. `--as <name>` flag on the call.
2. The DB-level default identity, recorded at `init` time.
3. Error: `identity required. Pass --as <name> before the verb.`

There is **no** `$USER` fallback, at init or at write time. `init` requires `--as <name>` (or `--strict`) and records exactly what you pass; nothing is read from the environment.

```sh
job init --as ben                         # records ben as default
job add "Write docs"                      # → attributed to ben (the default)
job --as alice add "Write tests"          # → attributed to alice (override)
```

## Default identity

Set at init time — `--as` is required:

```sh
job init --as claude
```

Use the name of whoever is running the command: a person's handle, or — for an automated assistant — the assistant's own name, not the account it runs under.

After init, change it with:

```sh
job identity set <name> --as <current-name>
```

The `--as` is required because the change itself is a write that needs attribution. This is bootstrap discipline — the only safeguard against an unattributed default-flip.

## Strict mode

`--strict` opts out of the default entirely. Every write must carry an explicit `--as`:

```sh
job init --strict
job add "x"                               # → identity required. Pass --as <name> ...
job --as alice add "x"                    # ok
```

Toggle after init with `job identity strict on|off --as <name>`. Turning strict *off* leaves the default unset until you call `job identity set` explicitly — there's no implicit revival.

## Multiple agents

Multiple agents can share one `.jobs.db` simultaneously. Each passes its own `--as`, or shares the default for unrelated work. There is no password, no key, no permission boundary — the name is an attribution label.

Users are created lazily: the first time a new name appears in a write, its row is added to the `users` table.

## What identity gives you

- **Event log attribution.** Every event (`created`, `claimed`, `done`, `noted`, …) records the actor.
- **Claim ownership.** Only the holder of a claim can release it without `--force`. Heartbeats and writes that auto-extend the claim only fire when actor matches holder.
- **`--mine` filtering.** `job ls --mine`, `job status` (without `--as`), and dashboard views collapse work by claimed-by name.
- **Auto-close attribution.** When a parent auto-closes after its last child completes, the agent who closed the final child is recorded on the parent's auto-close event.

Identity is intentionally weak as a security primitive but rich as a coordination primitive. Treat it that way.
