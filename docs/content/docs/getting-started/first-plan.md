---
title: Your first plan
weight: 3
---

This page walks one task tree from author through completion: write a plan, import it, claim work, close work, watch the parent auto-close. Every command below is real and the output blocks are captured from a clean run — except for task IDs and the replica id, which Jobs assigns randomly. **Your IDs will differ; the shapes won't.**

> Prerequisite: `job init --as alice` has been run in the current directory (see [Initialize](../initialize/)). The walkthrough uses the default identity `alice` — substitute your own.

## 1. Author

A plan is a Markdown document with a fenced YAML block whose top-level key is `tasks:`. Save this as `plan.md`:

````markdown
# Add /healthz endpoint

We want a minimal liveness probe so the load balancer can take an
unhealthy node out of rotation. The handler is small; wiring it into
the router has to wait until the handler exists and passes its tests.

```yaml
tasks:
  - title: Add /healthz endpoint
    ref: healthz
    labels: [release, p1]
    children:
      - title: Write the handler
        ref: handler
        desc: |
          200 OK with a JSON body of `{"status":"ok"}`. No auth, no DB
          touch — the probe must stay cheap.
        criteria:
          - returns 200 status code
          - response body is valid JSON
      - title: Wire it into the router
        blockedBy: [handler]
        labels: [glue]
```
````

Two leaves under one parent. The router task is blocked by the handler. The handler carries two acceptance criteria. The full grammar is in [Plan grammar](../../plan-grammar/).

## 2. Validate

Always dry-run first. The dry-run resolves refs, checks for cycles, and renders the tree exactly as `job ls` would — but writes nothing:

```sh
job import plan.md --dry-run
```

```text
- [ ] `<new-1>` Add /healthz endpoint
  - [ ] `<new-2>` Write the handler
  - [ ] `<new-3>` Wire it into the router (blocked on <new-2>)
```

`<new-N>` placeholders mark tasks that don't exist yet. If the plan were invalid — missing `title`, dangling `blockedBy`, duplicate `ref` — the dry-run would refuse with a stable error and nothing would be written.

## 3. Import

Drop `--dry-run` to commit the tree atomically:

```sh
job import plan.md
```

```text
5i5vFN  Add /healthz endpoint
AIs0dP  Write the handler
oBFdSx  Wire it into the router
```

The six-character ids are stable and case-sensitive. Use them anywhere a verb takes `<id>`.

## 4. Status

Open a session with `job status` to see the landscape:

```sh
job status
```

```text
3 open, 0 done (last activity: 0s ago)
Identity: alice (default) · strict mode off
Store: replica S8OIBo "laptop:~/src/healthz" · 1 log file, 8 events · cache in sync

  Add /healthz endpoint (5i5vFN): 0 of 2 done · next AIs0dP
Next: AIs0dP "Write the handler"
```

The per-root rollup line and the `Next:` hint name the work to do. The router task isn't surfaced because it's blocked.

The `Store:` line is the record behind all of it: the log files under `.jobs/log/` that hold every event, this checkout's replica id, and whether `.jobs.db` is a current cache of them. See [The store](../../concepts/the-store/).

## 5. Claim

`claim --next` finds the globally-next available leaf and locks it:

```sh
job claim --next
```

```text
Claimed: AIs0dP "Write the handler" (expires in 30m) as=alice

ID:           AIs0dP
Title:        Write the handler
Status:       claimed
Claim:        claimed by alice, expires in 30m
Parent:       5i5vFN (Add /healthz endpoint)
Blocks:       oBFdSx
Created:      2026-05-07 18:09

Description:
  200 OK with a JSON body of `{"status":"ok"}`. No auth, no DB touch — the probe must stay cheap.
Criteria: 2 pending — mark each before close, or use --force-close-with-pending
  76o [ ] returns 200 status code
  sX1 [ ] response body is valid JSON
```

The first line is the scriptable signal (any `Claimed:` line means success). Below it is the full briefing — same as `job show AIs0dP` would print. No follow-up `show` needed.

The `76o` and `sX1` are short ids for the two criteria — addressable by `--criterion <ref>=<state>` when you close.

## 6. Close (with criteria) and grab next

Now do the work. When you're done, close the task — and let `--all-passed` mark every criterion as passed in one move, plus `--claim-next` to atomically grab the next leaf:

```sh
job done AIs0dP --all-passed --claim-next -m 'Returns 200 OK with the expected JSON body.'
```

```text
Done: AIs0dP "Write the handler" as=alice
  note: 43 chars · "Returns 200 OK with the expected JSON body."
  Parent 5i5vFN: 1 of 2 complete
Claimed: oBFdSx "Wire it into the router" (expires in 30m) as=alice

ID:           oBFdSx
Title:        Wire it into the router
Status:       claimed
Claim:        claimed by alice, expires in 30m
Parent:       5i5vFN (Add /healthz endpoint)
Labels:       glue
Created:      2026-05-07 18:09
  Marked 2 criteria passed before closing.
```

The handler closes, its block on the router is auto-removed (closing a blocker auto-unblocks dependents), and the router is claimed in the same call. The parent is now `1 of 2 complete`.

Marking criteria one by one is also fine — `--criterion 76o=passed --criterion sX1=passed` does the same job. `--all-passed` is the shorthand for the common case. If you genuinely need to ship without a criterion satisfied, `--force-close-with-pending` records the unmarked labels as a waiver on the done event so a reviewer can see what was deferred.

## 7. Close the last leaf — parent auto-closes

Finish the router. No criteria, just the note:

```sh
job done oBFdSx -m 'Mounted on the default router; smoke-tested via curl.'
```

```text
Done: oBFdSx "Wire it into the router" as=alice
  note: 53 chars · "Mounted on the default router; smoke-tested via curl."
  Auto-closed: 5i5vFN "Add /healthz endpoint"
```

Closing the last open child cascades up: the parent `5i5vFN` auto-closes, attributed to the agent who closed the final child. You never explicitly close parents — they're scaffolding for their leaves.

## 8. Confirm

```sh
job status
```

```text
0 open, 3 done (last activity: 0s ago)
Identity: alice (default) · strict mode off
Store: replica S8OIBo "laptop:~/src/healthz" · 1 log file, 16 events · cache in sync
```

The plan is done. Every state change above is preserved as an event — replay it with `job log 5i5vFN` to see the full transcript, or read `.jobs/log/S8OIBo.jsonl`, which is the same history as text.

Commit `.jobs/log/` and the plan travels with the repo. [Across machines](../across-machines/) takes it from here.

## What you just used

| Verb | Why it mattered |
|------|-----------------|
| `job import` | Atomic tree creation from a Markdown plan. All-or-nothing. |
| `job status` | Session-opening identity check + landscape briefing. |
| `job claim --next` | Find and lock the next leaf in one call. |
| `job done --all-passed --claim-next` | Close with criteria handled and grab the next leaf atomically. |
| (auto-close) | Parents close themselves when their last child closes. |

That's the whole loop. Everything else — blockers, multi-agent attribution, `job tail` for live observation, the `/events` HTTP API — is depth on top of this five-verb spine.

Next: [Concepts](../../concepts/) to round out the mental model, or [Plan grammar](../../plan-grammar/) for the full YAML reference.
