---
title: Blockers
weight: 4
---

A **blocker** is a "this can't proceed until that closes" relationship between two tasks. It's the second axis of structure, alongside parent/child hierarchy: hierarchy is composition, blockers are sequence.

## Authoring

Blockers can be set at import time in the YAML grammar:

```yaml
- title: Wire it into the router
  blockedBy: [handler]
```

…or after the fact via `block add`:

```sh
job block add <blocked> by <blocker> [<blocker>...]
```

The verb is variadic — multiple blockers in one call run in a single transaction. Either every edge is recorded or none of them are. Duplicates collapse to a single edge silently.

## Removing

```sh
job block remove <blocked> by <blocker> [<blocker>...]
```

Atomic and idempotent. Removing a non-existent edge is not an error — the post-call state is what you asked for.

## Cycle detection

Blockers form a directed graph. Cycles are detected across the **full input set** of a single `block add` call — so adding two edges that would together close a cycle is refused even when neither edge is individually problematic.

```text
Error: cycle detected: A → B → A
```

The DB never holds a cyclic state. Refusal is the entire transaction's outcome — no partial application.

## Auto-unblock on done

When a task is marked `done`, every edge `<other> blockedBy <this>` is removed automatically. The downstream task transitions back to `available` if no other blockers remain, and the next `next` walk will surface it.

This is what makes blockers ergonomic: you don't have to remember to clean up after closing a blocker. The graph maintains itself.

`cancel` also auto-unblocks dependents — canceled work stops being a blocker. (Conceptually: "this isn't going to happen" is just as definitive as "this is done" for downstream sequencing.)

## Effect on the frontier

A blocked leaf is **not** claimable. `job next`, `job claim --next`, and the `/events` views all skip past it. `job ls` shows it with the `(blocked on <id>)` annotation so you can see what's holding it up:

```text
- [ ] `kTuMb` Wire it into the router (blocked on MZHd1)
```

`job show <id>` lists both directions — `Blocked by:` (blockers of this task) and `Blocks:` (tasks this one is blocking). The two views are symmetric.

## When to use blockers vs. hierarchy

- **Hierarchy** when the relationship is "this is part of that." Children scope their parent.
- **Blockers** when the relationship is "this has to finish before that can start." Independent siblings sequenced.

Hierarchy auto-closes; blockers auto-unblock. Don't shoehorn a sequence into a parent/child relationship just because both tasks are in the same plan.
