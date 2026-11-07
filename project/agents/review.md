<!-- agents:begin review@fbe6c0 -->
# Code review

Independent review catches bugs that green tests miss. It covers code meant to be kept. A prototype declared up front — a `spike` label on its container, or a ruling in the `AGENTS.md` head — is exempt until it is built on or merged, and then all of it counts as unreviewed.

## When

Review the unreviewed work at the first of:

- **A job container closes.**
- **About 1,500 lines of production code** across the unreviewed leaves' commits, not counting tests, goldens, vendored or generated code.
- **A commit touches a persisted or wire format, `unsafe` or FFI code, locking, or deletion.** Review it before building on it.
- **Handoff.** Review what's left, or name the unreviewed commit range in the handoff for the next session to review first.

The unreviewed work is every closed leaf since the baseline without the `reviewed` label:

```sh
job ls --status done --leaves --without-label reviewed --since <baseline>
```

The baseline is a `Review baseline: <RFC3339 time>` line in the head of `AGENTS.md`. If there is none, add one with the current time; work closed before it is out of scope.

## How

- **One fresh reviewer per surface**, given the code and each leaf's criteria, never the author's report.
- **A finding counts once confirmed** by a probe that reproduces it or a trace with file and line.
- **File each confirmed finding as a leaf** carrying its reproduction, with `--found-in` naming the reviewed leaf.

## Fixes

- **The reviewer doesn't fix.** It stays independent and verifies the fix.
- **The author fixes** while it is still running with room in its context. Otherwise a fresh agent does, assigned by file ownership (`delegation.md`), briefed with the finding and its reproduction.
- **Verify each fix against the reproduction**, by the reviewer or a fresh verifier.

## Mark it

```sh
job label add <id> [<id>...] with reviewed
```
<!-- agents:end review -->
