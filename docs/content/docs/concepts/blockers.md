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

Atomic: if any named edge does not exist, the call fails with `<blocked> is not blocked by <blocker>` and removes nothing — a typo in one id is caught rather than silently ignored.

## Cycle detection

Blockers form a directed graph, and the tree adds edges of its own: a parent closes only when its last open child does, so a parent waits on each open child. A loop in that combined graph is a deadlock no close can break, so `block add` refuses any edge that would close one. Cycles are detected across the **full input set** of a single call — so adding two edges that would together close a cycle is refused even when neither edge is individually problematic. The error walks the loop:

```text
Error: cannot block B by A: would create a circular dependency: B blocked by A, A blocked by B
```

Because containment counts, blocking a task on its own ancestor is refused too:

```text
Error: cannot block LPGHL0 by kCvL4D: would create a circular dependency: LPGHL0 blocked by kCvL4D, kCvL4D parent of LPGHL0
```

The reverse — a parent blocked by its own descendant — is allowed. It adds nothing a parent doesn't already wait for, and the edge drops when the child closes.

[`import`](../../plan-grammar/) runs the same check over a whole plan. Refusal is the entire transaction's outcome — no partial application.

These checks only stop a *new* loop from being written. A store populated before they existed can already hold one — [`status`](../../reference/observation/#status) scans the whole graph on every run and reports each loop it finds, in the same wording as the refusal above, plus which `block remove` breaks it:

```text
Deadlock: B blocked by A, A blocked by B — fix: job block remove B by A
```

There is no automatic repair; `block remove` is a decision about which edge was wrong, not something the tool can guess.

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
