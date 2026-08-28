---
title: Planning
weight: 2
---

The verbs that shape the tree before — and during — work: `add`, `import`, `edit`, `block`, `move`, `kind`, `label`, `split`. Every one of them is a write that emits an event.

## `add`

Adds a single task. With a parent argument, it nests under that task; without, it lands at the root.

```sh
job add "Build login page"
job add abc12 "Wire up the OAuth callback"
job add abc12 "Wire up the OAuth callback" -d "Use the github strategy."
job add abc12 "Important task" -b xyz99             # insert before sibling xyz99
job add abc12 "Bake compliance" --criterion "audit log lines exist" \
                                --criterion "PII redacted in transit"
job add abc12 "Refactor parser" -l p0 -l infra
ID=$(job add abc12 "Then claim it" --id-only)        # capture just the new id
job add "Flaky auth test on CI" --kind issue         # a new issue-tree root
```

The non-obvious moves:

- `-F <path>` reads the description from a file (`-F -` reads stdin) — the painless way to attach a multi-line description without fighting shell quoting. It is an error alongside `-d`/`--desc`. Unlike the `-m` verbs, `--desc` itself stays strictly literal, so a description that begins with `@` is stored as typed.
- `--criterion` is repeatable and seeds [acceptance criteria](../../concepts/criteria/) on the new task. Add as many as you want; each lands as `pending` and is referenced by a short id later.
- `--before <sib>` (or `-b`) is the only way to position a new task without a follow-up `move`. Without it, new children land at the end of the parent's child list.
- `--parent` and the positional parent argument are interchangeable — useful for scripts that pass the parent as a flag.
- `--kind task|issue` sets the new root's [tree kind](../../concepts/tree-kinds/). It is only valid when creating a root — kind is root-only, so `--kind issue` with a parent is an error rather than a silent downgrade.
- `--id-only` makes `add` scriptable: stdout is exactly the new task's bare id, with the advisory lines (auto-release, child-count hint) suppressed, so `ID=$(job add … --id-only)` captures a clean value for an immediate `claim`. (Criteria are still attached; only the chatter is suppressed.)
- `add` is leaf-only behavior on the parent: the parent stays open, but its leaf status flips off as soon as the first child is added.
- The positional order is **strict**: `add <parent> <title>`. If the leading arg doesn't resolve as a short id, `add` errors with `add: no such parent …` and reminds you of the order. If you pass a single arg that *does* resolve as an existing short id (the "forgot the title" slip), `add` refuses with `add: ambiguous single arg …` rather than silently creating a root task literally named after the id. Both errors lead with a stable, greppable prefix and tell you the fix. To create a root task whose title happens to look like a short id, pass `--parent=""` to declare the literal-title intent.

For more than three tasks at a time, prefer `import` — it's atomic and roundtrips through `job schema`.

## `import`

Parses a file and creates every task in one transaction. It finds the first fenced YAML block whose top-level key is `tasks:`; if the file has no fenced block at all, a bare YAML document whose top level is `tasks:` (a plain `.yaml`) is imported directly.

```sh
job import plan.md
job import plan.md --dry-run                       # validate, no writes
job import plan.md --parent abc12                  # nest the import under abc12
job import plan.md --format=json                   # JSON ack with the new ids
```

What to remember:

- The whole import is atomic. A typo in row 47 reverts rows 1–46. The `--dry-run` ack tells you what *would* be created without touching the database.
- `--parent <id>` lets one plan import as a subtree of another. Useful when an agent wants to pull a phase plan into the parent it was scoped from.
- **Block selection is observable.** Import picks the *first* `tasks:` block, but it warns on stderr when the choice is ambiguous (more than one candidate block — naming the one used by line) or lossy (the chosen block carries keys outside the grammar, which are silently dropped). A Markdown file that merely *illustrates* output YAML can otherwise hijack the import; the warnings make that visible. They never block an otherwise valid import, and they fire under `--dry-run` too.
- **A bare `tasks:` file needs no fence.** Hand `import` a plain `.yaml` whose top level is `tasks:` and it's parsed directly — the Markdown fence is only required when the `tasks:` block is embedded in prose. A file with neither a fenced block nor a bare `tasks:` document fails with a message naming both accepted forms.
- The schema is exhaustively documented in the [Plan grammar](../../plan-grammar/) section, and `job schema` prints the live source of truth.

## `edit`

Replaces a task's title, description, or criteria. At least one of `--title`, `--desc`, `-F`, `--criterion`, or `--set-criterion` is required.

```sh
job edit abc12 -t "New title"
job edit abc12 -d "Replace the entire description body."
job edit abc12 -d ""                                # clear the description
job edit abc12 -F newdesc.md                        # new description from a file
cat newdesc.md | job edit abc12 -F -                # or from stdin
job edit abc12 --criterion "new criterion to add"
job edit abc12 --set-criterion "8jt=passed"         # mark an existing one passed
job edit abc12 --set-criterion "8jt=skipped"        # or skipped, or failed
```

Notes:

- `-d ""` is the only way to *clear* the description. Omitting `-d` leaves the existing one in place. `-F <path>` is the file form of `-d` (`-F -` reads stdin); passing both is an error, and `-F` on its own satisfies the "at least one flag" requirement.
- `--criterion` adds new criteria; `--set-criterion <label>=<state>` updates an existing one by its short id (visible in `job show`). States are `passed`, `skipped`, `failed`, `pending`.
- `edit` does not change a task's status, parent, or labels. Reach for `move`, `label`, `done`, or `reopen` for those.

## `block`

Manage blocking edges between tasks. Two subcommands.

```sh
job block add  L9G25 by Hn4Y2 VBF5u izrBW          # variadic — one transaction
job block remove L9G25 by Hn4Y2                    # release one edge
```

What's worth knowing:

- `block add` is **atomic and cycle-checked across the full input set**. If any blocker would create a cycle (including with another blocker in the same call), the whole call fails — none of the edges are added.
- Duplicate blockers in one call collapse to a single edge. Re-asking for an edge that already exists is reported, not re-recorded.
- A blocker auto-removes when its target is `done`. You only need `block remove` for edges you want to drop manually — for instance, a task you've decided no longer depends on its blocker.
- The bare `job block <blocked> by <blocker>` (no `add`/`remove`) still works as a deprecated alias for `block add` and emits a stderr notice. Prefer the explicit form.

## `found-in`

Record where a task was surfaced, without parenting it there and without creating a blocking edge.

```sh
job found-in qP4nR in kTuMb                        # the bug turned up while doing kTuMb
job found-in qP4nR --clear                         # drop the reference
job add 9xKmT "Router drops trailing slash" --found-in kTuMb
```

What's worth knowing:

- **One source per task.** Setting a new one replaces the old; the event keeps both ids, so the history still holds the earlier answer.
- **It gates nothing.** Both ends stay claimable and closable regardless of the other's state, and closing the source's tree never cascades into the task.
- **It survives the source closing** — done, canceled, canceled by cascade, or deleted. The exception is `cancel --purge`, which erases the row the reference points at.
- `job show` prints `Found in:` on the task and `Surfaced:` on the source; `--format=json` carries them as `found_in` and `surfaced`.
- A task cannot be found in itself. Longer loops are fine — nothing walks the edge.

See [Found-in](../../concepts/found-in/) for when to reach for this instead of hierarchy or a blocker.

## `move`

Reorder among siblings, or reparent under a new parent.

```sh
job move abc12 before xyz99                        # reorder among siblings
job move abc12 after  xyz99
job move abc12 under  newparent                    # reparent (lands at end)
job move abc12 under  newparent before xyz99       # reparent and position
```

Move never closes a task. Combined with `add --before` and `import --parent`, this is everything you need to refactor a plan in place after work has started.

Moving an **issue-tree root** under a parent is refused: a root that gains a parent stops being a root, and its [kind](../../concepts/tree-kinds/) would silently stop meaning anything. Run `job kind <id> task` first, so the conversion lands in the event log.

## `kind`

Reads or sets a root's [tree kind](../../concepts/tree-kinds/) — task-tree (the default) or issue-tree.

```sh
job kind abc12                                     # read the current kind
job kind abc12 issue                               # mark it as an issue-tree
job kind abc12 task                                # convert it back
```

- **Roots only.** Children of an issue root are ordinary tasks; setting a kind on a non-root is an error.
- **Nothing is lost either way.** Only the kind changes, and the change is recorded as a `kind_changed` event carrying the before and after.
- **It steers the default readers.** `next`, `orient`, `claim --next`, `status`'s `Next:` hint and the `done` ack's `Next:` hint skip issue-trees; `--issues` targets them instead. An explicit id, an explicit scope, or a focus on the issue root overrides that.
- A no-op set (asking for the kind the root already has) prints a confirmation and records nothing.

## `label`

Two subcommands, both variadic, both atomic.

```sh
job label add abc12 p0 infra
job label remove abc12 infra
```

Labels are flat and free-form (no inheritance, no hierarchy). They're idempotent: repeating a name is reported, not re-recorded, and one event covers the whole call.

The one reserved name is `decision`. Tasks carrying it surface as a `Decision:` line in `job status` until they're closed — the convention is to use them as small, durable markers for choices the team needs to keep in view.

## `split`

Subdivides a leaf into children. The parent must currently have no children.

```sh
job split abc12 "Wire backend" "Wire frontend" "Document"
```

The shape after split:

- `abc12` is no longer a leaf — its leaf-frontier role transfers to the new children.
- All three children inherit nothing automatically: no labels, no criteria, no description. They're fresh tasks under `abc12`.
- The parent will auto-close once all the new children close. If the parent had a claim, that claim is released as part of the split.

Use `split` when you discover a leaf is bigger than expected. To pile children onto an existing phase, use `add` instead.
