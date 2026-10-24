# `orient` in reading order: the work first, history bounded

*Design + implementation plan · 2026-10-24*

This builds on [2026-06-25-job-orient-command.md](2026-06-25-job-orient-command.md) (why `orient` regenerates the plan from live data rather than pointing at a plan doc) and [2026-07-03-orient-slimming-and-active-root.md](2026-07-03-orient-slimming-and-active-root.md) (done-node elision). Both still hold. What this changes is the order the output is read in and the two places it still grows without bound.

## Why

Measured on Penumbra's editor-push plan on 2026-10-24 (`job orient > orient.yaml`, then `scripts/orient-weight orient.yaml`): **83 KB**, a 147-node root tree with 19 open nodes.

| Where the bytes go | Size |
|---|---|
| Notes on the root alone: 63 notes, sessions 3–50 | 34.9 KB |
| Tree skeleton of 122 done leaves (title/id/status/closed, ~123 B each) | 15 KB |
| Notes on open leaves | 9.1 KB |
| Descriptions and criteria | ~13 KB |
| The `orient:` header | 0.4 KB |

Two findings follow.

1. **The output is ordered least-important-first for a reader who stops early.** The header carries a criteria *tally* and `weigh_notes` as bare ids; the target's description, criteria text and notes sit about 800 lines down, wherever the tree reaches it. The agent opening a Penumbra session read the first 40 lines of an 83 KB dump (a truncated tool result does the same) and so never read the target or the leaves it would delegate. A dump that big will be cut short; whatever survives the cut must be the work.
2. **Open nodes keep their whole note history.** The 07-03 elision is keyed on `status == "done"` (`buildOrientNode`, `internal/job/orient.go`), so every other node — open containers, available, claimed and blocked leaves, and canceled ones — prints every note it has ever had. A root used as a session log (63 "Session N starts here" breadcrumbs, mid-session status and a handful of rulings) is 42% of the output, and it grows every session.

Also found: a leaf blocked by the target renders `status: available` in the tree with no blocker, so the frontier can't be read from the tree.

## Converged decisions

### Reading order

The output is ordered so that reading any prefix of it gives the most useful context available at that length:

1. **The target in full**, in the `orient:` header: description, criteria with their text and state, and *all* of its notes. The target is the work; its history is the context it needs. (Today the header carries a tally and `own_notes`; the description and criteria text are only in the tree.)
2. **Its dependencies**: the leaves it is blocked by and the leaves it blocks, each with status and its latest note inline. These are explicit links, not just neighbors.
3. **Its siblings**, replacing `weigh_notes`' bare ids: one line each with id, title, status and note count, so a reader can choose which `job show` to run. Sibling-hood is a mechanical rule (a wide container gives fifteen siblings of uneven relevance), so their notes are not inlined.
4. **The tree.**

### Bounded history in the tree

- **An open node prints its latest note**, plus a count of the earlier ones and the command that shows them (`+41 earlier: job show uyhAEz`). The target is the exception: its notes are all in the header.
- **Canceled nodes are elided like done ones.**
- **A blocked leaf renders as blocked**, naming its blockers' ids.
- **A done leaf is one line**: title, id and close date. Same information as today's four fields at about 60% of the size.
- **A done container whose subtree is entirely done collapses**: it keeps its description (the plan narrative 07-03 kept it for) and a count of its done leaves, and prints no leaf rows. Done leaves under a container that is still open stay as one-liners: they are the target's neighbors and say what already exists.
- **`--full` is unchanged**: every node, every note.

Estimated on the measured store: 83 KB → ~45 KB from note bounding, ~39 KB with one-line done leaves, ~28 KB with collapsed done containers. The rest is the open plan, which is what `orient` is for.

### What stays

`orient` remains the live plan, not a pointer to a plan doc: a doc goes stale the day it's written, and the notes are what keep the tree current. The most recent completion note stays. Elision stays at `OrientView` assembly, not in the renderer, so the deferred md renderer inherits it.

### Out of scope, for the house rules

A ruling recorded only as a note can now fall out of view behind a later note. That is deliberate: a ruling other work builds on belongs in the design doc it rules on, with the note pointing there — which the shared jobs guide will say. A session breadcrumb on a root becomes harmless, since only the newest one prints.

## Work breakdown

```yaml
tasks:
  - title: "`orient` in reading order, with bounded history"
    desc: |
      Reorder `job orient` so any prefix of its output carries the most useful context available at that length — the target in full, then its dependencies, then its siblings, then the tree — and bound the two things that still grow without limit: note history on open nodes and done-leaf rows. Measured motivation and every decision: project/2026-10-24-orient-reading-order.md. `scripts/orient-weight` breaks a dump down by field and status; use it on a seeded fixture and on a real store to report before/after sizes.

      All of this lives in internal/job/orient.go (assembly) and internal/job/orient_yaml.go (rendering); elision stays at assembly so the deferred md renderer inherits it.
    labels: [orient, context-budget]
    children:
      - title: Bound note history on open nodes
        ref: notes-bound
        desc: |
          In buildOrientNode, a node that isn't done prints only its latest substantive note, plus the count of earlier ones and the `job show <id>` that shows them. Canceled nodes are elided exactly as done ones are (today only `done` elides). The target keeps every note (they move to the header in the next leaf). --full keeps every note on every node.
        criteria:
          - An open node with several notes renders only its latest, with the count of earlier notes and the job show command
          - An open node with one note renders it with no count
          - The target's notes are never truncated
          - A canceled node renders with no desc, notes or criteria, as a done one does
          - --full renders every note on every node
      - title: Show blocked leaves as blocked in the tree
        ref: blocked-status
        desc: |
          A leaf with an open blocker renders `status: blocked` and lists its blockers' short ids, so the frontier can be read from the tree. Today a leaf blocked by the target renders `status: available` with no blocker.
        criteria:
          - A leaf with an open blocker renders as blocked and names its blockers
          - A leaf whose blockers are all done renders as available
      - title: "Lead the header with the target, its dependencies and its siblings"
        ref: header-order
        blockedBy: [notes-bound, blocked-status]
        desc: |
          The `orient:` header carries, in order: the target's description, its criteria with text and state, and all its notes; then each leaf it is blocked by and each leaf it blocks, with status and latest note; then one line per same-parent sibling leaf — id, title, status, note count — replacing weigh_notes' bare ids. The tree follows. Goal: the first screen of output is the work, whatever cuts the output short.
        criteria:
          - The header carries the target's description, criteria text with state, and all its notes
          - The header lists the target's blockers and the leaves it blocks, each with status and latest note
          - The header lists sibling leaves one per line with id, title, status and note count, replacing weigh_notes
          - The target's description and criteria appear before any other task's content in the output
      - title: Collapse done work in the tree
        ref: done-collapse
        desc: |
          A done leaf renders on one line: title, id, close date. A done container whose whole subtree is done keeps its description and a count of its done leaves, and renders no leaf rows. Done leaves under a container that is still open stay, one line each. The most recent completion note still prints. --full is unchanged.
        criteria:
          - A done leaf renders on a single line with title, id and close date
          - A fully done container renders its description and done-leaf count and no child rows
          - Done leaves under an open container still render, one line each
          - The most recent completion note still renders
          - --full output is unchanged by this leaf
      - title: Document the new orient shape
        blockedBy: [header-order, done-collapse]
        desc: |
          Update docs/content/docs/reference/observation.md (the orient section: header fields, note bounding, blocked status, done collapse, --full) and orient's long help in cmd/job. Replace the weigh_notes description. Report before/after sizes from scripts/orient-weight on a real store in the done note.
        criteria:
          - The observation reference and orient's help describe the reading order, note bounding, blocked status and done collapse
          - The done note reports before/after orient sizes with the command that produced them
```
