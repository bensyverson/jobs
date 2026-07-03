# Orient slimming + per-actor active root ("focus")

*Design + implementation plan · 2026-07-03*

## Context — why

Two related problems surfaced dogfooding `job` on a large external project
(~/git/hirewell, 90-task root, weeks of history):

1. **`job orient` output grows monotonically with history.** On hirewell it
   emits ~231KB — far past the tool-output threshold, so agents get a
   dumped-to-file path instead of in-context orientation. Measured breakdown:
   **91% (217KB) is `done` subtrees** carrying full descriptions, progress
   notes, and criteria. Notes on *non-done* tasks are ≈0KB. The static plan
   doc never had this problem because it has no accumulated history.
   Fine-grained split of the done weight: 113KB notes, 54KB desc on 70 done
   *leaves*, 11KB criteria — but only **6KB desc on the 13 done containers**
   (slices/phases), which is where the plan narrative lives.

2. **"Next" is global, but work is tree-local.** `done --claim-next` is
   root-scoped (RunClaimNextUnderRootOf), but bare `claim --next`, `job next`,
   status's `Next:` hint, and `job orient`'s no-arg target all resolve via
   global RunNext — so a fresh session silently walks the agent into a
   different root tree. Counter-intuitive discrepancy: stickiness exists only
   inside a done-chain.

## Converged decisions

### Orient slimming

- **Done leaves** render as `title / id / status / closed` only — no desc,
  notes, or criteria. The tree shape of finished work stays visible; history
  is one `job show` away.
- **Done containers** (done tasks with children) additionally keep their full
  `desc` — this preserves the slice-level plan narrative (the reason agents
  were pasted the full project doc) for ~6KB, not 54KB.
- **Completion note on the most recently closed task only** — a "here's what
  just happened" breadcrumb (orient has never emitted completion notes;
  this is a new, deliberately singular field).
- **Open/claimed nodes unchanged.** Their notes are near-free and are the
  live state orient exists to convey.
- **`--full` flag** restores today's full-fidelity output.
- Elision happens at **OrientView assembly**, not in the renderer, so the
  planned markdown renderer inherits it through the same seam.
- Budget check (hirewell): ~15–20KB live tree + 7KB done skeleton + 6KB
  container descs ≈ **28KB vs 231KB today**.

### Active root ("focus")

- **Per-actor**, matching claims; parallel agents on different trees each
  keep their own lane.
- **Event-sourced**: `focus_set` / `focus_released` events; current focus is
  materialized by scanning latest focus events per actor (event volumes are
  tiny — hirewell has 2,173 events total). No schema migration expected
  (events table is generic — verify).
- **Set by claiming, last-claim-wins**: any successful claim whose root
  differs from the current focus flips focus to that root. No ceremony.
  Plain `done` never flips focus (not a claim), but the claim performed by
  `done --claim-next` does.
- **Auto-released** when the focused root closes (done cascade) or is
  canceled/deleted.
- **Respected by the no-argument defaults** of: bare `claim --next`,
  `job next`, status's `Next:` hint, and `job orient`'s target resolution.
  Explicit arguments always win and behave exactly as today.
- **Fail loudly**: when the focused root has nothing available, error with a
  hint naming the escape (`claim --next <other-root>` flips focus by
  claiming; `job focus --clear` releases it).
- **`job focus`** shows the current focus; **`job focus --clear`** releases
  it (the "pause this tree" case). No `job focus <id>` setter — claiming is
  the setter.
- **`job status`** gains a `Focus:` line when set.

## Work breakdown

```yaml
tasks:
  - title: Slim `job orient` so large plans stay in context
    desc: |
      Cut orient output ~10x by eliding done-task history at OrientView
      assembly time, while preserving the plan narrative agents used to get
      from reading the full project doc. Target: hirewell orient ~28KB
      (from 231KB). Done leaves keep title/id/status/closed only; done
      containers also keep desc; notes and criteria are dropped from all
      done nodes; the single most recently closed task in the rendered tree
      carries its completion note. `--full` restores current behavior.
    labels: [orient, context-budget]
    children:
      - title: Elide done-node history during OrientView assembly
        ref: orient-elide
        desc: |
          In buildOrientNode (internal/job/orient.go): for done nodes, skip
          notes and criteria entirely; drop desc when the done node is a
          leaf; keep desc when it has children. Non-done nodes are
          untouched. Add an assembly-level option (e.g. a full bool threaded
          from RunOrient) so full fidelity remains reachable; elision is the
          default. TDD: fixtures with a done leaf (desc+notes+criteria), a
          done container, and an open leaf with notes — assert exactly what
          survives.
        criteria:
          - A done leaf node carries no desc, notes, or criteria in the assembled OrientView
          - A done container node keeps desc but carries no notes or criteria
          - Open and claimed nodes keep desc, notes, and criteria exactly as before
          - RunOrient with the full option produces today's unelided view
      - title: Inline the completion note on the most recently closed task
        ref: orient-recent-note
        desc: |
          Add a CompletionNote field to OrientNode, populated only for the
          done node with the greatest closed timestamp within the rendered
          tree (ties: latest event wins). Render as completion_note in the
          YAML node doc, omitempty. All other done nodes never emit it.
          TDD first: two done tasks with completion notes, only the most
          recently closed one surfaces.
        blockedBy: [orient-elide]
        criteria:
          - Only the most recently closed task in the rendered tree emits completion_note
          - A tree with no closed tasks emits no completion_note anywhere
      - title: Add `--full` flag to `job orient`
        desc: |
          CLI wiring in cmd/job: `job orient --full` threads the full option
          through RunOrient, restoring pre-elision output. Help text should
          frame default output as the context-budget mode.
        blockedBy: [orient-elide]
        criteria:
          - job orient --full output matches pre-change output on a seeded fixture db
      - title: Document orient elision in the existing docs pages
        desc: |
          Update the existing orient documentation (reference section +
          machine-interface if it shows orient output) to describe done-node
          elision, the single completion_note, and --full. No new doc files.
        blockedBy: [orient-elide, orient-recent-note]
        criteria:
          - Docs describe what done nodes emit and why, and mention --full

  - title: Per-actor active root ("focus") for tree-local next/claim defaults
    desc: |
      Introduce a per-actor "active root" so no-argument next/claim/status/
      orient defaults stay inside the tree being worked, instead of jumping
      to the globally first available leaf. Event-sourced (focus_set /
      focus_released), set automatically by claiming (last-claim-wins),
      auto-released when the root closes or is canceled, manually released
      via `job focus --clear`. Explicit arguments always override; when the
      focused root has nothing available, commands fail loudly with an
      escape hint rather than silently crossing trees.
    labels: [focus, claims]
    children:
      - title: Focus domain — events and materialized per-actor lookup
        ref: focus-domain
        desc: |
          New event types focus_set (payload: root task) and focus_released,
          attributed to an actor. GetFocus(db, actor) returns the currently
          focused root task or nil, derived from the latest focus event for
          that actor; a focus pointing at a deleted/canceled/done root reads
          as released. Confirm the events table needs no migration; if it
          does, add a numbered migration instead of hand-migrating.
        criteria:
          - SetFocus emits focus_set and GetFocus returns that root for the same actor only
          - ReleaseFocus emits focus_released and GetFocus returns nil afterward
          - GetFocus returns nil when the focused root is done, canceled, or deleted
      - title: Claiming flips focus (last-claim-wins)
        ref: focus-on-claim
        desc: |
          Every successful claim path (claim <id>, claim --next, the claim
          inside done --claim-next) resolves the claimed task's root and
          emits focus_set for the actor when it differs from their current
          focus. No event when the root is unchanged. Plain done/note/
          release never touch focus.
        blockedBy: [focus-domain]
        criteria:
          - Claiming a task in a different root flips the actor's focus to that root
          - Claiming within the focused root emits no focus event
          - A second actor's claim does not move the first actor's focus
      - title: Focus auto-releases when its root completes or is canceled
        ref: focus-autorelease
        desc: |
          When the done cascade or cancel closes a root that is some actor's
          focus, emit focus_released for each such actor. TDD the cascade
          case: closing the last open leaf cascade-closes the root and
          releases focus.
        blockedBy: [focus-domain]
        criteria:
          - Cascade-closing a focused root releases focus for every actor focused on it
          - Canceling a focused root releases focus
      - title: Scope no-arg next and claim --next to the focus, failing loudly
        ref: focus-next
        desc: |
          Bare `job next` and `job claim --next` (no parent argument) scope
          to the actor's focused root when one is set. When the focused root
          has no available leaf, return an error naming the focused root and
          the escapes (claim --next <other-id>, job focus --clear) instead
          of silently crossing into another tree. Explicit parent arguments
          bypass focus entirely. No focus set → today's global behavior.
        blockedBy: [focus-on-claim]
        criteria:
          - With focus set, bare next and claim --next only yield leaves inside the focused root
          - With focus set and the root exhausted, the error names the root and both escapes
          - An explicit parent argument overrides focus
          - With no focus set, behavior is unchanged
      - title: Status shows Focus and scopes its Next hint to it
        ref: focus-status
        desc: |
          `job status` renders a `Focus: <id> "<title>"` line for the actor
          when set, and its Next: suggestion resolves within the focused
          root (falling back to the loud-failure hint when exhausted).
        blockedBy: [focus-next]
        criteria:
          - Status output includes the Focus line when set and omits it when not
          - The status Next hint stays inside the focused root
      - title: Orient's default target respects focus
        ref: focus-orient
        desc: |
          resolveOrientTarget's no-arg path resolves the next available leaf
          within the actor's focused root instead of globally, with the same
          loud failure when the root is exhausted. Explicit target ids are
          untouched.
        blockedBy: [focus-next]
        criteria:
          - No-arg orient targets a leaf inside the focused root when focus is set
          - Orient with an explicit id ignores focus
      - title: Add the `job focus` command (show, --clear)
        ref: focus-cmd
        desc: |
          `job focus` prints the actor's current focus (root id, title, and
          a one-line availability summary) or "No focus set." `job focus
          --clear` emits focus_released. Deliberately no setter argument —
          claiming is the setter; help text says so.
        blockedBy: [focus-domain]
        criteria:
          - job focus prints the focused root or a clear no-focus message
          - job focus --clear releases and confirms
      - title: Document the active-root model in the existing docs pages
        desc: |
          Update existing reference/execution and observation docs (and the
          CLI long-help in cmd/job/commands.go) to explain focus: set by
          claiming, per-actor, auto-release, loud failure, --clear. No new
          doc files.
        blockedBy: [focus-next, focus-status, focus-orient, focus-cmd]
        criteria:
          - Docs and command help describe how focus is set, scoped, released, and escaped
```
