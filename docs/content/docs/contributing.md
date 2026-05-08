---
title: Contributing
weight: 8
---

How the codebase is organized, how to write a migration, where the test helpers live, and how to wire up the pre-commit hook before your first commit.

## Package layout

```
cmd/job/                 cobra CLI — package main
internal/job/            domain — runs, queries, renderers, event logic
internal/migrations/     forward-only SQL files (NNNN_*.sql), embed.FS
internal/web/            read-only web dashboard
docs/                    Hugo + Hextra documentation site (this site)
scripts/                 helper scripts and the vendored git hooks
```

A bit more on each:

- **`cmd/job/`** is the cobra CLI. One file per verb (`add.go`, `done.go`, `claim.go`, …) plus `commands.go` for `newRootCmd` and shared CLI helpers. The verb files are thin: they parse flags, call into `internal/job`, and render.
- **`internal/job/`** is the domain. Imported as `job "github.com/bensyverson/jobs/internal/job"` and accessed as `job.RunAdd`, `job.RunDone`, etc. The CLI is a translator between `flag.Set` and these functions; everything that mutates state lives here.
- **`internal/migrations/`** holds forward-only SQL files (`NNNN_*.sql`), exposed as an `embed.FS` via `migrations.FS()`. The runner in `internal/job/migrations.go` applies unapplied migrations inside their own transactions on every `OpenDB`. Idempotent. See [Migrations](#migrations) below.
- **`internal/web/`** is the read-only web dashboard. Subpackages: `server/` (http.Server lifecycle + mux), `handlers/` (one file per view; a `Deps` bundle for DB, templates, broadcaster), `templates/` (embedded `html/template` engine — layout + partials + pages), `assets/` (embedded CSS/JS/fonts with a content-fingerprint manifest served under `/static/`), `render/` (shared helpers — actor/label color, relative time), `broadcast/` (1-Hz event poll + per-subscriber fanout).

## Migrations

Schema changes follow one rule: **add a numbered file; never edit an applied one.**

To make a schema change:

1. Drop a new file under `internal/migrations/` with the next numeric prefix — e.g. `0042_add_archived_column.sql`.
2. Restart the server (or run any CLI command — every `OpenDB` runs the migrator).
3. The runner applies your migration inside its own transaction and records it. Subsequent opens are no-ops for that file.

What this means in practice:

- **Never edit an applied migration.** The runner identifies migrations by filename; modifying one already applied to a database leaves that database in a state different from a fresh checkout. Add a new migration that rewrites or adds to the schema instead.
- **Don't `migrate` manually.** There is no separate command for it; the migrator is part of `OpenDB`. The first command you run after dropping in a new file applies it. This includes `job ls`, `job status`, anything — every entrypoint opens the database, every open runs the migrator.
- **Forward-only.** There are no down migrations. If a migration was wrong, write a new one that corrects it.

The runner lives at `internal/job/migrations.go`; the embedded FS at `internal/migrations/`.

## Test helpers

Both the package's own tests and the CLI integration tests under `cmd/job/` share helpers in `internal/job/testhelpers.go`:

- `job.SetupTestDB(t)` — opens a fresh in-memory or temp-file DB, runs migrations, returns a handle.
- `job.MustAdd`, `job.MustAddDesc` — create a task, fail the test on error.
- `job.MustDone`, `job.MustClaim` — close or claim with assertion.
- `job.MustGet` — fetch a task and assert it exists.
- `job.TestActor` — the canonical fake identity for tests.

Reach for these instead of building scaffolding by hand. They're a **non-test file** on purpose so callers in other packages can import them.

Run the full suite with:

```sh
make test
```

JS tests for the web dashboard live under `internal/web/jstest/` and run via `make test-js`.

## Pre-commit hook

The repo ships a pre-commit hook in `scripts/git-hooks/pre-commit`. Wire it up once per clone:

```sh
git config core.hooksPath scripts/git-hooks
```

The hook runs `go vet`, `go fix`, `gofmt`, `go mod tidy`, the test suite, and `go build` against the staged changes, and aborts the commit on any rewrite or failure. If `go fix` modifies a staged file, the hook fails with a re-stage hint — silent rewrites are not allowed to slip into a commit.

Run any of these by hand from `make`:

```sh
make fmt                # gofmt
make fix                # go fix
make vet                # go vet
make test               # full test suite
make build              # local binary at ./job
make help               # every target
```

Run from the repository root — the Makefile keys off relative paths.

## Documentation

This site lives under `docs/` (Hugo + Hextra). When you change a CLI flag, an event shape, or anything user-visible, update the relevant page under `docs/content/docs/`. The plan-grammar JSON Schema is generated:

```sh
make docs-schema        # regenerate docs/content/docs/plan-grammar/_schema.json
```

The repo's [`CLAUDE.md`](https://github.com/bensyverson/jobs/blob/main/CLAUDE.md) treats docs updates as part of any feature change — README only when a brand-new doc file lands or when new users *need* to know about a change.
