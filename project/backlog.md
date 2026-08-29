# Backlog

Work decided against, parked rather than dropped in silence. Nothing here is scheduled or blocking — active work lives in `job`. Read it before proposing something that sounds novel; it may already have been weighed and parked.

- **One dated H2 per item:** what it is, why it's parked, and *what would un-park it*.
- **Delete an item when it lands, or when it stops being plausible.** A long list is one nobody reads.
- **Something parked that turns out to be needed becomes a task in `job`** — move it, don't work it from here.

Format: one dated H2 per entry, a headline, then what it is, why it's parked, and the trigger that would revive it.

---

## 2026-08-29 — `job status` reports whether `.jobs.db` is gitignored

A line in `status` saying whether the database is ignored or tracked. Parked because it needs a `git` shell-out inside a read verb, and the hint `init` prints when the patterns are missing covers most of the need (see [2026-08-29-init-identity-and-gitignore.md](2026-08-29-init-identity-and-gitignore.md)). Un-parked if a shared (tracked) database becomes a supported mode, when knowing which mode a repo is in matters on every session.
