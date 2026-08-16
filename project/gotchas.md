# Gotchas

Project-specific traps that cost real time and that no general rule predicts. Read at session start; append when you hit one.

- **Fix at the source when you can.** A gotcha is a bug report on our tooling, not a permanent fact — if it can be fixed in code, file it in `job` and fix it instead of recording it here.
- **Delete anything that becomes obvious, gets fixed, or stops recurring.** Keep this list short; a long list is one nobody reads.
- **Feedback about `AGENTS.md` itself** — a rule that was wrong, misread, or cost time — goes here too, prefixed `rule:`. It is harvested when the shared rules are reviewed.

Format: one dated H2 per entry, a bold headline, then what happened and what to do instead.

---

## 2026-08-16 — `go fix` can rewrite staged files silently, and the hook fails the commit

**`scripts/git-hooks/pre-commit` runs `go fix ./...` and hard-fails if it modified any staged file**, printing a re-stage hint. It compares the worktree against the staged set rather than capturing stdout, because `go fix` (the Go 1.22+ loop modernizers in particular) rewrites files in place without printing anything — an earlier version of the hook let those silent rewrites through and they landed as follow-up commits. Run `go fix ./...` yourself until it is clean, then stage, then commit.

## 2026-08-16 — the pre-commit hook does far more than format and test

**`scripts/git-hooks/pre-commit` also runs `go vet ./...`, `go mod tidy` (fails if `go.mod`/`go.sum` change), `go test ./...`, `go build ./...`, and `node --test internal/web/jstest/*.test.mjs` when Node and that directory exist.** Running only `gofmt` and the tests you touched will still bounce the commit. The hook in `.git/hooks/` is a *copy* and can drift from `scripts/git-hooks/pre-commit` — if the two disagree, the one in `scripts/` is canonical; re-install it.

## 2026-08-16 — the dashboard's JS tests are not part of `make test`

**`make test` is `go test ./...` only; the dashboard's JS tests are `make test-js` (`node --test internal/web/jstest/*.test.mjs`).** They live outside `internal/web/assets/` deliberately so they are not embedded and served, and they import the production modules from `internal/web/assets/js/`. A green Go suite says nothing about the dashboard — run `make test-js` after any change under `internal/web/`.

## 2026-08-16 — rule: the web rules assume a public site

**`web.md`'s "Public pages carry rich `<head>` metadata including schema.org data" has no meaning for this dashboard**, which is localhost-only, read-only, and not crawled. The head notes the exception, but every internal-tool repo that adopts `web` will need the same note — the module could scope the bullet to public-facing pages itself.
