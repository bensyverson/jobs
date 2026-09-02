# The adoption note, and a preview for `job gitignore`

*2026-09-02*

## What happened

An agent adopted a pre-store `.jobs.db` in `../Woodcase` and reported back that
it had pieced the outcome together from `git status` rather than from Jobs. The
note it got was:

```text
note: adopted this database into the store: 1422 events carried across as history,
a snapshot of 157 tasks written, replica 22DryP. The previous cache is at
/Users/ben/git/Woodcase/.jobs.db.pre-adopt.
```

Accurate, and it answers "what did you do to my data". It does not answer "what
do you need from me now", which after adoption is four separate things:

- **Where the store lives.** The note never names `.jobs/log`. The agent found
  the directory as an untracked path.
- **That the log is meant to be committed.** Nothing said so. A reader who never
  ran `job gitignore` could reasonably have added `.jobs/` to `.gitignore` and
  thrown away the point of the store.
- **That `.gitignore` needed updating, and which verb does it.** Woodcase's
  patterns predated `.jobs.db*` — they covered `.jobs.db` and the two WAL
  sidecars, so `.jobs.db.lock` and the `.pre-adopt` trio appeared as four new
  untracked files.
- **Whether `.jobs.db.pre-adopt` is disposable.** "The previous cache" implies
  yes without saying when it is safe to delete.

The house rule in `cli-design.md` is that a write answers with what it made and
names the next command. This one did the first half.

Separately: `job gitignore` had no `--dry-run`, so the agent could not preview
the edit and had to read the diff afterwards. (The inline-comment bug from
2026-08-29 is confirmed fixed — the run put comments on their own lines.)

## What we changed

**The note is now two notes** (`AdoptReport.notice` and `AdoptReport.nextStep`,
`internal/job/adopt.go`):

```text
note: adopted this database into the store: … replica 22DryP. The previous cache
is at /path/.jobs.db.pre-adopt and can be deleted once you trust the new store.
note: .jobs/log is the record now — commit it. Run `job gitignore` to ignore the
cache and its sidecars.
```

The second is advice, so it follows `init`'s rule for its own hint: **only where
it can be acted on.** Outside a git repository it is not printed at all, and the
`job gitignore` clause appears only while `MissingGitignoreEntries` is non-empty.
The repository check moved out of `cmd/job/init.go` into `job.IsGitRepo`, beside
the gitignore table it gates, so one definition serves both callers.

**`job gitignore --dry-run`/`-n`** prints the report and writes nothing, spelled
the same as `job merge`'s. The preview is rendered from
`job.PendingGitignoreEntries`, the same partition the real run acts on, and a
test asserts the two reports differ only in `Would write` versus `Wrote` — a
preview that could drift from the write would be worse than none.

## Evidence

- `go test ./internal/job/ ./cmd/job/` — five new tests, all red first:
  `TestAdopt_NoticeNamesTheStoreTheCommitAndGitignore`,
  `TestAdopt_NoticeDropsGitignoreOnceNothingIsMissing`,
  `TestAdopt_NoticeOmitsGitAdviceOutsideARepository`,
  `TestGitignore_DryRun_WritesNothing`,
  `TestGitignore_DryRun_LeavesAnExistingFileAlone`,
  `TestGitignore_DryRun_PreviewMatchesTheRealRun`.
- The third of those passes vacuously against the old code, so the guard was
  verified by deleting the `IsGitRepo` check and watching it fail, then
  restoring it.

Tracker: `U5hih9` (the note), `Jctvv3` (the preview).
