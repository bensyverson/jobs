---
title: Install
weight: 1
---

Jobs is a single Go binary called `job`. The project is **Jobs**; the binary is `job` (singular, since each invocation acts on one job).

## `go install`

```sh
go install github.com/bensyverson/jobs/cmd/job@latest
```

This drops the `job` binary into `$GOBIN` if it's set, otherwise into `$HOME/go/bin`. Confirm where Go put it:

```sh
go env GOBIN GOPATH
```

If `GOBIN` is empty, the binary is at `$(go env GOPATH)/bin/job` — typically `$HOME/go/bin/job`.

## PATH

Make sure that directory is on your `PATH`. The most portable shell-agnostic line:

```sh
export PATH="$(go env GOPATH)/bin:$PATH"
```

Add it to `~/.zshrc`, `~/.bashrc`, or whichever rc file your shell reads on startup. Then verify:

```sh
which job
job --help | head -1
```

A first-run `job --help` is the fastest sanity check that install + PATH are wired correctly.

## From a local checkout

If you've cloned the repo and want to iterate on Jobs itself, the [Makefile](https://github.com/bensyverson/jobs/blob/main/Makefile) has the canonical targets:

```sh
make install            # go install ./cmd/job → $GOBIN
make build              # local binary at ./job (no install)
make run ARGS="ls --mine"
make test
```

`make help` prints every target.

## Tell your agent to use it

Jobs is most useful when an agent treats it as the planning surface — not built-in plan/to-do tools. Add this line to your repo's `AGENTS.md` (or `CLAUDE.md`, or whichever instructions file your agent reads):

```text
To create and manage plans and task lists, always use the `job` command.
```

If the agent still falls back to its built-in plan or to-do tooling, strengthen the language — make the line `IMPORTANT:` and explicit, e.g. *"Never use the built-in plan/to-do tools. The `job` CLI is the only plan and task interface."*

That's the whole install. Next: [Initialize a database](../initialize/) and [walk a plan from author to done](../first-plan/).
