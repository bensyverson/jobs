# `job init`: require an identity, fix the gitignore hint, add `job gitignore`

*2026-08-29. Prompted by a transcript of `job init --default-identity claude` in a fresh repo; decisions talked through with Ben the same day. The import block at the end is the plan.*

## What is wrong today

The hint `init` prints after creating the database:

```
Recommended .gitignore entries:
  .jobs.db          # Jobs event store (local by default; remove this line to share it)
  .jobs.db-shm      # SQLite WAL index (always local)
  .jobs.db-wal      # SQLite WAL journal (always local)

Run: job init --gitignore  to write these for you.
```

1. gitignore has no trailing-comment syntax, so each line as printed is one literal pattern that matches nothing. The *writer* (`gitignoreHasEntry` in `internal/job/gitignore.go`) already documents this trap; the hint string was hand-typed separately and drifted.
2. The lines are indented, so they can't be pasted as-is.
3. The comments belong above the patterns, not beside them.
4. The suggested fix is `job init --gitignore` — advice that arrives one command too late, and useless to an agent, which initializes a repo once per session at most.

Separately, `init` records `$USER` as the default identity unless told otherwise. In the dominant case — an agent running `init` — that is the wrong name (`ben` for work done by an assistant), and it is wrong silently: the identity that every later `add`, `note` and `done` is attributed to.

## Decisions

Each one records the alternative it beat.

1. **`init` requires `--as <name>` and records it as the default identity.** Beat keeping `--default-identity` with a `$USER` fallback: the fallback is wrong for the dominant caller and quietly so. `--as` is already the global identity flag, so `job init --as claude` reads the same as every other attributed write. `--default-identity` is retired. Validation happens *before* `CreateDB`, so a wrong call leaves nothing behind to `--force` over.
2. **The no-default case keeps its existing name: `--strict`.** Beat `--no-default-identity`: the fact already has a name — `job identity strict on|off` toggles it and `job status` reports it — and two names for one fact is the drift the typed-constant principle warns about. Discoverability comes from the error message instead: `identity required. Pass --as <name> (writes without --as are attributed to it), or --strict to require --as on every write.`
3. **The help text steers an agent to its own name without naming any vendor.** The line, roughly: *Use the name of whoever is running this command — a person's handle, or for an automated assistant, the assistant's own name, not the account it runs under. `$USER` is the human who launched the session, which is usually not who is doing the work.* Reads naturally to a person, unmistakably to an agent.
4. **The hint is rendered from `jobGitignoreEntries`**, so what a reader pastes is byte-identical to what the verb writes. Beat fixing the string in place: that is how it drifted the first time.
5. **A `job gitignore` verb replaces `init --gitignore`.** Idempotent, appends only the missing patterns, needs no `--as` (it is not an event-store write), locates the directory from `--db`. Beat keeping both: BUILD stage, and one path is easier to document than two.
6. **The hint prints only when it is actionable**: a `.git` directory exists at the database's directory and at least one pattern is missing. Beat printing it always: an agent initializing outside a repo shouldn't be told to edit a `.gitignore`.

Parked (see `project/backlog.md`): a `job status` line reporting whether `.jobs.db` is actually ignored. Useful, but a `git` shell-out in a read verb, and the hint at `init` covers most of it.

## Specification

### `job init --as <name> [--strict] [--force]`

- Without `--as` and without `--strict`: error (message in decision 2), exit 1, no database created.
- With `--as`: create the database, then `Default identity: <name>`. The `(from …)` source suffix goes away; there is only one source now.
- With `--strict`: create the database in strict mode; `--as` is ignored if present. Output unchanged: `Strict mode: writes require --as <name> (no default identity).`
- `--force` unchanged.
- `--gitignore` and `--default-identity` are removed; passing them is a cobra unknown-flag error.

Help text (`Long`) covers: what the default identity is for, decision 3's guidance on choosing the name, `--strict`, and a one-line pointer to `job gitignore`.

### The hint

Printed after the identity line when decision 6's conditions hold:

```

Add to .gitignore (or run: job gitignore):

# Jobs event store — local by default; delete this line to share it
.jobs.db
# SQLite WAL sidecars — always local
.jobs.db-shm
.jobs.db-wal
```

Rendered by a `GitignoreHint(missing []string)` function in `internal/job/gitignore.go` over the same entries the writer uses; the comment for the two WAL sidecars is shared so they print as a pair. The `job gitignore` writer emits the same block (under its existing `# job` header) so a file written by the verb and a file pasted from the hint are identical.

### `job gitignore`

- Appends the missing patterns to `.gitignore` in the directory of the resolved database path (the same resolution `init` uses), creating the file if absent.
- Prints `Wrote N entries to .gitignore: .jobs.db, …` or `.gitignore already includes .jobs.db, .jobs.db-shm and .jobs.db-wal`.
- No `--as` required; no event recorded.
- Does not require the database to exist — the directory is what matters — so it works before or after `init`.

### Docs

`docs/content/docs/getting-started/initialize.md`, `concepts/identity.md`, `reference/setup.md`, `DOCS.md`, `README.md`, `project/agents/jobs.md` and `scripts/verify-getting-started.sh` all show `job init` bare or with `--default-identity`; every one changes. `concepts/identity.md`'s `# records $USER as default` line becomes false the moment this lands and must go with it.

## Plan

```yaml
tasks:
  - title: init identity and gitignore
    desc: |
      Make `job init` require `--as` and record it as the default identity; render the gitignore hint from the entry table in a copy-pasteable form; replace `init --gitignore` with a `job gitignore` verb. Spec and the alternatives each decision beat are in project/2026-08-29-init-identity-and-gitignore.md — read it before starting any leaf.
    labels: [cli, init]
    children:
      - title: Render the gitignore hint from the entry table
        ref: hint
        desc: |
          Replace the hand-typed `GitignoreHint` constant with a function over `jobGitignoreEntries` that renders the block in the doc's "The hint" section: patterns unindented, comments on their own line above the pattern(s) they describe, the two WAL sidecars sharing one comment. The writer emits the same block under its `# job` header so a pasted hint and a written file are identical. Start with a test that the rendered hint contains no line that is both a pattern and a comment, and a test that writer output and hint output agree line-for-line.
        labels: [cli, init]
        criteria:
          - No hint line carries a pattern and a trailing comment together
          - Hint lines are unindented and paste into .gitignore unchanged
          - The writer's block and the hint's block are the same lines
      - title: Add the job gitignore verb and retire init --gitignore
        ref: verb
        blockedBy: [hint]
        desc: |
          New `cmd/job/gitignore.go`: `job gitignore` appends the missing patterns to `.gitignore` in the resolved database's directory, creating the file if absent, and prints the wrote/already-includes summary. No `--as`, no event, no requirement that the database exist. Remove the `--gitignore` flag from `init`. Existing tests for the flag move to the verb; add one for the no-database case.
        labels: [cli, init]
        criteria:
          - job gitignore writes only the missing patterns and is idempotent on a second run
          - job gitignore works in a directory with no .jobs.db
          - job init --gitignore is an unknown-flag error
      - title: Require --as on init and retire --default-identity and the $USER fallback
        ref: identity
        desc: |
          `init` fails before `CreateDB` when neither `--as` nor `--strict` is given, with the message in decision 2 — reuse the existing identity-required helper rather than minting a third message. `--as` sets the default identity; the `(from …)` suffix goes. `--default-identity` is removed. Help text follows decision 3 and names no vendor. Rewrite the `identity_test.go` init cases: the `$USER` tests become "no database is created and the error names --as and --strict".
        labels: [cli, init]
        criteria:
          - job init with no flags exits non-zero, names --as and --strict, and creates no .jobs.db
          - job init --as <name> records <name> as the default identity
          - job init --strict still creates a strict-mode database
          - job init --default-identity is an unknown-flag error
          - The help text mentions no vendor or product name
      - title: Print the hint only when actionable
        blockedBy: [hint, verb, identity]
        desc: |
          After the identity line, `init` prints the rendered hint only when a `.git` directory exists in the database's directory and at least one pattern is missing from `.gitignore`. Preceded by a blank line and the "Add to .gitignore (or run: job gitignore):" lead. Tests cover: no .git → no hint; .git and all patterns present → no hint; .git and one missing → hint.
        labels: [cli, init]
        criteria:
          - No hint outside a git repository
          - No hint when .gitignore already carries every pattern
          - The hint's lead line names job gitignore, not init --gitignore
      - title: Update the docs, README, agents guide and getting-started script
        blockedBy: [verb, identity]
        desc: |
          Every `job init` example in docs/content/docs (initialize, identity, setup), DOCS.md, README.md, project/agents/jobs.md and scripts/verify-getting-started.sh moves to `job init --as <name>`; `--default-identity` and `init --gitignore` disappear; `job gitignore` gets its own entry in the reference and in initialize.md. Delete the `# records $USER as default` claim in concepts/identity.md. Run scripts/verify-getting-started.sh against the rebuilt binary.
        labels: [docs, init]
        criteria:
          - No doc, README or script mentions --default-identity or init --gitignore
          - job gitignore is documented in the command reference and the initialize page
          - scripts/verify-getting-started.sh passes against the rebuilt binary
```
