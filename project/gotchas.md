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

## 2026-08-28 — agent briefs: `events.go` is the reader, not the recorder

The sandbox facts that cost three of four parallel agents time now live in `project/agents/harness.md` — point briefs there. **`internal/job/events.go` reads events (`log`/`tail`); the recording helper is `recordEvent` in `internal/job/database.go`** — a brief that points at `events.go` "for the pattern" sends the agent to the wrong file.

**`git add -A` on the main checkout stages `.claude/worktrees/*` as embedded repos** and the pre-commit hook refuses the commit. The directory is now gitignored; if you see "adding embedded git repository", `git reset -- .claude/worktrees`.

**`go fix ./...` rewrites unrelated files** (`slices.Backward`, `strings.Cut`, `errors.AsType` as of Go 1.24) and every parallel agent's diff carries the same three hunks. Harmless — they apply identically — but expect the first patch to bring them and the rest to conflict trivially on them.

## 2026-08-28 — integration: `--all-passed`, stale agent claims, and `git apply --3way` stages

**`job done` refuses a leaf whose criteria are unticked**, even when the integrator has just verified every one. Verify, then close with `job done <id> --all-passed`; don't tick rows one at a time. **Agents forget to `release`** — say "RELEASE when done" in the brief in capitals, and if `done` reports "claimed by agent-x", `job release <id> --as agent-x` first. **`git apply --3way` stages what it applies**, so review with `git diff --cached`, not `git diff`, or the diff looks empty. **Two dashboard agents will both append to `components.css`** and both define `mustAddIssueRoot`-style test helpers in package `handlers_test`; brief them to append delimited blocks (they merge with markers stripped) and expect one helper collision.

**The Issues root auto-closed when its last child closed** (fixed in 8988033) — if an issue root ever shows `[x]` again, `job reopen <root>` and check the cascade; and an issue root is never a candidate for `next --issues`, so an exhausted issue tree is "nothing next", not the root.

## 2026-08-28 — SSE frames are *named*, so a browser client sees only the types it subscribes to

**Every `/events` frame carries `event: <event_type>`, and a named SSE frame never reaches a `message` listener.** `live.js` therefore registers one `addEventListener` per type from a hardcoded list — and that list had gone stale, so `found_in_set`, `kind_changed`, `criteria_added`, `criterion_state`, `claim_expired`, `focus_*`, `reparented` and `purged` were dropped before any live module saw them: the rows simply never arrived on /log. If a live view is missing one event type but fine on reload, check that list first. `TestLiveSubscribesToEveryEmittedEventType` now scrapes the `recordEvent` call sites in `internal/job` and fails when it falls behind.

## 2026-08-28 — `sleepy` must be invoked by absolute path from a worktree

**Run it as `/Users/ben/.swiftpm/bin/sleepy`, not `sleepy`.** Invoked through `PATH`, it resolves its own session helper relative to the working directory and dies with "Couldn't start a session helper from '<cwd>/sleepy': The file 'sleepy' doesn't exist" — only for the session verbs (`open`), so `load`/`shot` look fine and then `open` fails.

Also: sleepy renders offscreen with `document.visibilityState === "hidden"`, so **CSS animations never advance** — an element that animates in from `opacity: 0` screenshots as blank space. `getAnimations().forEach(a => a.finish())` before `shot` if you need to see it.

## 2026-08-28 — every layout `<script>` runs on every page, so a loose self-guard silently claims another view's markup

**`log-live.mjs` guarded itself with `document.querySelector(".c-log")` — and the single-actor page's Events list is also a `.c-log`.** Both it and `actors-live.js`'s private row renderer were therefore live on `/actors/{name}`, racing to prepend the same row; the loser's dedup check swallowed the duplicate, so nothing looked broken and the page had quietly been rendering *log-live's* markup, not the markup its own module wrote. Every module in `layout.html.tmpl` loads on every page, so a self-guard selector has to name the view it owns, not a class it happens to share: `log-live.mjs` now takes `.c-log:not([data-actor-events])` and `actor-single-live.mjs` owns the rest. If two modules can plausibly match the same node, say which one wins in a comment on both.

## 2026-08-29 — rule: a docs pass cannot touch `project/agents/*.md`

**Every file under `project/agents/` is a generated region**, so a docs leaf that lists one of them (as the init-identity plan did for `jobs.md`) cannot edit it; the fix goes through the shared-rule review. `jobs.md`'s "recorded at `job init`" line is still true after `init --as` became required, just no longer the whole story.
