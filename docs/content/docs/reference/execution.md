---
title: Execution
weight: 3
---

The active-work loop: `claim`, `release` (alias `unclaim`), `note`, `done`, `reopen`, `cancel`, `heartbeat`. Together they're the lifecycle of a single piece of work.

## `claim`

Grabs a task and holds it for a TTL (default 30m, supported units `s|m|h|d`). The first line of the ack is a one-liner suitable for parsing; the rest is the same briefing `show` would print, so a follow-up `show` is rarely needed.

```sh
job claim abc12                            # 30m default
job claim abc12 2h                         # long-running claim
job claim --next                           # find and claim the next available leaf
job claim --next infra-root                # restrict the search to a subtree
job claim --next 2h                        # combine: next leaf, longer hold
job claim abc12 --force                    # override an existing claim
job claim abc12 -q                         # one-line ack only, no briefing
job claim --next --issues                  # claim the next open issue instead
job claim abc12 -m "what I'm trying"       # starting note, same transaction
job claim --next -F plan.md                # ... including on the leaf --next picks
```

The `--next` modes:

- `claim --next` walks the leaf frontier in plan order and atomically claims the first available one. With a [focus](#focus) set, the no-argument walk stays inside your focused root; an explicit parent argument searches that subtree regardless of focus. The race-safe contract is: if two agents call `claim --next` at the same moment, exactly one wins each leaf — the loser gets the *next* leaf, not an error. This is the canonical spawn pattern for parallel agents.
- `--include-parents` widens the walk to include any available task, not only leaves. Reach for it only when you genuinely want to claim a task that has open children — usually you don't.
- `--issues` points the no-argument walk at [issue-trees](../../concepts/tree-kinds/) instead of task-trees. Without it, `claim --next` never hands you a bug — it answers "what is next in my plan". Like `next --issues`, it scopes to your [issue focus](#focus) when you have one and walks every issue-tree when you don't. An explicit parent argument overrides the default without the flag.
- `-m "<text>"` / `-F <path>` work the same under `--next` as on `claim <id>`: the starting note is recorded on whichever leaf the walk picks, in the same transaction as the claim. A malformed body aborts before anything is claimed.

Idiomatic combination: `job claim --next 1h && do-the-work && job done <id> --claim-next`. See `done --claim-next` below.

## `focus`

Your **focus** (active root) is the tree that scopes every no-argument default: bare `claim --next`, `job next`, `status`'s Next: hint, and `orient`'s target all stay inside it. It exists so a blocked or paused tree elsewhere in the forest never silently hands you a leaf from the wrong plan.

**Focus is held per [tree kind](../../concepts/tree-kinds/)** — one task-tree and one issue-tree, independently — so stopping to triage a bug never loses your place in the plan. The plan-shaped defaults read your task focus; the same verbs with `--issues` read your issue focus.

```sh
job focus                                  # show both, one line per kind
job focus qP4nR                            # set it (the root's kind picks the slot)
job focus --release                        # release both (no-arg defaults go global)
job focus --release --issues               # release only the issue focus
```

```text
Task:   zwrjL "Documentation site" — 3 of 8 done, 2 available
Issues: (none)
```

The rules, in claim order:

- **Claiming is the usual setter.** Any successful claim outside your focused root of that root's kind flips that focus to the claimed task's root (`focus_set` in the event log, carrying the kind it was set for) — last claim wins, no ceremony. `job focus <id>` is the explicit form; it accepts any task in the tree and focuses its root.
- **Each kind is its own slot.** Claiming inside an [issue-tree](../../concepts/tree-kinds/) moves your issue focus and leaves your task focus standing, and vice versa.
- **Focus is per-actor.** Two agents sharing a database each keep their own lane; one agent switching trees never moves another's defaults.
- **It releases itself.** When a focused root completes (including by cascade) or is canceled, that kind's focus releases automatically and the other kind is untouched. `focus --release` is the manual version — the "pause this tree" case.
- **Exhaustion fails loudly.** When your focused root has no available leaf, the matching no-arg `next`/`claim --next` return an error naming the root and the escapes — claim in another tree to shift focus, or `focus --release` — instead of silently crossing into a different plan.
- **Explicit arguments always win.** `claim --next <id>`, `next <id>`, `orient <id>`, and `status <id>` behave exactly as if focus didn't exist.
- **`--issues` follows your issue focus.** With one set, `next --issues` / `orient --issues` / `claim --next --issues` stay inside it; with none set they walk every issue-tree. A focus on an issue root no longer redirects the plain, plan-shaped defaults — that is what the per-kind split bought.

## `release` (and its alias `unclaim`)

Drops your claim, returning the task to `available`. Optionally records a parting note.

```sh
job release abc12
job release abc12 -m "Handing off — context in latest note."
job release abc12 -F handoff.md                      # parting note from a file
```

Only the holder can release without `--force`. If you want to take a task away from another agent, that's `claim --force`, not `release --force`.

## `note`

Append a timestamped note to a task. Notes are events (actor + body), not edits to the description, and they remain visible in `show` and `log`.

```sh
job note abc12 "Quick observation — the schema mismatch is in column 3."
job note abc12 -m "Same as above, with the flag form."
job note abc12 -F /path/to/long-message.md           # read body from a file
job note abc12 -m @/path/to/long-message.md          # the older spelling of the same thing
echo "from a pipe" | job note abc12 -                # positional - reads stdin
echo "or this way" | job note abc12 -m -             # -m - reads stdin
echo "or this way" | job note abc12 -F -             # -F - reads stdin
job note abc12 -m "ack" --result '{"errors":0}'      # attach structured JSON
```

`-F <path>` is the file form, spelled as `git commit -F` spells it. Combining it with `-m` or with the positional body is an error — pass the body one way.

The `--result` payload rides on the `noted` event and is preserved in JSON output of `log` and `tail` — that's how an agent passes a structured handoff to whatever's watching.

If the caller currently holds a claim on the task, the note auto-extends the claim's TTL. Heartbeat is for genuine pauses; if you're writing notes, you don't need to heartbeat.

## `done`

Closes one or more tasks atomically. The killer flag is `--claim-next`, which collapses close-and-advance into one event.

```sh
job done abc12
job done abc12 -m "Closing notes here."
job done abc12 -F /path/to/release-notes.md            # body from a file (or -m @path)
job done abc12 abc34 -F notes.md                      # one body applied to every id
job done abc12 abc34 abc56 -m "Closing the batch."   # multi-id, atomic
job done abc12 --cascade                              # close abc12 + all open descendants
job done abc12 --claim-next                           # close, then claim the next leaf in this root
job done abc12 --claim-next -q                        # close + claim, no follow-on briefing
job done abc12 --claim-next --under xyz99              # scope the follow-on claim to another subtree
job done abc12 --claim-next --any                      # claim the next leaf repo-wide (old global behavior)
job done abc12 --all-passed                           # mark every pending criterion passed
job done abc12 --all skipped                          # or skipped, or failed
job done abc12 --criterion "8jt=passed" --criterion "7wR=skipped"
job done abc12 --force-close-with-pending             # close anyway; record waiver
```

Things to keep in mind:

- **Idempotent.** A re-`done` on a closed task reports the existing state without recording a new event.
- **Multi-id close is atomic.** Either every id closes or none do. For multi-id closes, `--criterion` takes the long form `id:label=state`.
- **Pending criteria block the close.** Either resolve them (`--criterion`, `--all-passed`, `--all`) or wave them through with `--force-close-with-pending`. The waiver is recorded on the `done` event so it's visible later.
- **Auto-cascade.** Closing the last open child of a parent auto-closes the parent. The chain continues to the root, attributed to whichever agent closed the final leaf.
- **`--claim-next` is race-safe.** The close and the next claim are one transaction; two agents racing the same `done --claim-next` cannot both end up claiming the same follow-on leaf.
- **`--claim-next` is scoped to the closed task's root by default.** It claims the next available leaf within the root subtree of the task you just closed, so a focused session never gets handed an unrelated leaf in a different root. Override with `--under <id>` to target another subtree, or `--any` to restore the old repo-global next-leaf behavior.
- **The trailing `Next:` hint skips issue-trees.** The walk prefers forward siblings, then earlier ones, stepping up the closed task's ancestor chain before crossing into another root tree. When it crosses, it considers task-tree roots only — the same default `next`, `orient` and `claim --next` use — so closing planned work never hands you a bug report as "what's next". Inside the closed task's own tree nothing is filtered, whatever its kind.
- **`done` reports the issues it surfaced, it does not refuse on their account.** If the closed task has a [found-in](../../concepts/found-in/) edge to issues that are still open, the ack adds `Surfaced: N open issues — id "title", id "title"` (singular `1 open issue`) right after that task's closing line. Issues already `done` or `canceled` are excluded, and the line is omitted when none are open. Closing multiple ids at once prints the line once per closed task, naming only the issues that task surfaced. `--format=json` carries the same list on each closed entry as `surfaced_open`.

## `reopen`

Brings a closed task back to `available` and, by default, claims it for the caller.

```sh
job reopen abc12                                    # auto-claim after reopen
job reopen abc12 --no-claim                         # leave it unclaimed
job reopen abc12 --cascade                          # also reopen done descendants
```

The auto-claim default exists because reopening usually means "I'm picking this back up." `--no-claim` is the "I'm just resurrecting it for someone else" form. `--cascade` brings the whole subtree back; without it, only the named task is reopened.

## `cancel`

Non-destructive close that records *why*. `--reason` is required.

```sh
job cancel abc12 -m "Out of scope — moved to next quarter."
job cancel abc12 abc34 -m "Both blocked on a vendor we dropped."
job cancel abc12 --cascade -m "Whole subtree no longer needed."
job cancel abc12 -F reason.md                       # reason from a file (or -m @path, or -F -)
job cancel abc12 --purge                            # erase the row and events
job cancel abc12 --purge --cascade --yes            # erase a whole subtree
```

`--purge` is the only destructive operation in the verb list — it removes the task row and its events outright instead of transitioning state. `--purge --cascade` requires `--yes` for exactly that reason. Reach for `cancel` without `--purge` whenever you can; it preserves the audit trail.

## `heartbeat`

Refreshes one or more live claims by 30 minutes and emits a `heartbeat` event. The contract is strict: every named task must currently be claimed by the caller, or the entire call rolls back.

```sh
job heartbeat abc12
job heartbeat abc12 abc34 abc56                     # variadic, atomic
```

You rarely need this. Any write to a claimed task by its holder — `note`, `edit`, `label add`, `label remove` — already auto-extends the TTL. Heartbeat is the "thinking, not writing" tool: long pauses where you're not yet ready to commit anything but want the lock held.
