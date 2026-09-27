# Backlog

Work decided against, parked rather than dropped in silence. Nothing here is scheduled or blocking — active work lives in `job`. Read it before proposing something that sounds novel; it may already have been weighed and parked.

- **One dated H2 per item:** what it is, why it's parked, and *what would un-park it*.
- **Delete an item when it lands, or when it stops being plausible.** A long list is one nobody reads.
- **Something parked that turns out to be needed becomes a task in `job`** — move it, don't work it from here.

Format: one dated H2 per entry, a headline, then what it is, why it's parked, and the trigger that would revive it.

---

## 2026-08-29 — `job status` reports whether `.jobs.db` is gitignored

A line in `status` saying whether the database is ignored or tracked. Parked because it needs a `git` shell-out inside a read verb, and the hint `init` prints when the patterns are missing covers most of the need (see [2026-08-29-init-identity-and-gitignore.md](2026-08-29-init-identity-and-gitignore.md)). Un-parked if a shared (tracked) database becomes a supported mode, when knowing which mode a repo is in matters on every session.

## 2026-09-01 — `job compact`: snapshot the log and archive the files it summarizes

Once `.jobs/log/*.jsonl` is the record (see [2026-09-01-git-native-event-log.md](2026-09-01-git-native-event-log.md)), a long-lived repo's log grows without bound. The `snapshot` event that adoption writes is the primitive: `compact` would write one at the head and move the files it summarizes to an archive directory. Parked because the numbers say it is years away — this repo's whole history is about a megabyte of text — and git delta-compresses appends well. Un-parked when a rebuild is measurably slow or a clone's `.jobs/` is noticeably large.

## 2026-09-02 — store format check on the sync hot path

**What:** the store format guard (`checkStoreFormat`, `internal/job/store_format.go`) runs only when a rebuild reads the log files. If a newer binary has already rebuilt the cache in place, the watermarks match and an older binary takes `syncStore`'s hot path — one `stat` per file, no line read, no format check — so it opens a cache it did not build and appends under its older vocabulary. The cache it reads is correct, because the newer binary built it, which is why this is a small hole rather than a live bug; the schema check catches it only when the format bump also shipped a migration.

**Why parked:** closing it means either recording the format in the cache (a migration plus a write on every rebuild) or reading each file's first line on every command. Neither is worth it while every checkout here runs a current binary.

**Un-park when:** the format is first bumped past 1, or a second machine runs a binary that is routinely behind.

## 2026-09-02 — emphasis in dashboard prose

Descriptions and notes render the block subset (paragraphs, lists, fenced code, hard breaks) plus an inline pass for code spans, `[text](url)` links and short-id autolinks (`internal/job/prose.go`, `internal/job/prose_inline.go`, `assets/js/prose.mjs`, project/2026-09-02-prose-rendering.md). Emphasis — `*bold*`, `_italic_` — is the one inline construct still left literal, and headings, block quotes, images and raw HTML remain out of scope.

**Why parked:** emphasis is decoration, where every construct already rendered carries information (a command, a destination, a task). Its delimiters are also the fiddly part of CommonMark — flanking rules, intraword underscores, nesting with code spans — and every rule has to be written twice, once in Go and once in the JS twin, with the parity test as the only thing holding them together.

**Un-park when:** someone writes emphasis in a note and the raw asterisks read as noise on the dashboard, or when a second inline construct (a heading, a table) is wanted at the same time — at which point pulling in goldmark for the server and reconsidering what the scrubber renders beats hand-writing a third pass. Goldmark is the pick, being what the docs site's Hugo already uses; the subset rendered today is forward-compatible with it.

## 2026-09-02 — criterion autolinks in the plan scrubber

Scrubbed plan rows autolink task ids only. The head-frame island (`internal/web/initial`'s `CriterionState`) carries each criterion's label and state but not its short id, so a replayed frame cannot resolve a backticked criterion id, and linking only the ones added by a replayed `criteria_added` event would be inconsistent. Un-park when scrubbed rows need parity with the server-rendered page: add `short_id` to the island's criterion shape and have `proseLinksFromFrame` map criteria to `/tasks/<task>#crit-<id>` when exactly one task owns the id.

## 2026-09-26 — a cache-only replica run with no way into the log

**What:** when the cache holds a replica's events and that replica's log file never existed (the cache predates the log), and the run is gapped (purge erased rows) or declares a lower store format than its lines need, the bootstrap cannot write it out. Since eaed1e5 the store reads `log incomplete`, writes by that replica are refused and `job rebuild` refuses; the only exit is moving `.jobs.db` aside, which loses the events only that cache held. A fix would adopt such a run the way pre-store history is adopted — legacy lines plus a snapshot pinning the state — instead of copying it verbatim.

**Why parked:** it needs a cache old enough to predate the log *and* a purge or a format-2 event in that replica's run before the log existed; no store here is in that state, and the refusal already keeps it from getting worse.

**Un-park when:** anyone hits the `log incomplete` refusal with no copy of the file to restore.

## 2026-09-26 — `syncStore`'s in-sync shortcut can hide a missing file

**What:** the shortcut at the top of `syncStore` keeps the common path to one `stat` per file and skips the missing-file query. If another replica's file goes missing while this replica's own writes keep its file matching its watermark, status reads "in sync" when it is "log incomplete". Nothing is lost — seqs are not reused, `job rebuild` refuses, and the next `git pull` reports it — so the only effect is a wrong status line.

**Why parked:** the fix costs a query on every command to correct a label in a state that corrects itself.

**Un-park when:** someone is misled by the label, or the missing-file query becomes free (e.g. the watermarks table grows a per-replica row the shortcut already reads).

## 2026-09-26 — `FindDeadlocks` cost on a very large open frontier

**What:** `job status` runs `FindDeadlocks` (`internal/job/wait_graph.go`), which issues two queries per open task. On this repo's store it takes ~0.5 ms; on a synthetic store with 1,500 open, chain-blocked leaves it took 140–320 ms and dominated `status` (measured by the agent on tgA3ax with a scratch build and `/usr/bin/time -p job status --format=json`).

**Why parked:** 1,500 simultaneously open blocked tasks is a stress case, not a store anyone has.

**Un-park when:** `job status` is measurably slow on a real store — then load the blocks table and open-children map in two queries up front instead of per node.
