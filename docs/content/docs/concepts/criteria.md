---
title: Acceptance criteria
weight: 3
---

A task can carry a list of **acceptance criteria**: short, testable statements that the close should verify. Criteria turn `job done` into a check, not just a state flip — the agent has to assert that each criterion is satisfied (or explicitly waived) before the task is allowed to close.

This is the most distinctive thing Jobs does. Used well, criteria become first-draft unit tests.

## Lifecycle

Each criterion has a state — one of `pending`, `passed`, `skipped`, `failed` — and a server-minted **3-character base62 short id** (e.g. `nsO`, `K49`). The short id is the canonical handle, which matters because criterion labels are full sentences and quoting them through the shell is painful:

```text
Criteria: 2 pending — mark each before close, or use --force-close-with-pending
  nsO [ ] returns 200 status code
  yFW [ ] response body is valid JSON
```

Criteria are author-time: defined when the task is created (via the YAML import grammar's `criteria: [...]` key, or `job add --criterion "<label>"`). State transitions are recorded as `criterion_state` events on the event log; the initial set is recorded as a single `criteria_added` event.

## The strict close

`job done` is strict by default. If the target task carries unmarked pending criteria, the close refuses with a stable, greppable error:

```text
Error: cannot close: 2 pending criteria
  MZHd1 "Write the handler":
    [ ] returns 200 status code
    [ ] response body is valid JSON
Override: --force-close-with-pending
```

The `cannot close: N pending criteria` substring is stable for grepping. The unmarked labels are listed beneath the offending task; for multi-id closes, every offending task gets its own block.

Three close shapes recover.

### Mark each row

Repeatable per-criterion:

```sh
job done <id> --criterion nsO=passed --criterion yFW=passed
```

`<ref>` accepts the short id or the verbatim label. `<state>` is `passed`, `skipped`, or `failed`. For multi-id closes, prefix the ref with `<task-id>:` to disambiguate.

### Bulk shorthand

When every remaining criterion has the same disposition:

```sh
job done <id> --all-passed
job done <id> --all=skipped
job done <id> --all=failed
```

`--all-passed` is the common case after a clean run; `--all=skipped` is the "we're shipping without verifying these" call. Already-marked rows are left alone — the bulk flag only touches `pending`. Composes with explicit `--criterion` overrides; the explicit row wins.

The close ack reports `Marked N criteria <state> before closing.` so it's auditable.

### Force with waiver

When you genuinely need to ship without satisfying a criterion — and want the deferral on record:

```sh
job done <id> --force-close-with-pending
```

The unmarked labels are recorded as a **waiver** on the done event. A reviewer reading `job log` sees exactly what was deferred and by whom.

## When criteria don't apply

- `--cascade` closes are unaffected. The criteria belong to the children; closing a parent with cascade trusts the children's own criteria gates (or their absence).
- `cancel` is unaffected. Canceled work's criteria are moot.
- Tasks without criteria see no friction at all. A task with no `criteria:` key is a normal task; `done` closes it cleanly.

## Authoring criteria well

The bar is "short, testable." Treat each criterion as a first-draft unit test that names the behavior, not the implementation. A few patterns:

- **Outcome, not steps.** `returns 200 status code` over `runs the integration test`.
- **One assertion per row.** Splits into separate `--criterion` calls cleanly. `passes lint and tests` becomes two rows.
- **Avoid implementation references that will rot.** `parses the response with jsonpath` ages worse than `extracts the user id from the response`.

If a criterion is hard to mark `passed` or `failed` honestly, it's the wrong granularity — split it or remove it.
