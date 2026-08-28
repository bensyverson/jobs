---
title: Tree kinds
weight: 7
---

Every root task is one of two **kinds**: a **task-tree** (the default) or an **issue-tree**. The kind decides whether the tree answers "what is next in my plan".

## Why the distinction exists

A bug found while working a plan gets filed where it was found. It then holds that plan open long after the plan is otherwise finished, because a parent cannot close while a child is open.

Underneath that is a mismatch in how the two things complete. A plan is a **decomposition**: it has a bottom, and a parent closes when its children close. A defect is **encountered**: its lifetime is not bounded by the plan that surfaced it, and it closes on evidence — a regression test — rather than on structure.

An issue-tree is a place where that open-endedness is normal.

## Kind is a property of the root only

Children of an issue root are ordinary tasks. An issue **owns task children directly**, so it stays one object with one lifetime instead of the issue-and-PR pair other trackers make you maintain. Every verb — `claim`, `note`, `done`, `block`, `label`, `split` — works exactly the same inside an issue-tree.

Setting a kind on a non-root is an error rather than a silent no-op: only roots carry a meaningful kind, and a value no reader consults is worse than a refusal.

## Reading and setting the kind

```sh
job kind abc12                             # read: `abc12 "Bugs": issue-tree`
job kind abc12 issue                       # mark the root as an issue-tree
job kind abc12 task                        # convert it back
```

Conversion in either direction **loses nothing**. Nothing about the tree is touched except the kind, and the change lands as a `kind_changed` event carrying the before and after — so `job log` shows when a tree changed lanes and who moved it.

New roots can be born as issues:

```sh
job add "Flaky auth test on CI" --kind issue
```

`--kind issue` with a parent is an error, for the same root-only reason.

## What the default readers do

`next`, `orient`, and the no-argument `claim --next` answer "what is next in my plan", so they **skip issue-trees**. `job status`'s `Next:` hint and the trailing `Next:` hint on a `done` ack follow the same rule — the `done` hint skips issue-tree roots when it crosses out of the closed task's own tree, and never filters inside it. Pass `--issues` to ask the opposite question — the frontier across issue-trees — as a deliberate triage move:

```sh
job next --issues                          # the next open issue
job orient --issues                        # orient on it
job claim --next --issues                  # and take it
```

Three things override the default, because in each case you already named the tree you wanted:

- an **explicit id** — `job next abc12`, `job orient abc12`
- an **explicit scope** — `job next abc12 all`
- a [**focus**](../../reference/execution/#focus) set on an issue root — claiming inside an issue-tree flips your focus to it, and from then on your no-argument defaults stay there until you claim elsewhere or `job focus --clear`

`--issues` itself is forest-wide: like `next all`, it ignores focus, because it is the explicit "show me everything of this kind" form.

## Moving between lanes

`job move <issue-root> under <parent>` is **refused**. A root that gains a parent stops being a root, and its kind would silently stop meaning anything. Convert first, so the change is in the log:

```sh
job kind abc12 task
job move abc12 under def34
```

## Where the marking shows up

- `job ls` tags issue roots: `- [ ] `abc12` Bugs (issue-tree)`, alongside claims, blockers and labels.
- `job show` on an issue root prints a `Kind: issue` line. Task roots print nothing — `task` is the default and saying so everywhere would be noise.
- `job log` renders the conversion as `kind task-tree → issue-tree`.

## What tree kinds are deliberately not

There is no severity field, no triage state, and no reporter. [Labels](../labels/) already cover severity and component, and `job ls --label <name>` already answers "what is broken in this component, across all plans" — the one question a tree shape cannot answer natively. Fields are what make trackers heavy.
