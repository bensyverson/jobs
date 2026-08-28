# Feedback from the `agents` review — `-F` and an empty `orient`

*Proposal · 2026-08-28*

> **Correction (2026-08-28, on review against the code).** Two claims below are wrong.
> `job` already has a file form: `-m @path` reads a file and `-m -` reads stdin
> (`resolveMessage`, `cmd/job/commands.go`) on `note`, `done`, `claim` and `release`
> — so "`job done` takes no stdin body" is false; `done -m -` works. The real gaps
> are the spelling (`-F` is what the Git rule teaches) and `add`/`edit`, whose
> `--desc` is inline-only. "No other verb changes" also contradicts "`-F` on every
> body-taking verb": `claim`, `release` and `cancel` take bodies too. Decisions and
> the import block now live in
> [2026-08-28-body-flags-orient-and-issues.md](2026-08-28-body-flags-orient-and-issues.md);
> the YAML below is superseded.

## Context — why

The shared house rules (the `agents` repo) were reviewed on 2026-08-28 against four managed repos. The same `rule:` entry appeared independently in three of them (Hirewell 2026-08-17, Nobedan 2026-08-24, Organizize 2026-08-26): **`job note` has no `-F`.** The Git rule teaches `git commit -F <file>` because the shell interprets `-m` bodies first, and says the same hazard "applies to any `-m` flag, `job note -m` included" — so every agent infers `-F` exists here too, tries it, and fails once. Organizize also found that `job done` takes no stdin body, so the stdin workaround the gotchas prescribe for `note` doesn't transfer to `done`.

One habit should cover both tools: write the body to a file, pass `-F`. Stdin `-` stays for pipelines.

A second, unrelated report (Hirewell; reproduced in the `agents` repo the same day): the house rule says "open every session with `job orient`", and `orient` exits non-zero with `No available tasks. Run 'list all' …` when the focused root (or the whole repo) has nothing claimable. A session's first command failing reads as a broken tool, and the rule gets a `rule:` complaint it doesn't deserve. Orienting is a read; an empty tree is a valid answer, not an error.

## Decisions

- **`-F, --file <path>` on every verb that takes a free-text body**: `note` (the note text), `done` (the completion note), `add` and `edit` (the description). `-F -` reads stdin, matching git. `-F` and `-m`/`--desc` together is an error, as in git.
- **`job orient` exits 0 on an empty tree** and prints the same guidance it prints today ("No available tasks in focused root … or release it with `job focus --clear`"). `job next`/`claim --next` keep their non-zero exit — there the caller asked for a task and didn't get one; `orient` only asked for the lay of the land.
- **No other verb changes.** Agents marking acceptance criteria was reported as missing too, but `job edit --set-criterion label=passed` already does it; that goes in the shared jobs rules, not here.

```yaml
tasks:
  - title: "`-F <file>` and a non-failing empty `orient`"
    desc: >-
      Umbrella for the 2026-08-28 agents-review feedback. Three managed repos independently filed
      "job note has no -F"; the fix is to give every body-taking verb the same -F <file> form git has,
      so one habit covers both tools. Separately, `job orient` should not exit non-zero on an empty
      tree, because the house rules make it every session's first command.
    labels: [agents-feedback]
    children:
      - title: "`job note -F <file>`"
        ref: note-f
        desc: >-
          Add -F/--file to `note`: read the body from the path, `-F -` from stdin (same as the
          existing positional `-`). Passing -F together with -m or a positional body is an error,
          mirroring git. Update DOCS.md and the docs site page for `note`.
        criteria:
          - "`job note <id> -F body.md` records the file's contents verbatim, trailing newline trimmed"
          - "`job note <id> -F -` reads stdin"
          - "`-F` combined with `-m` or a positional body exits non-zero with a clear message"
          - "DOCS.md and the note docs page show the new flag"
      - title: "`job done -F <file>`"
        ref: done-f
        blockedBy: [note-f]
        desc: >-
          Same flag on `done` for the completion note, sharing the reader helper introduced for
          `note`. Multi-id `done` applies the one note to every id, as -m does today.
        criteria:
          - "`job done <id> -F note.md` records the file as the completion note"
          - "`-F` with `-m` exits non-zero"
          - "docs updated"
      - title: "`job add` and `job edit` take the description from a file"
        ref: add-f
        blockedBy: [note-f]
        desc: >-
          Descriptions are the longest bodies the CLI accepts and today exist only as --desc inline.
          Add -F/--file as the file form of --desc on both `add` and `edit`; `--desc ""` still
          clears on `edit`.
        criteria:
          - "`job add <parent> <title> -F desc.md` stores the file as the description"
          - "`job edit <id> -F desc.md` replaces the description"
          - "`-F` with `--desc` exits non-zero"
          - "docs updated"
      - title: "`job orient` exits 0 on an empty tree"
        ref: orient-empty
        desc: >-
          When neither the focused root nor the repo has an available leaf, `orient` prints the
          existing guidance and exits 0. `next` and `claim --next` are unchanged. Check the
          existing tests that assert on the error string (cmd/job/commands_test.go, next_all_test.go,
          internal/job/claim_next_scope_test.go) and change only the ones that exercise `orient`;
          explain each change in the commit body.
        criteria:
          - "`job orient` in an empty repo prints the no-tasks guidance and exits 0"
          - "`job orient` with a focused root that has no available leaf prints the focused-root guidance and exits 0"
          - "`job next` on an empty repo still exits non-zero"
      - title: "Retire the `-F` gotchas and reword the shared Git rule"
        blockedBy: [note-f, done-f, add-f]
        desc: >-
          Once installed: delete the `job note has no -F` entries in hirewell, nobedan and
          organizize project/gotchas.md, and in the agents repo change core's "-m hazard" line to
          teach `-F <file>` for git and job alike. Done from the agents repo, not here.
        criteria:
          - "the three gotchas are deleted"
          - "modules/core.md in the agents repo teaches -F for both tools and `agents sync` has been run in the managed repos"
```
