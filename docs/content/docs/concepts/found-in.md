---
title: Found-in
weight: 5
---

A **found-in** reference records where a task was surfaced — typically the leaf someone was working when a defect turned up. It is provenance, and only provenance: it does not parent the task, and it creates no blocking relationship in either direction.

## The problem it solves

`job` puts two relations on the parent edge at once. A child says both *this is part of that* (provenance) and *that cannot close until this closes* (blocking). For planned work those move together, which is the point. For a defect discovered mid-plan they must come apart: you want to remember that the bug came out of `kTuMb`, but you do not want the bug to hold the whole plan open while it waits for a decision.

Filing the bug under the leaf gets you both relations when you wanted one. Filing it elsewhere and writing "found while doing kTuMb" in the description gets you neither — nothing links the two, and no reader can walk from the leaf to what it produced.

Found-in is the missing half: the reference without the gate.

## Recording it

At creation time, when the bug is filed onto an issues root:

```sh
job add <issues-root> "Router drops the trailing slash" --found-in <leaf>
```

…or after the fact:

```sh
job found-in <task> in <source>
```

One source per task — work is found in one place. Setting a source over an existing one replaces it, and the event records both ids so the history keeps the earlier answer.

```sh
job found-in <task> --clear
```

removes the reference. Clearing a task that has none is an error rather than a silent no-op, so a mistyped id is caught.

A task cannot be found in itself. Longer loops are permitted: nothing walks this edge, so there is no traversal to protect and no cycle to detect.

## Reading it, from both ends

`job show` prints the reference on the task and its mirror on the source:

```text
ID:           qP4nR
Title:        Router drops the trailing slash
Status:       available
Parent:       9xKmT (Issues)
Found in:     kTuMb Wire it into the router (done)
```

```text
ID:           kTuMb
Title:        Wire it into the router
Status:       done
Surfaced:
  - `qP4nR` Router drops the trailing slash
```

The source's status is printed inline because the interesting case is a closed source — the plan shipped, the bug did not. `--format=json` carries the same information as `found_in` and `surfaced`.

## What it survives

The whole point of the reference is that it outlives the work that produced it. It survives the source being marked `done`, being canceled, being canceled as part of a `cancel --cascade`, and being soft-deleted. None of the close or cancel paths touch the table.

The one thing it does not survive is `cancel --purge`, which erases task rows outright. A reference to a row that no longer exists is not provenance worth keeping, so the edge goes with it.

## What it deliberately is not

- **Not hierarchy.** The task is parented wherever you filed it. Closing the source's tree never cascades into the task, and the task never counts toward the source's open children.
- **Not a blocker.** Either end can be claimed, worked and closed while the other is wide open. `job next`, `job orient` and `job ls` are unaffected in both directions.
- **Not a triage system.** There is no severity field, no triage state, and no reporter. Use [labels](../labels/) for severity and component; `job ls --label` answers "what is broken in this area, across every plan".

## When to use which relation

| You want to say | Use |
|---|---|
| This is part of that | Hierarchy (`job add <parent>`, `job move`) |
| That cannot close until this does | [Blockers](../blockers/) (`job block add`) |
| This turned up while doing that | Found-in (`job found-in`) |

The rare defect that genuinely *should* hold a plan open gets both: file it with `found-in` for the provenance, then `job block add <leaf> by <bug>` for the gate. Two explicit statements beat one overloaded edge.
