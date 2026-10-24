# Three papercuts from a multi-agent session: criterion wording, worktree replicas, claim length

*Decisions + plan · 2026-10-24*

From a process review of a long multi-agent session in another repo, where every delegated brief had to carry workarounds for these three. Each was confirmed against the code and this repo's earlier decisions before filing.

## 1. A criterion's text can't be reworded

`job edit --set-criterion "<label>=<state>"` changes only a criterion's state. When a plan moves or a ruling changes what "done" means, the workaround is to mark the stale criterion `skipped`, add a reworded one with `--criterion`, and `job note` why — so dead criteria pile up on the leaf for every later reader.

**Decision.** Reword a criterion in place, recorded as an event like every other write, so `job log` keeps the old wording. Its state is unchanged by a reword.

## 2. A worktree becomes its own replica without saying so

Since [the event log moved into git](2026-09-01-git-native-event-log.md), a worktree is its own replica by design: a bare `job` there builds a cache from the checked-out `.jobs/log/`, mints a replica, and its events ride the merge back. [The store rules](2026-09-01-agent-rules-for-the-store.md) keep agents on the main checkout's store anyway, so the integrator's `job tail` sees them live. The failure mode, recorded in [gotchas](gotchas.md) on 2026-09-26, is that the switch is silent: an agent that forgets `--db` writes for an hour to a replica nobody can see.

**Decision.** Keep the replica model; don't redirect to the main checkout, which would bake one workflow into the tool. Make the moment loud instead: when opening a store mints a new replica inside a linked git worktree, print a warning to stderr naming the main checkout's store and the `--db` that reaches it. A per-repo setting that chooses the behavior is deferred until the warning proves not enough.

## 3. Claims outlast nothing that builds slowly

Claims default to 30 minutes. In a repo where a build and test run under load take longer, the claim lapses mid-work and the agent's `job release` fails, so every brief there says `job claim <id> 2h`. A Go repo's claims can stay short.

**Decision.** A per-repo default claim duration, committed with the repo so every checkout and worktree shares it; an explicit duration on `claim` still wins. There is no committed per-repo configuration yet (`.jobs/local.json` is per replica), so where the setting lives is the first thing the leaf settles — ask before choosing.

## Work breakdown

```yaml
tasks:
  - title: Papercuts from multi-agent use — criterion wording, worktree replicas, claim length
    desc: |
      Three fixes whose workarounds every delegated brief had to carry. Decisions and reasoning: project/2026-10-24-worktree-and-criteria-papercuts.md. Each leaf is independent.
    labels: [agents-feedback, papercut]
    children:
      - title: Reword a criterion in place
        desc: |
          `job edit <id>` gains a way to replace a criterion's text, addressed by its current label (the flag's spelling is the leaf's call; match --set-criterion's "label=value" form if it reads well). The change is an event, so `job log` shows the old and new wording; the criterion's state is unchanged. Update the edit reference page and --help.
        criteria:
          - A criterion's text can be replaced by naming its current label, and its state is unchanged
          - job log shows the old and new wording
          - An unknown label exits non-zero with a clear message
          - The edit reference page and help describe the reword
      - title: Warn when a worktree's first open mints its own replica
        desc: |
          When opening a store mints a new replica and the working directory is inside a linked git worktree (git rev-parse --git-common-dir differs from --git-dir), print one warning to stderr: this worktree is now its own replica, its writes stay invisible to the main checkout's store until the branch merges, and `--db <main checkout>/.jobs.db` writes to the shared store instead. Name the real path. Don't change behavior, and don't warn on later opens of the same replica or outside a worktree. Update the multi-agent recipe and the store concepts page. See project/2026-10-24-worktree-and-criteria-papercuts.md §2 and the 2026-09-26 gotcha.
        criteria:
          - Minting a replica inside a linked worktree prints one stderr warning naming the main checkout's store path and the --db to use
          - No warning on a later open of the same replica, in a main checkout, or outside git
          - Behavior is otherwise unchanged and the command's stdout is unaffected
          - The multi-agent recipe and the store concepts page describe the warning
      - title: Per-repo default claim duration
        desc: |
          A repo can set the default claim duration, committed so every checkout and worktree shares it; `job claim <id> <dur>` still wins, and with no setting the default stays 30m. There is no committed per-repo config yet (.jobs/local.json is per replica): settle where the setting lives and how it is set before building, and ask Ben. See project/2026-10-24-worktree-and-criteria-papercuts.md §3.
        labels: [decision]
        criteria:
          - With the setting present, a claim with no duration uses it, in the main checkout and in a worktree
          - An explicit duration on claim overrides the setting
          - With no setting, the default stays 30m
          - The claim reference page and help describe the setting
```
