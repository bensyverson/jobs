# Issues vs tasks: giving discovered work its own lifetime

*2026-07-25 — proposal, not a decision*

> **Decided 2026-08-28.** Tree kind is a property of the root only, and an issue
> root owns task children directly. The proposal also missed an existing precedent:
> `job block add` is already a non-parent edge stored apart from the tree, and
> `found-in` should be modelled on it. Decisions and the import block now live in
> [2026-08-28-body-flags-orient-and-issues.md](2026-08-28-body-flags-orient-and-issues.md);
> the YAML below is superseded.

## The problem, as it actually shows up

> "One persistent problem with Jobs is that if we have a bug leaf on a tree, we
> have to keep the whole tree open until the bug is resolved."

A bug found while working a plan gets filed where it was found. It then holds
that plan open long after the plan is otherwise finished, because a parent
cannot close while a child is open.

The instinct is to reach for a second tool — an `issue` or `ticket` binary
alongside `job`. This proposal argues against that, and for a smaller change
that solves the same problem.

## What is actually wrong

`job` puts **two different relations on the same edge**:

- **provenance** — this tree is where the bug was found
- **blocking** — this tree cannot close until the bug is fixed

For planned work these always coincide, which is why one edge has been enough.
For a discovered defect they come apart: it should keep the first relation
forever and shed the second immediately.

Underneath that is a deeper mismatch. `job` models completion **structurally** —
a parent closes when its children close. That works because a plan is a
decomposition, and a decomposition has a bottom. A defect is not decomposed, it
is *encountered*: its lifetime is not bounded by the plan that surfaced it, and
it closes on evidence — a regression test — rather than on its children
finishing.

So the thing an "issues universe" really provides is **a place where
open-endedness is normal and structural completion does not apply**.

## Why not a second tool

`job orient` exists because "where do I look" was the expensive problem. A second
binary doubles it, and every cross-reference between the two becomes hand-
maintained prose. Two stores also raise the question of which one is
authoritative for work that is both a bug *and* a plan item — which is most bugs
worth fixing.

There is a real argument for separation, but it is about **schema and workflow**,
not about storage: severity triage, external reporters, first-response SLA,
deduping incoming reports from many people. None of that applies to a
single-operator tool today. Fields are what make trackers heavy, and they are the
part to skip until triage is an actual activity rather than an anticipated one.

## Proposal

**One binary, one database.** Add the smallest thing that makes the two
lifetimes distinct:

1. **A tree kind.** Mark a root as a task-tree or an issue-tree. `next`,
   `orient` and the no-arg `claim` scope to task-trees by default, so "what is
   next in my plan" never surfaces a bug, and a triage view asks the opposite
   question deliberately. Without this, an issues root still works — via `move`
   and `focus` — but correctness depends on remembering to scope, rather than
   the default being right.

2. **A `found-in` cross-reference.** An issue permanently records the leaf that
   surfaced it *without being parented by it*. This is the whole fix for the
   stated problem: provenance survives, blocking does not.

3. **Nothing else yet.** No severity field, no triage states, no reporters.
   `label` already covers severity and component, and `ls --label` already
   answers "what is broken in this component, across all plans" — the one
   question a tree shape cannot answer natively and the main reason people reach
   for a tracker.

`job move <id> under <new-parent>` already exists, so today's workaround — a
long-lived issues root plus `move` — works without any change at all. The
proposal above is what turns that convention into something the tool understands.

## The design question worth settling first

An issue usually **spawns** work, and work is a task. If the two universes are
hard-separated you inherit the issue↔PR dance from GitHub: two objects, one
piece of reality, and a permanent bookkeeping tax.

Preference: let an issue **own task children directly**, so it stays one object
with a lifetime of its own. The alternative — an issue that links to a separate
task tree — is more orthodox and worse to live with at this scale.

This should be decided before any of the above is built, because it determines
whether "tree kind" is a property of the root only, or of every node.

## Cost, honestly

A long-lived issues root grows monotonically, and without the tree-kind change it
will start appearing in `next` and `orient`. `focus` scopes around it, but that
is friction you feel every session. That friction is the actual argument for
doing (1) rather than living with the convention indefinitely.

## Import block

The YAML below is `job import`-ready if this is taken up. It deliberately puts
the open design question first, since it gates the rest.

```yaml
tasks:
  - title: Issues as a first-class lifetime in Jobs
    desc: |
      Discovered defects currently hold plan trees open, because job puts provenance (where a bug was found) and blocking (what cannot close until it is fixed) on the same parent edge. For planned work those always coincide; for a defect they must come apart. See project/2026-07-25-issues-vs-tasks.md for the full reasoning, including why this is NOT a second binary.

      Scope discipline: no severity field, no triage states, no reporters. Labels already cover severity and component, and `ls --label` already answers "what is broken in this component". Fields are what make trackers heavy.
    labels: [proposal, issues]
    children:
      - title: DECISION — can an issue own task children directly?
        ref: issue-children
        desc: |
          Gates everything below, because it decides whether "kind" is a property of the root only or of every node.

          An issue usually SPAWNS work, and work is a task. Hard-separating the two universes inherits GitHub's issue-vs-PR dance: two objects for one piece of reality, and a permanent bookkeeping tax. The proposal's preference is to let an issue own task children directly so it stays ONE object with its own lifetime. The orthodox alternative — an issue that links to a separate task tree — is more conventional and worse to live with at this scale.
        labels: [decision]
        criteria:
          - The decision is recorded with its reasoning, and states whether tree kind attaches to roots only or to every node
      - title: Tree kind — task-tree vs issue-tree
        ref: tree-kind
        blockedBy: [issue-children]
        desc: |
          Mark a root as a task-tree or an issue-tree, and scope the default readers accordingly: next, orient and the no-arg claim answer "what is next in my plan" without surfacing issues, while a triage view asks the opposite question deliberately.

          This is what turns the existing workaround (a long-lived issues root plus `job move`) into something the tool understands. Without it the workaround still functions, but correctness depends on the operator remembering to `focus` every session.
        labels: [core]
        criteria:
          - A root can be marked as an issue-tree, and the marking is visible in ls and show
          - next, orient and no-arg claim exclude issue-trees by default, and there is an explicit way to ask for them
          - An existing tree can be converted without losing history
      - title: found-in cross-reference
        ref: found-in
        blockedBy: [issue-children]
        desc: |
          An issue permanently records the leaf that surfaced it WITHOUT being parented by it. This is the actual fix for the reported problem: provenance survives, blocking does not.

          It must survive the leaf being closed — the common case is precisely that the plan finishes while the bug does not.
        labels: [core]
        criteria:
          - An issue records the leaf it was found in, and that reference survives the leaf closing
          - The reference is visible from both ends, and creates no blocking relationship
      - title: Migrate existing stragglers onto an issues root
        blockedBy: [tree-kind, found-in]
        desc: |
          Sweep open trees for leaves that are really discovered defects rather than planned work, move them under the issues root, and record found-in for each. `job move <id> under <new-parent>` already exists, so this is a data pass, not a feature.

          Do this LAST: doing it before found-in exists would lose the provenance the move is meant to preserve.
        labels: [migration]
        criteria:
          - Every moved leaf carries a found-in reference to where it came from
          - No tree is left open solely because of a defect leaf
```
