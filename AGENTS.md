IMPORTANT: As you implement features, keep the [documentation](docs/content/docs/) up to date; update [README.md](README.md) only when a doc file is added or new users must know.

## Overview

You are working on Jobs, a hierarchical task manager for the CLI, backed by an event store in SQLite.

## Documentation

- [docs/content/docs/](docs/content/docs/) — the published documentation site (Hugo); `make docs` serves it locally
- [DOCS.md](DOCS.md) — the full CLI reference; the fastest way to learn what a command does
- [README.md](README.md) — what Jobs is and a quick start, written for humans
- [DESIGN.md](DESIGN.md) — the dashboard's design system: tokens, type scale, component specs
- [project/](project/) — dated design documents and agent-feedback reports; [project/gotchas.md](project/gotchas.md) holds project traps and rule feedback
- [Makefile](Makefile) — `build`, `install`, `test`, `test-js`, and the docs targets
- [scripts/](scripts/) — re-runnable tooling (screenshots, snapshot replay, getting-started verification)

## Architecture

`internal/job` is the product — the SQLite event store, the plan grammar, and the rendering. `cmd/job` is a thin cobra CLI over it, and `internal/web` is a server-rendered dashboard over the same core.

- Schema changes are numbered migrations in [internal/migrations/](internal/migrations/). They are applied when the database is opened, so *any* `job` invocation migrates — there is no server to restart.
- [DESIGN.md](DESIGN.md) is the design system the web rules below defer to, authoritative for everything under `internal/web/` — including its desktop-first density target, which outranks the web rules' mobile-first default. The dashboard is localhost-only and read-only, so the rule about public-page `<head>` metadata does not apply here.
- The dashboard's JS is mostly plain progressive-enhancement modules in `internal/web/assets/js/`, not custom elements; only `peek-sheet` and `live-region` are registered, and neither uses a shadow root. The web rules' one-element-per-file-with-a-shadow-root convention governs *new* components — don't retrofit the existing scripts to it as a side quest.
- **This repo builds `job`.** The `job` on your `PATH` and the `./job` at the repo root are both stale until `make install` / `make build`, so a CLI change you just wrote is not exercised by running `job` until you rebuild.

<!-- agents:begin core@3a7a5e -->
## Working rules

**Understand the why.** If the goal behind a request isn't clear, ask before solving — beware the XY problem.

**Diverge, then converge.** First brainstorm options (create choices), weigh them against the user's goals, recommend one (make choices), confirm, then execute.

**Ambiguity.** If the *code* could go several ways, choose the idiomatic one for the language. If the *requirement* is ambiguous or the question is architectural, stop and ask — don't decide.

**Dependencies.** Avoid them unless re-implementing would be unreasonable; ask before adding one; each is security and maintenance surface.

**TDD, strictly red/green.** Write tests for every case and every new method first, watch them *all* fail, then implement. A test that is green during red tests nothing — remove or rewrite it. If an existing test must change to pass because the behavior or expectation has changed, explain why clearly. Every bug fix starts with a regression test.

**Plans and tasks live in `job`.** Open every session with `job orient` (no arguments), then read `project/gotchas.md` — while reading, prune it: delete any entry that's now fixed, obvious, or a general rule, marking it `rule:` first if it's general. Don't use Plan Mode or ad-hoc todo lists.

**Don't tour the codebase.** Start from the README and the docs (an Explore agent is fine); dig only where the task leads — once you have a specific need, read as much as that need requires.

**Scripts.** Analysis tooling goes in `scripts/` so it can be re-run — check there before writing one.

**Critique before declaring done.** Re-read the original request: is the need actually met? Do lint and tests pass? Are docs updated? What would an expert flag? Fix serious flaws before reporting.

**Tidiness.** No stray files in the repo root; delete transients, and file valuable artifacts (reports, scripts) where they belong.

**Documentation.** Keep the project docs current as you build. Touch the README only when a doc file is added or new users must know.

**Gotchas.** When a project quirk costs you time and no rule predicts it, append it to `project/gotchas.md`. If a rule in this file was wrong or misled you, record that there too, prefixed `rule:`.

**Where these rules come from.** The marked regions are generated and shared across repos via a CLI tool named `agents`; don't edit inside them. If a rule here is wrong or cost you time, say so in `project/gotchas.md` prefixed `rule:`; that is how shared rules get reviewed.

**Local rulings.** A repo-local ruling, or an override of a shared rule, lives in the project-owned head of `AGENTS.md`, above the generated regions — say plainly that it overrides, and link a dated project doc for the why.

## Git

- Offer to commit when a unit of work is complete and accepted. Rebase onto upstream; ask on real conflicts, explaining the conflict in plain terms first.
- Commit all uncommitted files together — later changes usually depend on earlier ones, and a half-working state helps nobody. Never amend.
- The subject completes "This commit…": present-tense verb first — "Adds…", "Fixes…", "Retires…". Detail goes in the body.
- Pass the message with `-F <file>`, not inline `-m`; the shell interprets `-m` first. Same for `job`: `note`, `done`, `add` and `edit` all take `-F <file>` (`-F -` reads stdin).
- Pre-commit hooks run the formatter and tests. Run them yourself first (see the stack rules).
- Never pipe a gating command (`git commit … | tail`) — the pipe swallows its exit status, so a following `&&` runs even after a failure.
<!-- agents:end core -->

<!-- agents:begin principles@7a5b19 -->
## Principles

Defaults, not laws. When we break one, we do it consciously and say so in the report and the docs.

- **Pragmatism.** Builders, not purists. Practical choices that serve the near-term goal and protect the long-term one.
- **Eat the frog.** No band-aids. Given an easy-but-compromised path and a correct one, take the correct one; fix problems at the source. Keep YAGNI in mind, but when a need is obvious, don't underdeliver.
- **Composability.** Simple, strong components composed into systems — never a monolith.
- **Library + thin executable.** Core logic in a library; the app or CLI is a light consumer, so the core can be reused elsewhere. An adapter that holds a decision rather than wiring one is a bug.
- **Decoupling.** Tight coupling makes testing, debugging and refactoring hard — separate concerns. Separating a model, its storage and its UI is the everyday case: databases and UI frameworks change; today's web app may grow a CLI or mobile app.
- **Just enough abstraction.** One layer around an LLM provider is prudent; a `TextGenerationProvider` above it is not.
- **Readable file sizes.** Aim for files a reader can hold in their head (a few hundred lines; ~400 is the comfortable ceiling). Past ~2k lines, navigation degrades and errors accumulate; splitting also makes functionality discoverable by filename.
- **Comments say why, not what.** Doc comments state *what* concisely; other comments only explain the non-obvious. No change history in comments. Most code needs none.
- **Strongly typed.** Prefer enums, named constants and config over magic strings and numbers; prefer typed structs over dictionaries, even for wire types. Two packages exchanging data across a serialization seam share **one** struct that both import, never a hand-written twin on each side — the type checker cannot see across encode/decode, and two definitions drift. Given a bool and a typed constant, take the typed constant: a bool named for one consequence gets reused to gate the others until it means several things, so name the underlying *fact* as a type and let the behaviors follow.
- **Previews.** Give each UI component a way to render in its various states — a SwiftUI `#Preview`, a demo page, a story — the foundation for tests and for human review.
- **Async by default.** Keep the app interactive during heavy work; surface loading and error states. On the web, prefer progressive enhancement over full reloads.
- **Event streams where they fit.** Append-only logs are auditable, undoable, and time-travelable.
<!-- agents:end principles -->

<!-- agents:begin stage-build@3d5d83 -->
## Stage: BUILD

Pre-launch, zero users, no existing data. Never spend effort on backward compatibility — assume every use is green-field — but flag breaking changes and update the affected tests. Be ambitious: if a feature is important, build it fully now rather than an MVP; balance that against over-engineering and future-proofing.
<!-- agents:end stage-build -->

<!-- agents:begin go@91ab6a -->
## Go

- Before committing: `go fix ./...`, `gofmt -w .`, `go vet ./...` and `go mod tidy`, then the tests you touched. `go fix` converges over several passes — "re-run to apply more" is progress, not failure; re-run until clean before editing code.
- **Run `go fix ./...` before staging, not just before committing.** A pre-commit hook that re-stages `gofmt` rewrites will not re-stage `go fix` rewrites: a file `go fix` changed that is already gofmt-clean commits unfixed, and your working tree quietly diverges from what you committed.
- **Tests that share a database need `-p 1` and a database per agent.** `go test ./...` runs packages in parallel, so packages that seed the same fixtures and truncate the same tables produce a wall of unrelated-looking failures that survives a re-run and reads as a real regression.
- **Schema changes are numbered migrations** in the project's migrations directory (the head names it). Never run one by hand — the binary migrates when it starts or opens the database and records the version; the next run applies it. Read the full note history on the task (`job show <id>`) before writing schema; it is the most expensive thing to change.
- **On SQLite:** **`CHECK` passes on NULL.** `CHECK (a = b)` admits any row where either side is NULL — guard every comparison with `IS NOT NULL`, or it enforces nothing.
- **On SQLite:** **NULLs are distinct in a `UNIQUE` index.** A nullable column in a dedup key admits duplicates forever; wrap it in `COALESCE(col, '')` in the index expression.
- **On SQLite:** **Never hold a transaction open across a model or network call.** `BeginTx` is deferred — it pins a read snapshot at the first read, so the write at the end fails with `SQLITE_BUSY_SNAPSHOT` if any other connection committed meanwhile, and `busy_timeout` cannot rescue it because waiting cannot refresh a stale snapshot. Split into a step that reads and calls but writes nothing, and a short transaction that persists the result.
- Wire types are structs, not `map[string]any`, unless the shape is genuinely dynamic.
- **`r.ParseForm()` reads a body only when it is urlencoded**; for multipart it leaves it empty without erroring. Keep one wire format per route — a handler that accepts two body shapes needs two sets of checks where the design wanted one.
<!-- agents:end go -->

<!-- agents:begin web@32992c -->
## Web

- **Vanilla HTML, CSS and JS** — no frameworks or build tools beyond the server. WebComponents are the enhancement layer: the server ships each element's real content as HTML inside it, and the component's JS upgrades what's already there — never an empty tag that renders itself.
- **Follow `DESIGN.md` when the project has one** — tokens, type scale, color roles and component conventions come from there, not from ad-hoc values.
- **Every page works without JavaScript by default — ship full HTML.** Then use JS to enhance where users expect modern interactivity (re-sorting a list, a live-updating form field) so those don't need a full reload. No client-side routing and no client-side data fetching to render a page: this is the middle ground between 1994-style brutalism and shipping one `<div>` and a JS blob. A surface that is inherently live — a chat, a streaming dashboard — is the exception; the head names it.
- **No inline `style` attributes**; styles live in stylesheets under a class or selector.
- **Responsive and mobile-optimized from the first draft.** Without a brand identity, default to a simple, modern, clean aesthetic.
- **Paths over query strings** (`/api/people/89`, not `?id=89`); queries only for search, sort, filters.
- **Public pages carry rich `<head>` metadata** including schema.org data.
- **Server-side tests cannot see the browser.** Keep a JS/browser runner and run it by hand whenever you change behavior a browser can observe — a green server suite is not evidence about the page.
- **`sleepy` is the browser-evidence tool on this machine** (SleepyHollow, headless WebKit, globally installed — `sleepy --help`): `load` for HTTP facts and console errors, `shot --full-page --size WxH` at any viewport, `ax` for the accessibility tree, `wire` to prove the request inventory (e.g. zero external requests), `query`/`find`/`style` for semantic checks, `open`/`fill`/`click` + `--session` to drive flows. Renders offscreen — never a window on the user's display; anything else an agent launches must be windowless too. **`sleepy` alone is the day-to-day check** — don't run a second engine for routine work; at final integration and QA, check once in a Blink engine (Chrome plus Android is what most visitors see; Firefox is not needed). Chrome for Testing lives in puppeteer's cache (`~/.cache/puppeteer/chrome/*/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing`; glob the version); launch it `--headless=new --disable-gpu --no-first-run --password-store=basic --use-mock-keychain --user-data-dir=$TMPDIR/chrome --window-size=WxH --screenshot=out.png <url>` — the two keychain flags are mandatory or every launch raises a macOS keychain prompt that stacks and outlives the process. Two traps: **`eval` runs in an isolated world** — page-JS state (upgraded component methods) reads as missing while DOM state shows; don't diagnose components from it — and **`pdf` renders one unpaginated screen-media sheet**; use an `NSPrintOperation` harness for print evidence (working example: nobedan `scripts/print-proposal/`).
- **New components: one custom element per file, named for the element** (`<app>-thing` lives in `app-thing.js`), with a shadow root so component styles don't leak. Don't retrofit existing scripts as a side quest.
<!-- agents:end web -->

<!-- agents:begin docs@7ba2fd -->
## Documentation practice

- **Plans, findings, designs and decisions go in `project/` as dated documents** (`YYYY-MM-DD-title.md`) — the written history of the project. They are point-in-time records: correct an earlier one *in place*, as a marked block quote, rather than silently editing a number or leaving a stale claim standing.
- **Every figure names the tool and flags that reproduce it.**
- **Work decided against goes in `project/backlog.md`**, not into silence: one dated H2 per item — what it is, why it's parked, and *what would un-park it*. Nothing there is scheduled or blocking; active work lives in `job`. Check it before proposing something that sounds novel.
- **When a finding overturns a premise, edit the premise.** Readers act on the title and opening; a correction appended underneath doesn't reach them.
- **A wrong documented cause is worse than none** — it stops the next reader looking. Correcting one means saying it was wrong, not quietly rewording.
- **Open the note before you cite it**, and check whether a recorded ruling has been superseded before passing it on.
- **Every repo has a README; if none exists, write one (delegate it if you can).** It is tight: the project's name and a one-line description a non-technical reader understands (6th-grade reading level); one short paragraph of what it is; how to install or consume it; a crisp Quick Start with an example or two; links out to the specific docs for anything more; authorship and license at the end.
- **The head of `AGENTS.md` lists where the docs live; keep that list current.**
<!-- agents:end docs -->

<!-- agents:begin delegation-brief@4fe3f0 -->
## Delegating to subagents

Design on the main thread; dispatch execution to agents for anything larger than a small change. **Read `project/agents/delegation.md` before dispatching** — it carries what to delegate, the worktree workflow, the traps, and the briefing template.

- Commit **and push** before dispatching: worktrees branch from `origin/main`, so anything unpushed is invisible to the agent.
- Assign work by files, not strictly by task, and read across every open tree — there is usually more parallel work than `job orient` showed.
- Agents `claim` and `note` (unique `--as` each), never `done`, and never commit; the main thread integrates, runs the full suite once, commits, then closes leaves.
- Choose the model deliberately, end every brief with **"what in this brief is wrong?"**, and verify what comes back — the pushback, not the typing, is usually the value.
<!-- agents:end delegation-brief -->
