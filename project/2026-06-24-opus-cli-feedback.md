# `job` CLI experience report — 2026-06-24

Date: 2026-06-24
Author: Claude (Opus 4.8), used cross-repo — drove a multi-leaf TDD batch in the **[redacted]** repo (`../redacted`) with `job` as the primary tracker, not working in this repo.
Scope: ergonomics observed while taking a five-leaf "resilience batch" from plan → red/green/commit per leaf, using `job` to import the tree, claim leaves, and close them. Not a comprehensive review — just what I bumped into in one real session.

## Context

I picked up a handoff in [redacted], planned a five-part change (benign-skip classification for two connectors, progress logging, an optional sweep timeout, docs), imported it as a parent + 5 leaves, and worked them one at a time: claim → write failing tests → implement → full suite → commit → `done`. So this is feedback from using `job` exactly as intended for staged TDD work, end to end.

## What worked

- **`job status` as the session opener earns its keep — again.** It was the first thing I ran against an unfamiliar handoff, and "10 open, 194 done · last activity 35m ago" + the per-root rollup + `Next:` told me instantly that the two parent jobs the handoff claimed were done actually *were* done. It's the cheapest possible trust-check on a handoff note, and it doubles as the landscape briefing. Keep it dense.
- **`import --dry-run` is the right gate.** I rendered the tree (`<new-1>`…`<new-6>`) and eyeballed the shape before materializing it. For anything bigger than a single task I'd never want to commit a tree blind, and the preview matched the committed result exactly.
- **Parent auto-close on the last leaf is still a delight.** Closing the fifth leaf returned `Auto-closed: rcbgd "Resilience batch + offboarded-organizer documentation"` in the same breath — I never had to walk back up the tree. This was called out as a win in the 2026-04-28 report and it's still pulling its weight.
- **`done -m` notes are a free commit-message draft.** Each leaf's completion note ("Zoom no-account 404 → ErrNoZoomAccount sentinel; …; full suite green; committed") was basically the work summary already written. The claim→note→done trail kept the tracker honest against what I'd actually committed, with zero side bookkeeping.
- **Per-leaf claim/done maps cleanly onto red/green/commit.** One leaf = one claim = one green commit = one `done`. The granularity the tracker wants and the granularity the TDD rhythm wants are the same granularity, so the tool stayed out of the way.

## Friction I hit

- **`--claim-next` is repo-global, and that's a footgun.** This is the big one. The 2026-04-28 report explicitly *asked* for `job claim --next` to "pick the next available leaf **under whatever I'm currently working on**." It shipped — but as a global next-leaf. After closing my first leaf with `done --claim-next`, it claimed `u7wKE`, an out-of-scope **phase-3 NOTE** in a completely different root, not the next leaf of the batch I was visibly working in. I had to `release` it and explicitly `claim` the right leaf. The feature is genuinely useful, but global scope makes it actively misleading inside a focused subtree: the obvious mental model is "next under *this*," and the default does "next anywhere." Either scope it to the working subtree by default, or add `--under <id>` / honor the most-recent claim's root.
- **`job import` requires a fenced ` ```yaml ` block, not raw `tasks:`.** My first import was valid YAML starting with `tasks:` and failed with `Error: no YAML 'tasks:' block found` — which points at the wrong thing, since the `tasks:` block was right there, just not fenced. I only recovered because the quickstart example happens to show the fence. Either accept a bare `.yaml`/`.yml` whose top level is `tasks:`, or make the error say what it means: "found a file but no fenced ```yaml block; wrap the tasks: block in a code fence (or pass a .yaml file)."
- **`job add`'s stdout isn't cleanly machine-parseable for the new ID.** I tried `job add ... --plain | grep` to capture the created ID and got nothing usable, which is what pushed me to `import` (fine here, since I wanted a tree anyway). But for the common "add one task and immediately claim it" flow, there's no obvious "print just the new ID" path. A `--quiet`/`--id-only` that emits the bare ID on stdout would make `job add` scriptable.

## Things I noticed but didn't need to solve

- The 30m claim default was, again, invisible during active work — I bumped my claims to 2h up front and never thought about it. Good default.
- Notes being append-only made the handoff trail readable after the fact, but (as the April report also noted) the *description* still isn't editable in-flight, so the "stable brief" and the "latest status" live in different places. Didn't block me this session; flagging it persists.

## Suggested changes

Grammar below uses the `job import` schema (`job schema`); titles complete "This task…" in present tense per the project's commit style.

```yaml
tasks:
  - title: Tighten claim-next scoping and import/add ergonomics from cross-repo TDD use
    desc: |
      CLI papercuts surfaced driving a 5-leaf TDD batch in another repo
      through `job` (2026-06-24). See
      project/2026-06-24-opus-cli-feedback.md for the full report.
    labels: [cli, dx]
    ref: cli-experience-2026-06-24
    children:
      - title: Scope `claim --next` / `done --claim-next` to the working subtree
        desc: |
          Today `--claim-next` picks the next available leaf REPO-WIDE. In a
          focused session that means closing a leaf can hand you an unrelated
          out-of-scope task in another root (observed: closing a resilience-batch
          leaf claimed a phase-3 NOTE in a different tree). The 2026-04-28
          experience report asked for "next under whatever I'm currently working
          on" — that's the intuitive model. Default `--claim-next` to the nearest
          ancestor root of the just-closed/most-recently-claimed task, and add an
          explicit `--under <id>` to override. Keep a `--next --any` escape hatch
          for the old global behavior.
        labels: [cli, dx]
      - title: Accept a bare `tasks:` YAML file in `job import`, or fix the error
        desc: |
          A file whose top level is `tasks:` (valid YAML, no markdown fence)
          fails with "no YAML 'tasks:' block found" — which is misleading, since
          the block is present, just unfenced. Either ingest a `.yaml`/`.yml`
          whose root key is `tasks:` directly, or reword the error to name the
          real cause: a fenced ```yaml block (or a .yaml file) is required.
        labels: [cli, dx]
      - title: Add an `--id-only`/`--quiet` output mode to `job add`
        desc: |
          For the common "add one task, then claim it" flow there's no clean way
          to capture the new ID — `job add` output isn't easily machine-parseable
          and `--plain` didn't yield a grabbable ID. Emit just the created task
          ID on stdout under `--id-only` so `ID=$(job add ... --id-only)` works.
        labels: [cli, dx]
```

## Net

`job` stayed out of the way and kept the plan honest against what I actually committed, which is the main thing I want from a tracker driving staged TDD. The one change I'd genuinely prioritize is **scoping `--claim-next` to the working subtree** — it's a shipped feature whose default does the surprising thing, and it's the only friction this session that caused a wrong action rather than just an extra keystroke.
