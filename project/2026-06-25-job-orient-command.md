# `job orient` — leaf-context orientation for fresh agents

*Design + implementation plan · 2026-06-25*

## Context — why this exists

The recurring pattern: hand a fresh agent the entire Markdown plan doc + its YAML
block, then have them run `job status` to pick up the next leaf. The doc gives
plan-level context; `job status` gives a one-line pointer (`next VBF5u`). But this
**loses every note prior agents left on closed leaves** — and those notes are the
living memory of the plan (e.g. a closed sibling's done-note: *"scripts/verify-getting-started.sh
re-runs the canonical sequence so an agent can spot drift"*). That convention is
nowhere in the static doc, because it was learned *after* the doc was written, and a
fresh agent has no reason to `job log` a sibling task to find it.

`job orient [id]` replaces the "paste the whole doc" practice with a **live,
notes-enriched regeneration** of the plan around a target leaf: the full tree (titles +
*full* descriptions), every substantive note folded onto its node, criteria as the
acceptance checklist, and a synthesized header that puts the actionable punchline first.

`job status` stays the **orchestrator's** opener (multi-root landscape briefing).
`job orient` becomes the **worker's** opener (deep single-leaf context). They are
complementary; we are deliberately *not* merging orient into status (keeps status terse,
preserves the token-efficiency choice).

## Design decisions (converged)

- **New verb, not a flag on `show`.** `job orient` bakes in the "next leaf" default and
  reads as the worker's session-opener. `show --ancestors` already renders the spine
  (with descriptions) but omits everything *off* the spine — siblings/cousins and their
  notes. That gap is the whole point.
- **Default scope = the whole ROOT tree** containing the target (full context — the doc
  the agent used to paste). The positional id is the **target** (what you're about to
  work on), not the scope. `--scope <id>` optionally limits rendering to a subtree for
  very large plans. Target and scope stay orthogonal.
- **`id` is a real key, not a comment.** The primary consumer is an agent that immediately
  runs `job claim <id>`; the most actionable field must be parseable, not hidden in a
  comment. (As a bonus, real keys make the dump *invalid* import grammar, preventing the
  foot-gun of re-importing a read dump and duplicating the tree.) Re-importability is a
  non-goal; the importable subset is untouched, so it stays a one-line option later.
- **First-class `orient:` header** (top-level sibling to `tasks:`), because the most
  synthesized/actionable content deserves parsing, not a comment. It carries **synthesis**
  the agent would otherwise compute: which node is the target, what it blocks, criteria
  tally. It does **not** duplicate note bodies (relocation, not synthesis — redundant once
  the tree is read). Instead:
  - `own_notes`: the target's *own* prior progress notes, inlined (highest-value signal,
    worth one duplication for primacy).
  - `weigh_notes`: a **pointer list of node ids** whose notes bear on this task
    (deterministic rule: the target's same-parent sibling leaves). Bodies stay in the tree.
- **Notes filtered to substance.** Include `noted` events + completion notes. Exclude
  churn: `heartbeat`, `claimed`, `claim_expired`, `released`, `moved`, `labeled`,
  `unlabeled`, `blocked`, `unblocked`. Raw trail remains in `job log`.
- **Per-node field order:** identity (`id`, `status`) → spec (`desc`, `criteria`) →
  history (`notes`) **last**.
- **`--format yaml` is the default** (full structured fidelity). **`--format md`** is a
  planned second renderer — a YAML front-matter header + a markdown-UL tree (leaner prose,
  criteria as `- [ ]`/`- [x]` checkboxes), idiomatic to this project's own Hextra docs.
  **Deferred — build YAML only for now**, but design the renderer seam so md drops in.

## Output schema (`--format yaml`)

> Note: this example is fenced as `text`, not `yaml`, on purpose — `job import`
> selects the *first* `yaml`/`yml`/unlabeled fence containing a top-level `tasks:`
> key, so only the work-breakdown block at the very bottom must be a `yaml` fence.

```text
orient:
  target: VBF5u
  title: Recipes section
  root: zwrjL
  status: available
  blockedBy: []
  blocks:
    - {id: L9G25, title: Retire DOCS.md}   # finishing the target unblocks this
  criteria: {passed: 0, total: 4}
  own_notes: []          # target's own prior progress notes, hoisted (often empty)
  weigh_notes: [eEkuq]   # nodes whose notes bear on this task; bodies are in the tree
tasks:
  - title: Documentation site
    id: zwrjL
    status: open
    desc: >-
      Hextra-on-Hugo documentation site at https://bensyverson.com/documentation/Jobs/.
      Replaces DOCS.md as the single source of truth for agent and human readers.
    labels: [docs]
    children:
      - title: Getting started section
        id: eEkuq
        status: done
        closed: 2026-05-07
        desc: >-
          Three pages — install.md, initialize.md, first-plan.md — plus _index.md.
          first-plan.md walks author -> import -> claim -> done end-to-end.
        criteria:
          - {text: "install.md covers `go install`, $GOBIN, PATH, AGENTS.md line", state: passed}
        notes:
          - "Wrote install/initialize/first-plan.md. scripts/verify-getting-started.sh
             re-runs the canonical sequence so an agent can spot drift. Updated _index.md to cards."
      - title: Recipes section
        id: VBF5u
        status: available
        target: true        # marks the orient target within the tree
        blocks: [L9G25]
        desc: >-
          Four narrative pages — great-plans, criteria-as-tests, multi-agent, recovery.
          The only place narrative belongs.
        criteria:
          - {text: "great-plans.md gives concrete advice on YAML authoring (sizing, refs, blockedBy hygiene)", state: pending}
        notes: []
```

## Behavior

| Invocation            | Target                          | Rendered scope                    |
|-----------------------|---------------------------------|-----------------------------------|
| `job orient`          | next available leaf (`RunNext`) | whole root tree of that leaf      |
| `job orient <id>`     | `<id>`                          | whole root tree containing `<id>` |
| `job orient --scope <id>` | next leaf (or positional)   | subtree rooted at `<id>`          |
| `--format yaml` (default) | —                           | structured YAML                   |
| `--format md` (deferred)  | —                           | front-matter + markdown-UL tree   |

## Reuse map (from code exploration)

Architecture: `cmd/job/*.go` = thin cobra wrappers; `internal/job/*.go` = `RunX(db, …)`
domain functions + renderers. New work mirrors this: `cmd/job/orient.go` + `internal/job/orient.go`.

- **Tree / ancestors:** `GetAncestors` (tasks.go:1814, root-first), `getChildren`
  (tasks.go:1548), `collectDescendants` (summary.go:266, BFS subtree). Root = last of
  `GetAncestors` (or self if root).
- **Per-node info:** `RunInfo` → `TaskInfo` (tasks.go:1686/1706) already bundles
  parent/children/blockers/blocked/labels/notes/criteria — the model to lean on.
- **Notes:** `getNotesForTask` (tasks.go:1851) returns `[]NoteEntry{Actor,Text,CreatedAt}`
  from `noted` events. Completion note on `Task.CompletionNote` (models.go). Filter churn
  by event type (`events.go`).
- **Criteria:** `GetCriteria` (criteria.go:111) → `[]Criterion{ShortID,Label,State}`;
  `CriterionState` enum (pending/passed/skipped/failed); `PendingCriteriaByShortID`
  (criteria.go:304) for batch tallies.
- **Next leaf:** `RunNextFiltered(db, parentShortID, actor, label, includeParents)`
  (claims.go:637); `RunNext` (claims.go:633) for the no-arg default.
- **Models:** `Task` (models.go:5), `Event` (models.go:21).
- **Render/format flag:** mirror `cmd/job/info.go:70` (`--format md|json`). YAML via
  `gopkg.in/yaml.v3` (already a dep; used in import.go:12).
- **Test harness:** `SetupTestDB`, `MustAdd`/`MustAddDesc`, `MustDone`, `MustClaim`,
  `MustGet`, `RunNote`, `TestActor`, and `CurrentNowFunc` time-mock (testhelpers.go,
  info_notes_test.go).

## Implementation plan (strict red/green TDD)

1. **`RunOrient` domain model.** Assemble an `OrientView{Header, Tree}` for a target
   (or next leaf): full root tree, per-node filtered notes + criteria+state, and the
   synthesized header (target/status/blocks/blockedBy/criteria tally/own_notes/weigh_notes).
   Red tests first: target resolution (no-arg → next leaf), whole-root-tree scope,
   `--scope` subtree, note-churn exclusion, criteria tally, `weigh_notes` = sibling-leaf
   ids, `own_notes` hoisting.
2. **YAML renderer.** Emit `orient:` then `tasks:` with the field order above
   (`notes` last), criteria as `{text,state}`, `target: true` on the target, `closed` on
   done nodes. Renderer behind an interface so md can drop in later.
3. **CLI wiring.** `cmd/job/orient.go`: positional optional id, `--format` (yaml default;
   md returns a clear "not yet implemented" until built), `--scope`; register on root cmd.
4. **Docs.** Add `docs/content/docs/` command-reference entry for `orient` (note md format
   as planned). Update `README.md` only if a new doc file warrants it.

Deferred (tracked, not built now): the `--format md` front-matter+UL renderer.

## Verification

- `go test ./internal/job/...` green (new `orient_test.go` + existing suite).
- `gofmt` + `go fix` clean (pre-commit hooks).
- Manual: against `.jobs.db`, `job orient` and `job orient VBF5u` emit valid YAML with the
  header + full-description tree + folded notes; `job orient --scope <branch>` narrows;
  done-leaf notes appear, claim/heartbeat churn does not.

---

## Importable work breakdown

```yaml
tasks:
  - title: Add `job orient` command
    desc: >-
      A leaf-context orientation command for fresh agents. Regenerates the plan tree
      around a target leaf with full descriptions, folded substantive notes, criteria as
      a checklist, and a synthesized header. Replaces the "paste the whole doc" practice.
      See project/2026-06-25-job-orient-command.md for the full design.
    labels: [cli, orient]
    children:
      - title: RunOrient domain model + assembly
        ref: orient-model
        labels: [cli, orient]
        desc: >-
          Assemble an OrientView{Header, Tree} in internal/job/orient.go. Target resolves
          to the positional id, else the next available leaf via RunNextFiltered. Default
          scope is the whole root tree (walk GetAncestors to root, then build the subtree);
          --scope limits to a given subtree. Each node carries status, full description,
          criteria with state, and notes filtered to substance. The header synthesizes
          target/status/blockedBy/blocks/criteria-tally plus own_notes (target's own notes,
          inlined) and weigh_notes (same-parent sibling-leaf ids). Reuse RunInfo,
          GetAncestors, getChildren/collectDescendants, getNotesForTask, GetCriteria.
        criteria:
          - No-arg orient targets the next available leaf (matches RunNext)
          - Positional id sets the target; default render scope is its whole root tree
          - "--scope <id> limits the rendered tree to that subtree"
          - Notes include `noted` events and completion notes; churn (heartbeat, claimed, claim_expired, released, moved, labeled, blocked) is excluded
          - Header criteria tally reports passed/total for the target
          - weigh_notes lists the target's same-parent sibling-leaf ids that carry notes
          - own_notes inlines the target's own prior notes (empty when none)
      - title: YAML renderer for orient output
        ref: orient-yaml
        labels: [cli, orient]
        blockedBy: [orient-model]
        desc: >-
          Render OrientView as YAML via gopkg.in/yaml.v3 behind a renderer interface so the
          deferred md renderer can drop in. Emit top-level `orient:` header then `tasks:`
          tree. Per-node field order: id, status, (closed when done), desc, labels, blocks,
          criteria, then notes LAST. Criteria render as {text, state}. The target node gets
          `target: true`.
        criteria:
          - Output has a top-level `orient:` header and a `tasks:` tree
          - Per-node order is id, status, desc, criteria, then notes last
          - Criteria render as {text, state}; the target node is flagged target true
          - Done nodes include a `closed` date; full descriptions are never truncated
          - Output is valid YAML and round-trips through a yaml.v3 decode in tests
      - title: CLI wiring for `job orient`
        ref: orient-cli
        labels: [cli, orient]
        blockedBy: [orient-yaml]
        desc: >-
          Add cmd/job/orient.go mirroring cmd/job/info.go conventions. Optional positional
          id, --format (yaml default; md returns a clear not-yet-implemented message until
          the md renderer lands), --scope flag. Register on the root command. Wire through
          the resolved actor for next-leaf resolution.
        criteria:
          - "`job orient` and `job orient <id>` emit YAML against a real .jobs.db"
          - "--format defaults to yaml; --format md returns a clear not-implemented message"
          - "--scope <id> narrows the rendered tree"
          - Command is registered and appears in `job --help`
      - title: Document `job orient`
        labels: [cli, orient, docs]
        blockedBy: [orient-cli]
        desc: >-
          Add a command-reference page under docs/content/docs/ for `job orient`: purpose
          (worker's opener vs status as orchestrator's opener), invocations, the orient
          header fields, note filtering, and a note that --format md is planned. Update
          README.md only if a new doc file warrants linking.
        criteria:
          - Docs cover orient's purpose, invocations, header fields, and note filtering
          - The planned --format md renderer is noted as forthcoming
          - README updated only if a new doc file warrants it
  - title: Warn on ambiguous or lossy `job import` block selection
    desc: >-
      job import silently selects the first yaml/yml/unlabeled fence containing a
      top-level `tasks:` key, and silently ignores keys outside the grammar
      (extractTasksYAML + rawTask.UnmarshalYAML default case in import.go). A Markdown
      file that merely *illustrates* output YAML can therefore hijack the import with no
      warning — surfaced while planning project/2026-06-25-job-orient-command.md. Make
      selection observable: warn (stderr) when more than one candidate block carries a
      top-level `tasks:` key, naming the one chosen; warn when the chosen block carries
      keys outside the import grammar. Warnings must not block an otherwise valid import.
    labels: [cli, import, papercut]
    criteria:
      - Importing a file with more than one yaml/yml/unlabeled `tasks:` block warns on stderr and names the block used
      - Importing a block containing non-grammar keys warns on stderr that those keys were ignored
      - Warnings appear under --dry-run too and never block an otherwise valid import
```

> The `--format md` renderer (front-matter header + markdown-UL tree with checkbox
> criteria) is intentionally **not** in the import block — it's prose-only for now, captured
> in *Design decisions* and *Implementation plan* above so it isn't lost, but not yet a
> claimable task.
