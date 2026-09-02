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

## woodcase under the Claude Code sandbox (2026-08-30)

- `~/.woodcase` and `~/Library/Caches/woodcase` are allow-listed in `~/.claude/settings.json` (added 2026-08-30), so every read/write verb, the activity log and `shot` run sandboxed. Only `woodcase serve` needs the sandbox off — it binds a port, which no allow-list grants; the same is true of `job serve`. In practice the human runs `serve`.
- rule: the harness note "never invent cache or `HOME` redirects to make a toolchain work in-sandbox" was right and cost time when ignored — redirecting `WOODCASE_HOME` hid the file from the user's own `serve`, which lists files from the activity log. Re-run the one failing call unsandboxed, or allow-list the tool's own state directory.
- `which woodcase` reported "not found" inside the sandbox while the binary ran, because `~/.swiftpm/bin` was unreadable; it is allow-listed now. Don't conclude a working command is a shell function.
- In zsh a bare word starting with `=` (`echo =====`) is `=cmd` expansion and errors as "not found"; quote separators.
- Lucide's check icon is `circle-check`; `check-circle-2` renders nothing, silently — read the PNG.

## 2026-09-01 — a shared JS module must be listed in the layout's importmap, or it 404s silently

**Assets are content-fingerprinted, so `import … from "./position.mjs"` inside a hashed module resolves to `/static/js/position.mjs`, which `Manifest.Handler` 404s on purpose.** The layout's `<script type="importmap">` block is what maps each unhashed specifier onto the fingerprinted URL — and a module missing from it takes down the *whole* module graph downstream of it with **no console error at all**: `sleepy load` reported zero console errors while the scrubber silently never entered history mode. `sleepy wire` was what showed the 404. `internal/web/templates/importmap_test.go` now scans every relative import under `assets/js/` and fails when one has no importmap entry.

## 2026-09-01 — `job merge` is a one-shot on a store-backed database

**`merge` predates the store and works on two `.jobs.db` caches, writing its result straight into the local cache.** The next `job` command therefore *adopts* that cache — appending the arriving events plus a snapshot to this replica's log and keeping `.jobs.db.pre-adopt` — which changes the local event history. Re-running the same merge then fails with `these databases are unrelated: their event logs differ from the first event`, so the documented "merging the same pair twice changes nothing" no longer holds once a store exists. Verified 2026-09-01 with a freshly built binary against two divergent copies (`.jobs.db` copied, both sides written, `job merge ../two/.jobs.db` twice: first succeeds, second exits 1). The docs now say a merge is applied once; if idempotence is wanted back, merge has to go through the log instead of the cache. Two clones of one repo never need `merge` — they share the log, so `git pull` is the whole story.

## 2026-09-02 — Chrome for Testing writes the screenshot and then never exits

**Headless Chrome for Testing (`--headless=new --screenshot=…`) writes the PNG and then hangs in this harness**, so a foreground call times out with no output and looks like a failure. macOS has no `timeout`, and `--timeout=` does not make it quit. Run it detached or in the background, wait for the PNG to appear, then `pkill -f "Chrome for Testing"`, and read the PNG. `job serve` needs `--bind 127.0.0.1:<port>` for a fixed port and must be started detached (`nohup … & disown`) unsandboxed; kill it by port afterwards.
