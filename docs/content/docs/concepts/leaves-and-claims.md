---
title: Leaves and claims
weight: 2
---

A task is **claimable** if and only if it has no open children. Parents with open children are scaffolding — `next`, `claim --next`, and the dashboard's frontier views skip past them, surfacing the executable work in their descendants.

This is the single rule that lets agents coordinate without a queue, a scheduler, or a lock manager.

## Leaf frontier

The leaf frontier is the set of all available, unblocked, unclaimed leaves. Read it with:

```sh
job next            # the single next leaf (idiomatic)
job next all        # the entire frontier (orchestration)
```

`next` walks forward-siblings-first at each ancestor level before stepping up — so the hint after a `done` keeps you inside the current plan as long as there's work there. Across roots, root order is preserved.

`--label <name>` filters the frontier; `--include-parents` falls back to the legacy "any available task" semantics if you really want to claim a parent.

## Claims

A claim is a short-TTL lock on a leaf. Default duration is 30 minutes:

```sh
job claim <id>                       # 30m
job claim <id> 4h                    # explicit duration; units s/m/h/d
job claim --next                     # find and lock in one call
job claim <id> -m "what I'm trying"  # record a starting note in the same tx
```

Claims are advisory but enforced for terminal state changes: only the holder can `done` or `release` without `--force`. The first line of a `claim` ack is the scriptable signal — agents grep for `Claimed:`. The full briefing follows, identical to `job show <id>`.

`-m "<text>"` records a `noted` event *before* the `claimed` event in the same transaction, so an agent's starting context anchors the work at the head of its timeline rather than trailing it. Mirrors `release -m` and `done -m`; supports `@path` and `-` for stdin, and `-F <path>` (`-F -`) as the file form.

Claiming a parent that has open children **is refused**. The lock has no referent — its real work lives in its descendants. Claim a leaf instead.

## TTL and auto-extend

A claim's TTL is a liveness signal, not a deadline. Any write to a claimed task by its holder auto-extends the TTL by 30 minutes — `note`, `edit`, `label add/remove`, and `criterion` operations all count. So while you're working, your claim stays fresh without explicit heartbeats.

`heartbeat` exists for the "thinking, not writing" case: a long pause where you genuinely have nothing to commit:

```sh
job heartbeat <id>            # +30m
job heartbeat <id> <id> <id>  # variadic
```

When a claim expires, the lock evaporates — the leaf becomes claimable again. Expired claims show up as `Stale:` lines in `job status`, naming the leaf and how long the claim has been past its TTL. Treat stale claims as a question for the operator: was the agent killed? Was it stuck? Should someone reclaim?

## Auto-release

Adding an open child to a **claimed** parent **auto-releases** the parent's claim. The parent no longer has executable work of its own — its leaves are now its children. The `released` event records the auto-release trigger and the prior claimant, so the log explains the transition.

This is what makes "split this task into subtasks mid-work" safe — you don't have to remember to release first.

## Auto-close

When the last open child of a parent closes — via `done` **or** `cancel` — the parent **auto-closes**, cascading upward through the ancestor chain. The agent who closed the final child is attributed on every auto-close event.

The destination depends on the sibling mix:

- Any sibling closed as `done` → parent cascades to `done`.
- All siblings canceled → parent cascades to `canceled`.

So a fully-canceled subtree drops "all work here was dropped" right up the tree, while a normal completion behaves as you'd expect.

## Putting it together

These three behaviors — refuse-claim-on-parent, auto-release on split, auto-close on last child — mean parents are pure scaffolding. You never explicitly claim or close them. `next` always points at real work. The shape of the tree at any moment tells you exactly what's in flight, what's available, and what's done.
