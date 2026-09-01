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
