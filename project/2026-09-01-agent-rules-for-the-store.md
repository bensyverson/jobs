# Agent rules for the store: proposed replacement text

*2026-09-01. Written while documenting [the git-native event log](2026-09-01-git-native-event-log.md) (leaf 7W8wv). `project/agents/jobs.md` is a generated region shared across repos via the `agents` CLI, so this note proposes the edit rather than making it. **Flagged for the shared-rules review.***

## What changed underneath the rule

`.jobs.db` stopped being the record. The record is now `.jobs/log/*.jsonl` — one append-only JSONL file per replica, **tracked in git**. `.jobs.db` is a disposable cache rebuilt from it on open. A replica is one checkout on one machine, and a git worktree is its own replica.

That matters for one clause of the current rule: *"`.jobs.db` is usually gitignored, so it does not exist inside a worktree."* Still true — `job gitignore` writes `.jobs.db*`. But the sentence now implies the tracker is unreachable from a worktree, and that is no longer the case: a worktree checks out `.jobs/log/`, so a worktree-local `job` command would build its own cache from the committed log and work, minting its own replica id.

We are **not** recommending that. It trades the integrator's live `job tail` on the main checkout for isolation, and a leaf's events would only become visible on the squash-merge. The absolute `--db` remains the rule. But the *reason* has moved from "it cannot work" to "we choose not to", and a rule that gives a reason an agent can disprove in ten seconds is a rule an agent will talk itself out of.

## Current text

> - **Every subagent passes `--as <name>`, unique per agent, on every call**, plus an absolute `--db /abs/path/.jobs.db`. `.jobs.db` is usually gitignored, so it does not exist inside a worktree and a relative path fails there. Never "fix" that with `job init`; it creates a second, empty database.

## Proposed replacement

> - **Every subagent passes `--as <name>`, unique per agent, on every call**, plus an absolute `--db /abs/path/.jobs.db` naming the **main checkout's** database. The cache is gitignored, so a relative `--db` inside a worktree finds nothing; never "fix" that with `job init`, which creates a second, empty tracker. A worktree does check out `.jobs/log/`, so a worktree-local command would build its own cache and mint its own replica — deliberately not what we do, because the integrator's `job tail` on the main checkout is how a dispatch is watched, and a worktree-local claim would not be visible until the merge.

## What changes for agents today

**Nothing.** Same flags, same paths, same claim/note/release loop. Two facts are worth an agent knowing anyway, and both are covered in [the store concepts page](../docs/content/docs/concepts/the-store.md):

- `.jobs/log/` is tracked. An agent editing files in the main checkout can see `.jobs/log/<rep>.jsonl` in `git status` and should leave it to the integrator's commit, exactly like any other tracked change it did not make.
- `.jobs.db` is disposable. If a cache looks wrong, `job rebuild` is the fix, and it cannot lose anything. Deleting the cache is safe; deleting or hand-editing a log file is not.

`project/agents/harness.md`'s worktree section says *"gitignored files do not exist in a worktree — `.jobs.db`, `local/`, dev databases — so pass every such path absolute"*. That stays correct as written and needs no change.
