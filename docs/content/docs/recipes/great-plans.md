---
title: Writing great plans
weight: 1
---

A plan is a Markdown document with a fenced YAML `tasks:` block, loaded with [`job import`](../../reference/planning/#import). The grammar is small — [Plan grammar](../../plan-grammar/) covers every key — so the grammar is not the hard part. The hard part is writing a tree that an agent with no context can pick up at any leaf and finish. This page is about that.

## Start from a worked example

Here is a plan for rate-limiting a public API. Everything on this page refers back to it.

````markdown
# Rate-limit the public API

Anonymous clients can hammer `/search` today. We want a per-key token
bucket in front of the public routes, a 429 with `Retry-After` when it
runs dry, and an operator-visible counter.

```yaml
tasks:
  - title: Rate-limit the public API
    ref: rate-limit
    desc: |
      Per-API-key token bucket in front of every public route. Anonymous
      callers share one bucket per client IP. Internal routes are exempt.
    labels: [api]
    children:
      - title: Token bucket in internal/ratelimit
        ref: bucket
        desc: |
          A pure, clock-injected token bucket: `Allow(key) (ok bool,
          retryAfter time.Duration)`. No HTTP in this package — the
          middleware is a separate leaf.
        criteria:
          - a fresh key allows exactly `burst` calls, then refuses
          - a refused call reports the wait until the next token
          - tokens refill at `rate` per second against an injected clock
      - title: Middleware returns 429 with Retry-After
        ref: middleware
        blockedBy: [bucket]
        criteria:
          - a refused request gets 429 and a Retry-After header in whole seconds
          - requests under /internal/ never touch the bucket
      - title: Expose a refused-requests counter on /metrics
        blockedBy: [middleware]
        criteria:
          - the counter increments once per 429, labelled by route
      - title: Document the limits in the API guide
        blockedBy: [middleware]
```
````

The prose above the fence is for people; the importer ignores it. Keep it anyway — the plan file is the dated record of *why* this work exists, and it is what a reviewer reads first.

## Size a leaf as one unit of work

A leaf is what one agent claims, finishes and closes in one sitting. Two tests decide the size:

- **Could you close it with one honest note?** "Bucket landed with a fake clock; one test per criterion" is one note. "Bucket landed, middleware half-wired, docs not started" is three leaves pretending to be one.
- **Does it touch one surface?** Group by the files and the surface the work touches, not by the sentences in the request. "The form's spacing, typography and badge column" is one leaf if it is one template and one stylesheet. Twenty review comments on one page are not twenty leaves.

Per-leaf overhead is roughly fixed — a claim, a briefing, a close, and in a multi-agent run a worktree and an integration — so tiny leaves are not free. Split a leaf when the halves carry a genuine decision, a different risk, or a different blocker. "It was a different bullet in the feedback" is none of those.

The example splits the bucket from the middleware for exactly that reason: the bucket is pure logic with its own tests and no HTTP; the middleware is blocked on it and carries different criteria. The counter and the docs are separate leaves because they can run in parallel once the middleware lands.

When a leaf turns out to be several after you've started, don't quietly widen it — split it:

```sh
job split xwP6SQ "Bucket core" "Per-IP fallback for anonymous callers"
```

```text
Split: xwP6SQ into 2 children
  t2sB55 "Bucket core"
  RXc1OH "Per-IP fallback for anonymous callers"
Released: xwP6SQ (prior claim by alice auto-released — parent now has open children)
```

[`split`](../../reference/planning/#split) only works on a leaf with no children. Your claim is released, because the original is a parent now, and it auto-closes when the new children do. **Its criteria stay on it** — and an auto-close does not check them, so they end up pending on a closed task. After a split, re-add each criterion to the child that will satisfy it (`job edit t2sB55 --criterion "…"`) and mark the parent's copies `skipped` with `job edit xwP6SQ --set-criterion "<id>=skipped"`.

## Parents are scaffolding

A parent groups its leaves and closes itself when the last one closes (see [Leaves and claims](../../concepts/leaves-and-claims/#auto-close)). It is not claimable while it has open children, so it cannot hold work of its own. If a parent has work — "write the design doc before the leaves start" — make that work a leaf child, first in the list.

Give the parent a `desc` that states the goal and the constraint every leaf shares ("internal routes are exempt"). [`job orient`](../../reference/observation/#orient) renders the target leaf's whole root tree with each parent's description, so a parent's `desc` is read by every agent that claims any leaf under it. Say shared things once, there.

## Write descriptions for a reader with no context

The schema's own hint is the rule: *assume the reader is an agent with fresh context.* The description is what someone sees when they `claim` the leaf — the claim prints the full briefing — and nothing else from your head comes along.

A good leaf description says:

- **What** to build, specifically enough to start: the function signature, the file, the route.
- **Why**, or the constraint that makes the obvious approach wrong: "clock-injected", "no HTTP in this package".
- **Where the edges are**: what belongs to a sibling leaf instead ("the middleware is a separate leaf").

`desc` is [Markdown prose](../../concepts/prose/): single newlines reflow, blank lines separate paragraphs, lists and fenced code keep their shape. Use `|` block scalars so the YAML doesn't fight you.

## Name dependencies with refs

`blockedBy` resolves an entry three ways, in order: a `ref` in this import, a verbatim title in this import, then an existing short id in the store. Prefer refs.

- **Refs survive edits.** A title-based `blockedBy` breaks the moment someone polishes the title before importing. A ref is a handle you chose for exactly this purpose.
- **Refs are unique across the document**, so there is no ambiguity error to hit.
- **Refs live only for one import.** They are not stored on the task. To depend on work from an earlier plan, use its short id: `blockedBy: [jxs6sI]`.

A mistyped ref fails the dry-run, naming the row:

```text
Error: tasks[0].children[1]: blockedBy[0] "buckets" does not match any ref, imported task title, or existing task ID
```

## blockedBy hygiene

A block orders the frontier: [`next`](../../reference/observation/#next), `orient` and `claim --next` skip a blocked leaf. It is the only sequencing the tool understands, so be precise with it.

**Block only on real prerequisites.** Each edge you add removes a leaf from the parallel frontier. The counter and the docs in the example both wait on the middleware, and on nothing else — so once the middleware closes, two agents can take them at once. Chaining them (docs blocked by counter) would serialize work that doesn't need it.

**Don't restate the tree as blocks.** Siblings under a parent aren't ordered by anything except `blockedBy`, but not every sibling needs an order. If the leaves could genuinely run in any order, leave them unblocked; list order is enough of a hint for a human.

**Never block a leaf on its own ancestor.** A parent closes only when its last child closes, so a child blocked by its parent can never become available — and neither can the parent. Import refuses it, dry run included, and names the loop:

```text
Error: tasks[0].children[1]: blockedBy "release": would create a circular dependency: tasks[0].children[1] "Announce it" blocked by tasks[0] (ref release), tasks[0] (ref release) parent of tasks[0].children[1] "Announce it"
```

Block on the sibling that actually has to finish (`blockedBy: [tag]`) instead.

**No loops.** Two leaves blocked on each other form a cycle that no close can break. Both [`block add`](../../reference/planning/#block) and `import` refuse one — including a longer loop that runs through a parent, since a parent waits on its children — and the error lists every step, so you can see which edge to drop.

If every leaf in a tree carries a `blocked on`, nothing in it can ever start — check the dry-run for a tree with no unblocked leaf.

**Blocking on a parent ref is fine** when the dependency really is "all of that subtree": `blockedBy: [rate-limit]` on a task outside the tree waits until the whole rate-limit parent auto-closes.

**Remember that closing — or canceling — a blocker unblocks its dependents automatically.** You never clean up edges after a `done`. The flip side is in [Recovery](../recovery/): canceling a blocker releases its dependents too.

## Write criteria as checks

Every leaf that produces something checkable should carry `criteria:` — short sentences a reviewer can mark `passed` or `failed` without arguing about what they mean. `job done` refuses to close while any are pending. [Criteria as tests](../criteria-as-tests/) is the whole recipe; the short version:

- One assertion per row.
- The outcome, not the steps: `a refused request gets 429` rather than `run the integration tests`.
- Leave a leaf with no checkable output (the docs leaf above) without criteria rather than inventing one.

## Validate, then import

Always dry-run first. It resolves every ref, renders the tree the way `ls` would, and writes nothing:

```sh
job import rate-limit.md --dry-run
```

```text
- [ ] `<new-1>` Rate-limit the public API
  - [ ] `<new-2>` Token bucket in internal/ratelimit
  - [ ] `<new-3>` Middleware returns 429 with Retry-After (blocked on <new-2>)
  - [ ] `<new-4>` Expose a refused-requests counter on /metrics (blocked on <new-3>)
  - [ ] `<new-5>` Document the limits in the API guide (blocked on <new-3>)
```

Read it as the frontier an agent will see: exactly one leaf is available (`<new-2>`), and closing it will make one more available, then two. If the shape surprises you, fix the plan now; a dry-run costs nothing.

Watch stderr too. A key outside the grammar is dropped with a warning, not an error — the dry-run below has silently lost its criteria because the author wrote `acceptance:`:

```text
warning: bad2.yaml: ignored 1 key(s) outside the import grammar: acceptance
- [ ] `<new-1>` Rate-limit the public API
  - [ ] `<new-2>` Token bucket
  - [ ] `<new-3>` Middleware (blocked on <new-2>)
```

Then import for real. The whole file lands in one transaction or not at all:

```sh
job import rate-limit.md
```

```text
lQPrYh  Rate-limit the public API
jxs6sI  Token bucket in internal/ratelimit
XRZTSR  Middleware returns 429 with Retry-After
qhjsJH  Expose a refused-requests counter on /metrics
lcycGU  Document the limits in the API guide
```

## Put it in the right place

- **Nest a phase under existing work** with `--parent`: `job import phase-2.md --parent lQPrYh`. A bare import makes every top-level entry a new root, and roots render as peers of whole projects in `status` and `ls`.
- **Keep discovered bugs out of the plan.** A bug found mid-plan goes in an [issue-tree](../../concepts/tree-kinds/), where `next` won't hand it out as plan work; `job issue "<title>"` files it there and records which leaf surfaced it.
- **Keep the plan file.** Commit it under a dated name next to your other design notes. The tree in the store is the live state; the file is why it exists.

## Checklist

- Every leaf closes with one honest note and touches one surface.
- Parents carry the goal and the shared constraint; leaves carry the work.
- Each description names what, why, and where the edges are.
- `blockedBy` uses refs, names only real prerequisites, never an ancestor, never a loop.
- Checkable leaves carry criteria, one assertion each.
- `--dry-run` read as a frontier, stderr read for warnings, then import.

See also: [Plan grammar](../../plan-grammar/), [`import`](../../reference/planning/#import), [Blockers](../../concepts/blockers/), [Your first plan](../../getting-started/first-plan/).
