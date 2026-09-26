---
title: Multiple agents
weight: 3
---

Jobs has no scheduler and no lock server. Several agents can share one store because of three small rules: every write carries a name, a claim says who holds a leaf, and only the holder can finish it. This recipe is the discipline that makes those rules work when one session — the *integrator* — hands leaves to subagents and brings their work back.

The walkthrough uses a store whose default identity is `claude`, and two subagents, `install-agent` and `faq-agent`.

## Names

**The integrator uses the store's default identity.** `job init --as claude` records it; the name is whoever is running the session — an assistant's own name, not the account it runs under. The integrator's calls carry no `--as`.

**Every subagent gets its own name, and passes it on every call.** Pick names that say what the agent is doing (`install-agent`, `faq-agent`) so `job log` reads as a story. Never reuse a name across two live agents: identity is how claims are owned, and two agents sharing a name can release and close each other's work without any error.

Identity is attribution, not security — there is no password — so the discipline is the protection. See [Identity](../../concepts/identity/).

## Pass an absolute `--db`

A subagent usually works in its own git worktree, and a worktree is a separate checkout. `.jobs.db` is gitignored, so it isn't there — but if your repo commits `.jobs/log/`, the log *is*. A bare `job` call inside the worktree then doesn't fail. It quietly builds a private cache from the worktree's copy of the log, under a new replica:

```text
1 claimed, 2 open, 1 done (last activity: 1m ago)
Identity: none set · --as required on writes
Store: replica wK3IT6 · 1 log file, 18 events · cache rebuilt on open
```

A write made there — `job note LqnaMT -m "…" --as wt-agent` succeeds — lands in `.jobs/log/wK3IT6.jsonl` inside the worktree. The integrator's store never sees it, and nobody watching `job tail` sees it either, until that branch is merged. (Without a committed log the same call fails with `no job database found`, which is at least loud.)

So every subagent call names the shared store by absolute path:

```sh
job claim omMmbF 2h --as install-agent --db /abs/path/to/repo/.jobs.db
```

Never "fix" a missing database with `job init`. It creates a second, empty store.

## Bracket the fan-out

Delegation has a before, a during and an after. Put tracker work at both ends.

### Before: mint the leaves, then turn strict mode on

**Create every leaf before you dispatch.** Work that exists only inside an agent's prompt is invisible while it runs and leaves no notes behind. Import the plan (or `job add <parent> "<title>"` each leaf), so every agent is handed a real id:

```sh
job import fanout.md
```

```text
QJtKmC  Docs refresh
omMmbF  Rewrite the install page
sPAQHz  Rewrite the FAQ
LqnaMT  Fix broken links site-wide
```

**Then turn on strict mode.** With strict on, a write without `--as` is refused instead of being attributed to the default identity — so a subagent that forgets its name fails on its first write rather than silently writing as `claude`:

```sh
job identity strict on --as claude
```

```text
Strict mode: on
```

```sh
job claim sPAQHz
```

```text
Error: identity required. Pass --as <name> before the verb.
```

`job status` shows the state on its second line, so it's hard to leave on by accident:

```text
Identity: claude (default) · strict mode on
```

Strict mode applies to the integrator too: while it's on, your own writes need `--as claude`. That's the price of the guard, and it's small.

### During: agents claim, note and release

Each agent runs the same three verbs, always with its name and the absolute store:

```sh
job claim omMmbF 2h --as install-agent --db /abs/path/.jobs.db
job note omMmbF -F /abs/scratch/omMmbF-note.md --as install-agent --db /abs/path/.jobs.db
job release omMmbF --as install-agent --db /abs/path/.jobs.db
```

- **`claim`** prints the whole briefing — description, parent, blockers, criteria — so the agent needs no follow-up `show`. Give it a duration that covers the work (`2h`); any write by the holder extends it, and the claim can't be shortened by one.
- **`note`** is where findings outlive the agent. Note as you go — what you tried, what you found, what the integrator needs to know — not only at the end. Pass bodies with `-F <file>`: shell quoting of `-m` bodies mangles backticks and `$`. When agents share a scratch directory, prefix each file with the leaf id so two agents can't overwrite each other's notes.
- **`release`** hands the leaf back, open. It's the last call an agent makes.

**Agents don't run `done`.** Closing is a claim that the work is integrated, tested and committed — which only the integrator knows. An agent that closes its own leaf also triggers auto-close on the parent and auto-unblocks dependents before anyone has looked at the diff.

Holding a claim is also what stops the integrator from stepping on the agent. Until the agent releases, the leaf is theirs:

```sh
job done omMmbF --all-passed -m "Integrated."
```

```text
Error: task omMmbF is claimed by install-agent (expires in 1h). Wait for expiry, or ask install-agent to release.
```

The same message comes back for `note`, and for another agent's `claim`. `note`, `done` and `release` on a claimed task must carry the claimant's identity — which is why a brief that shows a bare `job note <id>` is a broken brief.

`edit --set-criterion` is the exception: it isn't claim-guarded, so an agent can mark criteria as its tests go green, and a reviewer can mark them without taking the claim.

### Watching

- `job status` — counts, identity, one row per root, and `Stale:` lines for claims past their deadline.
- `job tail --users install-agent,faq-agent` — the live event stream from just those agents.
- `job log <id>` — one leaf's full history, every line attributed:

```text
[2026-09-26 17:35] omMmbF created: "Rewrite the install page"  @claude
[2026-09-26 17:35] omMmbF claimed (2h)  @install-agent
[2026-09-26 17:35] omMmbF noted: "Rewrote docs/install.md around the Homebrew path. …"  @install-agent
[2026-09-26 17:35] omMmbF released  @install-agent
[2026-09-26 17:35] omMmbF done (note: Integrated; verified the VM transcript.)  @claude
```

### After: integrate, close, turn strict off

When an agent hands back, review its diff and its notes, run the suite, commit, and close the leaf yourself — criteria and all:

```sh
job done omMmbF --all-passed -m "Integrated; verified the VM transcript."
```

```text
Done: omMmbF "Rewrite the install page" as=claude
  note: 39 chars · "Integrated; verified the VM transcript."
  Next: sPAQHz "Rewrite the FAQ"
  Parent QJtKmC: 1 of 3 complete
```

Agents forget to release. If `done` says the leaf is claimed by the agent that just finished, release it on the agent's behalf — you know the name — and then close:

```sh
job release omMmbF --as install-agent
```

When every agent is back, close the bracket:

```sh
job identity strict off --as claude
```

```text
Strict mode: off
```

## Taking over an abandoned claim

An agent that crashed or ran out of time leaves a claim behind. Its deadline will pass on its own, and the leaf will show as `Stale:` in `job status`. To take it now, claim with `--force`; the ack names who you displaced:

```sh
job claim sPAQHz --force
```

```text
Claimed: sPAQHz "Rewrite the FAQ" (overrode previous claim by faq-agent, expires in 30m) as=claude
```

Read the leaf's notes first — `job show sPAQHz` — so you pick up where it stopped rather than where it started.

## Parallel agents from one frontier

When the leaves are interchangeable and you'd rather not assign them, let each agent take the next one:

```sh
job claim --next --as agent-1 --db /abs/path/.jobs.db
```

`claim --next` is race-safe: two agents calling it at the same moment get two different leaves, never the same one. `job next all --format=json` lists the whole frontier if you'd rather decide the assignment yourself.

Assign by file ownership, not one leaf per agent. If three leaves touch the same file, one agent should take all three; merge cost decides the split, not task count.

## A brief, as a checklist

Everything a subagent needs, restated in its prompt — it may not be able to read the tracker:

- Its name, and "pass `--as <name>` and `--db <absolute path>` on every `job` call".
- The leaf id(s) to claim, and a duration.
- The substance of the leaf's description and criteria — the tracker is not the brief.
- "Note findings as you go with `-F <file>`, files prefixed with the leaf id."
- "**Release** when done. Do not run `job done`. Do not commit."

See also: [Identity](../../concepts/identity/), [Leaves and claims](../../concepts/leaves-and-claims/), [Execution verbs](../../reference/execution/), [`tail`](../../reference/observation/#tail).
