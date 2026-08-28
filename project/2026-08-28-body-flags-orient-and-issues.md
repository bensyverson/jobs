# `-F` bodies, a non-failing `orient`, and issue trees

*2026-08-28 — decisions, consolidated from two proposals*

This supersedes the import blocks in
[2026-07-25-issues-vs-tasks.md](2026-07-25-issues-vs-tasks.md) and
[2026-08-28-agents-feedback.md](2026-08-28-agents-feedback.md). Both were
reviewed against the code on 2026-08-28; the corrections are recorded in place
there and folded in here. Reasoning stays in the originals — this document is
the decisions and the plan.

## 1. `-F <path>` on every body-taking verb

**Why.** Three managed repos independently filed "`job note` has no `-F`". The
shared Git rule teaches `git commit -F <file>` and says the `-m` hazard applies
to `job note -m` too, so agents infer `-F` exists here, try it, and fail once.

**What was wrong in the proposal.** `job` already has a file form: `-m @path`
reads a file and `-m -` reads stdin, via `resolveMessage` in
`cmd/job/commands.go`, on `note`, `done`, `claim` and `release`. The report that
"`job done` takes no stdin body" was false — `done -m -` works today. The real
gaps are (a) the spelling, and (b) `add`/`edit`, whose `--desc` is inline-only.

**Decision.** `-F, --file <path>` is an alias for `-m @path` (`-F -` for
`-m -`) on every verb that takes a free-text body: `note`, `done`, `claim`,
`release`, `cancel` (the reason) and, as the file form of `--desc`, `add` and
`edit`. `@path` stays — it costs nothing and is in the field. Giving `-F`
together with `-m`/`--desc`/a positional body is an error, as in git. One reader
helper serves all seven verbs.

## 2. `job orient` exits 0 on an empty tree

**Why.** The house rule makes `orient` every session's first command, and it
currently exits non-zero with `No available tasks…` — the errors built in
`internal/job/claims.go` and returned unchanged by `cmd/job/orient.go`. A
first command that fails reads as a broken tool. Orienting is a read; an empty
tree is a valid answer.

**Decision.** `orient` prints the same guidance and exits 0. `next` and
`claim --next` keep their non-zero exit: there the caller asked for a task and
did not get one. The tests in `cmd/job/commands_test.go`,
`cmd/job/next_all_test.go` and `internal/job/claim_next_scope_test.go` assert
on the error string; only the ones that exercise `orient` change, each explained
in the commit body.

## 3. Issue trees: kind on the root only

**Why.** A bug found while working a plan is filed where it was found, and then
holds the plan open because a parent cannot close while a child is open. `job`
puts two relations on the parent edge — provenance (where it was found) and
blocking (what cannot close until it is fixed) — and for a discovered defect
they must come apart. Full reasoning, including why this is *not* a second
binary, is in the 2026-07-25 document.

**Decision on the gating question.** Tree kind is a property of the **root
only**, and an issue root owns task children directly. Children of an issue
root are ordinary tasks; the root's kind is what the default readers filter on.
This is the smallest schema change, keeps every verb working under an issue
tree, and avoids the issue↔PR dance of two objects for one piece of reality.
Per-node kind only pays off if issues nest inside task trees — precisely the
situation being ended.

**Precedent the proposal missed.** `job block add` is already a non-parent edge
stored apart from the tree. `found-in` is the same shape with the opposite
semantics — a reference that explicitly does not block — and should start from
the blockers table design rather than invent one. The existing workaround
also gains from it: an issues root plus `move`, and `block add <leaf> by <bug>`
for the rare defect that genuinely should hold a plan.

**Scope discipline.** No severity field, no triage states, no reporters.
`label` covers severity and component; `ls --label` answers "what is broken in
this component, across all plans".

## Import

```yaml
tasks:
  - title: "`-F <file>` on every body-taking verb"
    desc: >-
      Alias for the existing `-m @path` / `-m -` forms, spelled the way git spells it, so one habit
      covers both tools. See project/2026-08-28-body-flags-orient-and-issues.md §1. `@path` stays.
      `-F` combined with `-m`, `--desc` or a positional body exits non-zero with a clear message.
    labels: [agents-feedback, cli]
    children:
      - title: "`-F` on note, done, claim, release, cancel"
        ref: f-notes
        desc: >-
          These five already route through resolveMessage (cmd/job/commands.go). Add one shared
          -F/--file flag registration and a helper that merges -F into the raw message before
          resolveMessage runs, erroring on -F plus -m or a positional body. Multi-id `done` applies
          the one note to every id, as -m does today. Update DOCS.md and the docs-site page per verb.
        criteria:
          - "`job note <id> -F body.md` records the file's contents verbatim, trailing newline trimmed, exactly as `-m @body.md` does"
          - "`-F -` reads stdin on every one of the five verbs"
          - "`-F` combined with `-m` or a positional body exits non-zero with a clear message"
          - "DOCS.md and the docs-site pages for the five verbs show the flag"
      - title: "`add` and `edit` take the description from a file"
        ref: f-desc
        blockedBy: [f-notes]
        desc: >-
          --desc is inline-only today (cmd/job/add.go, edit.go). Add -F/--file as its file form,
          sharing the helper from f-notes; `--desc ""` still clears on `edit`.
        criteria:
          - "`job add <parent> <title> -F desc.md` stores the file as the description"
          - "`job edit <id> -F desc.md` replaces the description; `--desc \"\"` still clears"
          - "`-F` with `--desc` exits non-zero"
          - "DOCS.md and the docs-site pages for add and edit show the flag"
  - title: "`job orient` exits 0 on an empty tree"
    desc: >-
      When neither the focused root nor the repo has an available leaf, `orient` prints the existing
      guidance and exits 0. `next` and `claim --next` are unchanged. See §2 for the tests that
      assert on the error string; change only the ones that exercise `orient` and explain each in
      the commit body.
    labels: [agents-feedback, cli]
    criteria:
      - "`job orient` in an empty repo prints the no-tasks guidance and exits 0"
      - "`job orient` with a focused root that has no available leaf prints the focused-root guidance and exits 0"
      - "`job next` and `job claim --next` on an empty repo still exit non-zero"
  - title: Issue trees — a lifetime for discovered work
    desc: >-
      Kind on the root only; an issue root owns task children directly. Provenance is a found-in
      edge modelled on the blockers table; blocking is not implied. See §3 and
      project/2026-07-25-issues-vs-tasks.md. Scope discipline: no severity field, no triage states,
      no reporters.
    labels: [issues, core]
    children:
      - title: Tree kind — task-tree vs issue-tree
        ref: tree-kind
        desc: >-
          A root can be marked as an issue-tree (numbered migration). next, orient and the no-arg
          claim exclude issue-trees by default; an explicit flag asks for them. A root can be
          converted either way without losing history.
        criteria:
          - "A root can be marked as an issue-tree, and the marking is visible in ls and show"
          - "next, orient and no-arg claim exclude issue-trees by default, and there is an explicit way to include them"
          - "An existing root can be converted in either direction without losing history"
          - "Docs page and DOCS.md describe tree kinds"
      - title: found-in cross-reference
        ref: found-in
        desc: >-
          An issue records the leaf that surfaced it without being parented by it. Start from the
          blockers table design; the edge creates no blocking relationship. It must survive the leaf
          being done, cancelled, or cancelled by cascade — the common case is that the plan finishes
          while the bug does not.
        criteria:
          - "An issue records the leaf it was found in, and the reference survives that leaf being done or cancelled (including by cascade)"
          - "The reference is visible from both ends in show, and creates no blocking relationship"
          - "Docs page and DOCS.md describe found-in"
      - title: Migrate existing stragglers onto an issues root
        blockedBy: [tree-kind, found-in]
        desc: >-
          Sweep open trees for leaves that are discovered defects rather than planned work, move them
          under an issues root, and record found-in for each. `job move` already exists, so this is a
          data pass. Do it last: moving before found-in exists loses the provenance the move is meant
          to preserve.
        labels: [migration]
        criteria:
          - "Every moved leaf carries a found-in reference to where it came from"
          - "No tree is left open solely because of a defect leaf"
```

Not imported: retiring the three `-F` gotchas and rewording the shared Git rule
to teach `-F <file>` for git and job alike. That is done from the `agents`
repo once these ship, and tracked there.
