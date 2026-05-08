---
title: Plan grammar
weight: 4
---

`job import` reads a Markdown file, finds the first fenced YAML block whose top-level key is `tasks:`, and creates every task in one transaction. This page walks the grammar by example. The [JSON Schema](schema/) page is the machine-checkable spec, generated directly from `job schema`.

## A minimal plan

`plan.md`:

```markdown
# My plan

Some introductory prose. The importer ignores everything outside the first
fenced YAML block whose top-level key is `tasks:`.
```

…followed by the fenced YAML block itself:

```yaml
tasks:
  - title: Build login
  - title: Build signup
```

Two roots, no nesting, no labels, no anything. Every other field is optional. The Markdown above the YAML fence — heading, prose, anything else — is ignored by the importer; it's there for human readers.

## Children, descriptions, and labels

```yaml
tasks:
  - title: Build login
    desc: |
      OAuth-only. We're targeting GitHub for the launch and adding Google in
      the next iteration.
    labels: [auth, frontend]
    children:
      - title: Wire the OAuth callback
        desc: Use the github strategy from passport.
      - title: Add the login button
        labels: [ui]
```

Notes:

- `desc` is free text. Paragraphs separate with a blank line. The schema's hint to "assume the reader is an agent with fresh context" is a good rule — the description is what someone (or something) sees when they `claim` the task with no other context.
- `labels` is flat and free-form. There's no inheritance — children don't pick up their parent's labels automatically. List the ones you want on each node.
- `children` is recursive. The same grammar applies at every level.
- A parent should not carry its own work; if it would, model that work as a leaf child. The leaf-frontier rule (parents auto-close when their last child closes) makes that the cleanest shape.

## Acceptance criteria

```yaml
tasks:
  - title: Audit the logging pipeline
    criteria:
      - every request has a correlation id in the access log
      - error logs include the user id when present
      - PII fields are redacted before write
```

Each entry becomes a pending acceptance criterion on the task. They're created with the `pending` state; transitions land later via `job done --criterion label=passed` (or `--all-passed`, `--all skipped`, `--all failed`, `--force-close-with-pending`). See [Acceptance criteria](../concepts/criteria/) for the full lifecycle.

The short ids the CLI uses to address each criterion (`8jt`, `7wR`, etc.) are assigned at insert time — they don't appear in the plan. Look them up later with `job show <id>`.

## Cross-task blocking with refs

The same plan can declare blockers between tasks it's creating. There are three ways a `blockedBy` entry resolves, in order:

1. **A `ref:` defined elsewhere in this import.** The cleanest form. Refs are author-chosen handles, unique across the document, never persisted on task rows.
2. **A verbatim task title elsewhere in this import.** Must be unambiguous; if two siblings share the title, the import errors out.
3. **A pre-existing short id in the database.** This is how a plan ties new work to tasks that already exist.

```yaml
tasks:
  - title: Refactor the parser
    ref: parser-rewrite
    children:
      - title: Land the new lexer
        ref: lexer
      - title: Land the new tokenizer
        ref: tokenizer
        blockedBy: [lexer]
      - title: Land the new AST
        blockedBy: [lexer, tokenizer]

  - title: Migrate consumers to the new parser
    blockedBy: [parser-rewrite]              # ref → entire subtree of work

  - title: Decommission the old parser
    blockedBy: [Migrate consumers to the new parser]   # title resolution

  - title: Update CHANGELOG
    blockedBy: [abc12]                       # short id from a prior import
```

A few facts that aren't obvious from the schema:

- **The whole import is atomic.** A typo in row 47 reverts rows 1 through 46. Pair the first run with `--dry-run` to see what *would* land.
- **Cycles are detected across the full input set.** Two new blockers that would form a loop with each other (or with an existing edge in the database) cause the entire import to fail.
- **Refs scope to one import.** A ref defined in one plan is not addressable from another plan — the next plan must use the resulting short id, or re-use a verbatim title.
- **Blockers auto-clear when the blocker is `done`.** You only need `block remove` for edges you want to drop while the blocker is still open.

## Importing under an existing parent

By default, the plan's roots become roots in the database. With `--parent <id>`, every root is nested under that parent instead — useful when an agent wants to drop a phase plan into the parent it was scoped from.

```sh
job import phase-2.md --parent abc12
```

Inside the plan, `blockedBy` entries that point at short ids work as before — they're resolved against the database, not the import. So a plan can refer to existing work on the way in.

## A complete worked example

Putting every field together. This is the kind of plan you'd hand to `job import phase-1.md`:

```yaml
tasks:
  - title: Phase 1 — auth
    ref: phase-1
    desc: |
      OAuth-only login and signup. Targeting parity with the v1 password flow
      so we can flip the feature flag without UX regression.
    labels: [auth, p0]
    criteria:
      - login round-trip works in staging with a fresh GitHub account
      - signup creates the user row, organization row, and default project
      - error path returns the user to /login with a flash message
    children:
      - title: Wire OAuth callback
        ref: oauth-callback
        labels: [backend]
      - title: Build the /login page
        ref: login-page
        labels: [frontend]
        blockedBy: [oauth-callback]
      - title: Build the /signup page
        labels: [frontend]
        blockedBy: [oauth-callback]
        criteria:
          - "submitting the form posts to /signup"
          - "duplicate-email error renders inline"

  - title: Phase 2 — billing
    blockedBy: [phase-1]
    desc: Stripe Checkout flow. Out of scope for the v1 launch but staged here.
```

Validate it without writing:

```sh
job import phase-1.md --dry-run
```

Then commit:

```sh
job import phase-1.md
```

## Where the spec lives

This page describes the grammar; [`job schema`](schema/) emits the live JSON Schema generated from the same Go types the importer uses. When this page disagrees with the schema, trust the schema and file an issue — the schema is the source of truth.
