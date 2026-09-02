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

## 2026-09-02 — full markdown rendering in the dashboard

Descriptions and notes are markdown prose, but only the block subset is rendered: paragraphs, lists, fenced code, hard breaks (`internal/job/prose.go`, `assets/js/prose.mjs`, project/2026-09-02-prose-rendering.md). Inline syntax — backticks, emphasis, links — stays literal. Parked because a full parser is a dependency on both surfaces (goldmark for Go, a JS twin for the scrubber) plus a sanitization surface, and the subset already fixes the reflow pain. Un-park when someone wants inline code or links rendered on the dashboard; goldmark is the pick, being what the docs site's Hugo already uses, and the block subset is forward-compatible with it.
