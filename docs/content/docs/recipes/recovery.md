---
title: Recovering from mistakes
weight: 4
---

Every change in Jobs is an event in an append-only log, so almost nothing is lost by a mistake — but the fix still has to be the right verb. Three verbs undo or end work, and they mean different things:

| Situation | Verb | What survives |
|---|---|---|
| It was closed, but it isn't finished | [`reopen`](../../reference/execution/#reopen) | everything; the task is open again |
| It's not going to happen | [`cancel -m "<reason>"`](../../reference/execution/#cancel) | everything, plus the reason |
| It should never have existed | [`cancel --purge -m "<reason>"`](../../reference/execution/#cancel) | nothing in any view |

Reach for them in that order. Most mistakes aren't any of the three, though — see [Smaller mistakes](#smaller-mistakes) at the end first.

## Reopen: the work wasn't finished

A leaf got closed too early: a criterion was marked `failed` and closed anyway, a reviewer found a hole, the close was on the wrong id. Here the middleware leaf was closed with one criterion failing:

```sh
job done XRZTSR --criterion JrI=passed --criterion n62=failed -m "Internal routes still hit the bucket; shipping the 429 path now."
```

On reflection that isn't shippable. Reopen it:

```sh
job reopen XRZTSR
```

```text
Reopened: XRZTSR "Middleware returns 429 with Retry-After"
  Re-blocked: lcycGU "Document the limits in the API guide" (blocked by XRZTSR)
  Re-blocked: qhjsJH "Expose a refused-requests counter on /metrics" (blocked by XRZTSR)
  claimed by alice (expires in 30m)
```

`reopen` claims the task for you by default, on the theory that reopening means "I'm picking this back up". `--no-claim` leaves it available for someone else — use that when you reopen on an agent's behalf. `--cascade` also reopens the subtasks the task's own `done --cascade` or `cancel --cascade` closed with it; subtasks closed separately stay closed.

Reopening also undoes what the close did on its own. Closing a blocker removed its block edges, which left the leaves that waited on it available to claim against unfinished work; reopening puts those edges back — the `Re-blocked:` lines above. And if the leaf was the last one open under its parent, its close auto-closed the parent; reopening the leaf reopens the parent too, with an `Auto-reopened:` line in the ack.

It leaves alone anything a person did since the close: a parent someone closed by hand stays closed, and an edge someone re-added or removed, or whose dependent has since been closed, stays as it is. The [`reopen` reference](../../reference/execution/#reopen) has the exact rules.

One thing reopen does not touch: **the criteria keep their marks.** The failed row still reads `[!]`. Set it back to pending so the next close has to account for it again:

```sh
job edit XRZTSR --set-criterion n62=pending
```

## Cancel: it's not going to happen

The work is real but no longer wanted: superseded, out of scope, folded into something else. `cancel` closes it and records why. The reason is required — it is the whole point of canceling instead of deleting:

```sh
job cancel lcycGU
```

```text
Error: cancel requires --reason "<text>"
```

```sh
job cancel lcycGU -m "Folded into the API guide rewrite; tracked there."
```

```text
Canceled: lcycGU "Document the limits in the API guide" as=alice
  reason: 49 chars · "Folded into the API guide rewrite; tracked there."
```

Write the reason for someone reading `job log` in six months: where did the work go, or why did it stop mattering? `-F reason.md` takes a longer one from a file.

Canceling is still a close, so it moves the tree:

- **It unblocks dependents**, exactly as `done` does — "this isn't going to happen" is as final as "this happened". If a dependent needed the canceled work, it is now available and wrong. Cancel it in the same call (`job cancel <a> <b> -m "…"` is atomic), or re-block it on whatever replaces the canceled work.
- **The last open child closing closes the parent.** If every sibling was canceled, the parent is canceled too:

  ```text
  Canceled: jpSx13 "Backfill old invoices" as=alice
    reason: 52 chars · "Nothing to backfill once the vendor imports history."
    Auto-closed (canceled): zycvaO "Migrate billing"
  ```

  If any sibling was done, the parent closes as done.
- **To drop a whole subtree, cancel its root with `--cascade`.** Without it, canceling a parent with open children refuses outright — the same "incomplete subtasks" rule `done` applies — rather than leaving them behind on the frontier where `next` would still hand them out:

  ```text
  Error: task zycvaO has open subtasks: jpSx13 (Backfill old invoices) (run 'job cancel --cascade zycvaO' to cancel all).
  ```

A canceled task can be brought back with `reopen`, reason and all still in its history.

## Purge: it should never have existed

A bare `job add` that minted a stray root, a duplicate from importing the same plan twice, a test tree in a real store: these aren't work that was abandoned, they're noise. `--purge` removes the task from every view — `ls`, `show`, `log`, the dashboard — instead of closing it:

```sh
job cancel wPIwxT --purge -m "Duplicate root from a bare add; the real leaf is XRZTSR."
```

```text
Purged: wPIwxT "Wire the middleware"
  reason: Duplicate root from a bare add; the real leaf is XRZTSR.
  (1 events erased)
```

```sh
job show wPIwxT
```

```text
Error: task "wPIwxT" not found
```

Three things to know before you reach for it:

- **It's irreversible from the CLI.** There is nothing to `reopen`. If there's any chance the task was real work, `cancel` without `--purge` instead.
- **A purge still takes a reason, and records one** — the purge itself is an event.
- **It is not a way to erase a secret.** The event log is append-only: a purge appends a tombstone that later reads honor, and the original `created` event — title, description and all — is still in `.jobs/log/`, and in git history once that's committed. If a task's text must be gone, that is a history rewrite, not a `job` command.

Purging a subtree is guarded because it erases more than you can see at a glance:

```text
Error: cancel --purge --cascade requires --yes (irrecoverable erasure of 4 tasks)
```

Read the count, then add `--yes`.

## Smaller mistakes

Most slips don't need any of the above:

- **Filed in the wrong place** — `job move <id> under <parent>`. A bare `job add "<title>"` makes a root; moving it under the right parent is usually better than purging and re-adding, because it keeps the id anyone may already have.
- **Wrong title or description** — `job edit <id> -t "…"` or `job edit <id> -F desc.md`. The old text stays in the log.
- **Wrong blocker** — `job block remove <blocked> by <blocker>`.
- **Claimed the wrong leaf** — `job release <id>`.
- **Leaf turned out to be several** — `job split <id> "<a>" "<b>"` (see [Writing great plans](../great-plans/#size-a-leaf-as-one-unit-of-work)).
- **Marked a criterion wrong** — `job edit <id> --set-criterion "<id>=<state>"`; it works on a closed task too.

See also: [Execution verbs](../../reference/execution/), [The event log](../../concepts/events/), [Blockers](../../concepts/blockers/), [Leaves and claims](../../concepts/leaves-and-claims/#auto-close).
