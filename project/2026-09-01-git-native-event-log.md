# The log is the record: a git-native event store for Jobs

*2026-09-01. Prompted by Ben working across several machines, one repo whose `.jobs.db` was copied and edited on both, and the question "what obvious solution am I missing?" Talked through the same day; the import block at the end is the plan.*

## The problem

`.jobs.db` is a SQLite file that is gitignored on every machine. There is no way to carry a project's tasks to a second machine except copying the file, and once two copies have both been written to there is no way back: SQLite is a binary blob, so git can't merge it, and Jobs has no notion of a second writer.

Ben's first sketch was a seed database plus a delta file of changes since the last commit, checked in, with the local copy replaying the delta and then clearing it. That is most of the right idea. It is also a reinvention of something Jobs already has: the `events` table records every mutation with actor, type, timestamp and payload, and the dashboard's scrubber already replays it. The one step the sketch adds, clearing the delta, throws away the history git would otherwise keep for free.

## The vision

**The event log becomes the record and lives in git as text. SQLite becomes a disposable local cache of it.**

- A repo carries a tracked `.jobs/` directory holding the log. A fresh clone plus any `job` command rebuilds the cache and works. Nothing else needs to exist.
- Each machine appends only to its own log file. Two machines never write the same file, so git never sees a conflict. There is no merge driver, no CRDT, no daemon, no cloud. Git is the transport; `git pull` is sync.
- Merging is replay. Reading every log file and applying the union in a deterministic order gives the same tables on every machine. Most concurrent edits simply compose: one machine notes a task while another closes it, both apply. Only a genuine same-field conflict resolves last-write-wins, and the one lossy case, two machines claiming the same task while apart, is reported rather than hidden.
- SQLite stays as the engine. Every read verb and the whole dashboard are SQL queries over a tree, and a rebuild of a few thousand events is well under a second. What changes is its job: it is never written by a command handler again, only by the function that applies events. It can be deleted at any time.
- Existing users lose nothing. The first `job` command against a legacy `.jobs.db` converts it in place: every event it holds goes into the log, a snapshot event pins the exact current state, and the cache is rebuilt and checked against the old tables before the old file is retired.
- A PR's diff shows which tasks it closed and why, in plain text, and `git log -p .jobs/` is the tracker's history.

### What this is not

| Option | Why not |
|---|---|
| Commit `.jobs.db` | Binary. Every merge is pick-a-side and lose the other side's work. |
| Seed + delta files | The events table is the delta already; clearing it discards history; a single shared delta file conflicts on every merge. |
| Sync the `.db` through Syncthing or iCloud | Corrupts WAL-mode SQLite. |
| Litestream, Turso, Dolt | Cloud, a server, or a heavy dependency. |
| A CRDT library | The domain's operations are already idempotent or naturally LWW, and contention is rare. |
| Drop SQLite, query the log | Reimplements indexes, joins and the tree walk by hand for no gain. |

## Sizing

Numbers from this repo's own database, reproduced with:

```
ls -l .jobs.db
sqlite3 .jobs.db "select count(*) from events; select count(*) from tasks; select avg(length(detail)) from events;"
```

| | |
|---|---|
| events | 2,647 |
| tasks | 449 |
| mean payload | 284 bytes |
| log as JSONL, estimated | ~1 MB uncompressed |
| `.jobs.db` | 1.8 MB |

Git delta-compresses appends to a text file well, and this is one of the most active Jobs databases that exists. Rebuild cost is not a design constraint; the plan starts with a full rebuild on every change and only adds an incremental path if it is ever felt.

## Design

### The store

```
.jobs/
  log/
    k7Qx2m.jsonl      one file per replica, append-only, tracked
    Zp09aQ.jsonl
  local.json          this machine's replica id, clock, default identity, focus — ignored
.jobs.db              the cache — ignored, disposable
```

- **A replica is one checkout on one machine.** Its id is six base62 characters minted the first time the store is opened there, kept in `local.json`. A worktree is its own replica. Losing `local.json` just mints a new replica; the old file stays in the log and is still valid.
- **The record is `.jobs/log/*.jsonl`.** Every other file can be deleted and regenerated.
- **`--db` keeps its meaning and its default.** It names the cache path, and the store is the `.jobs/` directory beside it. Every brief, doc and script that passes `--db /abs/path/.jobs.db` keeps working. Beat renaming the cache to signal that it is disposable: the name appears in every doc and agent brief, and adoption should be invisible.
- **`job gitignore` writes two patterns:** `.jobs.db*` and `.jobs/local.json`. The `*` covers the WAL sidecars and the adoption backup.

### The event

One JSON object per line. Fields are fixed; payload shape is per type.

```json
{"v":1,"rep":"k7Qx2m","seq":412,"ts":1756742400123,"actor":"ben","type":"done","task":"VBF5u","data":{"note":"…","criteria":{"aB3":"passed"}}}
```

- `rep` and `seq` identify the event globally. `seq` is per-replica and gapless, so a reader can tell a truncated file from a complete one.
- `ts` is a hybrid logical clock in milliseconds: `max(wall, last_seen + 1)`, where `last_seen` is the largest `ts` this replica has read or written, kept in `local.json`. It makes cause sort before effect even when two machines' clocks disagree.
- **The global order is `(ts, rep, seq)`.** Every replica sorts the union of every file by that key and gets the same sequence. That is the whole merge algorithm.
- `task` and every reference inside `data` are short ids, never row ids. Row ids are minted by the cache and differ per machine.
- `data` is a typed Go struct per event type, one file of them, shared by the writer, the applier and the dashboard. Today's `map[string]any` payloads go.
- Task timestamps (`created_at`, `updated_at`, `closed`) come from the event's `ts`, not from the clock at apply time, so a rebuild reproduces them.

### Ordering within a parent

`sort_order` integers cannot merge: two machines inserting under one parent both shift the same rows. They are replaced by **fractional sort keys**, short strings with the property that a key can always be generated between any two neighbours. A `created`, `moved` or `reparented` event carries the task's new key, and applying it is a plain column write with no shifting. Two concurrent inserts into the same gap get distinct keys, and the tie-break is `rep`. Criteria get the same treatment.

### Apply

`apply(tx, event)` is the only code that writes `tasks`, `blocks`, `task_labels`, `task_criteria`, `found_in` or `users`. A command handler becomes:

1. Read the cache to validate (claim ownership, pending criteria, the tree).
2. Build the events the command means, cascades included.
3. Under the store lock, append them to this replica's file, then apply them to the cache in one transaction.

**Apply never derives.** If closing the last child closes its parent, the handler emits the parent's `done` too, as it does today. Apply stays a dumb, total function from events to rows, which is what makes it testable and what makes replay deterministic. The cost of that choice is that a cascade can be missed when the trigger is split across replicas, which the reconcile pass below repairs.

#### Merge rule per event type

| type | on replay |
|---|---|
| `created` | Idempotent by short id. The same id created on two replicas is a collision (see decision 1). |
| `edited` | Per field, the later `(ts, rep)` wins. Payload keeps old and new values so the scrubber can rewind. |
| `done`, `canceled`, `reopened` | Status is whatever the latest transition says. A repeat is a no-op. |
| `purged` | A tombstone. Later events for that id apply to nothing; children are purged by reconcile. |
| `claimed` | Carries `until`. Two live claims on one task: the earlier wins, reconcile releases the later with reason `lost-merge` and the loser's machine is told. |
| `released`, `claim_expired`, `heartbeat` | Latest wins. `heartbeat` is replicated: it is a fact about shared state, and it is rare. |
| `blocked`, `unblocked`, `labeled`, `unlabeled` | Set membership; the latest event for the pair wins. |
| `moved`, `reparented` | Carry parent and sort key; latest wins. |
| `noted` | Append-only; never conflicts. |
| `criteria_added` | Idempotent by criterion short id. |
| `criterion_state`, `found_in_set`, `found_in_cleared`, `kind_changed` | Latest wins. |
| `snapshot` | Full state. Applied as an overwrite at its position in the order; see adoption. |

Local, never in the shared log: `focus_*` and config. Focus is per-session workflow state and the default identity is per-machine; both move to `local.json`. Beat keeping them as events flagged local: two kinds of event in one stream invites the wrong one to leak.

### Rebuild, and when it runs

The cache records a watermark: for each log file, the byte offset it has applied. On every open:

- Every file's size equals its watermark and no new files exist: nothing to do. This is the hot path and costs a `stat` per file.
- Otherwise: rebuild. Drop every table, sort the union of every file by `(ts, rep, seq)`, apply in order, set the watermarks. Own writes go through the same watermark, so a crash between append and apply is healed by the next open.

There is no `job sync`. Sync is `git pull`, and the next `job` command notices. `job rebuild` forces a full rebuild and is the recovery verb.

**Reconcile.** After a rebuild that ingested events from another replica, evaluate the invariants a single replica would have kept and append the events that restore them, attributed to this replica: a parent whose last child closed on another machine, a purged root whose child was added elsewhere, two live claims on one task. The appended events are ordinary log entries and propagate like any other. This is the one place a read can write, and it prints what it did.

**Locking.** One `flock` on `.jobs.db.lock`, beside the cache so the existing `.jobs.db*` ignore pattern covers it, around append plus apply. Concurrent `job` processes on one machine (parallel agents) serialize there; SQLite's own locking covers the rest.

### Short ids under independent minting

Today `generateShortID` mints five random base62 characters and checks uniqueness against the local table. Two replicas can mint the same id while apart. The odds are small but not zero, and a collision is expensive: both replicas have already used the id in notes and commit messages, so no automatic remap is safe.

| tasks in the repo | chance of at least one cross-replica collision |
|---|---|
| 1,000 | 0.05% |
| 5,000 | 1.4% |
| 10,000 | 5% |

Detection is mandatory either way: two `created` events for one id from different replicas fail the rebuild with a message naming both replicas and both titles. The message names the way out, because the hygiene rule says never hand-edit a log file: `job rekey <rep>:<id>` appends a `rekeyed` event, attributed to this replica, that gives the later replica's task a fresh id. It reads the raw log rather than the cache, since the cache is what refused to build. From that position in the order every event from that replica for the old id applies to the new one, so every machine that pulls the log converges without a second decision. Notes and commit messages that cite the old id keep pointing at the earlier task, which is the one that kept the id; the `rekeyed` event and the notice name both so a reader can tell.

**Criteria are the larger hazard.** A criterion short id is three characters and is checked for uniqueness across the whole table, a space of 238,328 ids that this repo already fills with 344 rows: at 500 criteria the cross-replica odds pass 40%. No lookup needs that: every criterion is resolved by task and id, and every event that references one carries its task. Criterion ids therefore become unique per task and stay three characters. See decision 1.

### Adoption of a legacy database

Runs automatically the first time `job` opens a `.jobs.db` that has no `.jobs/` beside it, because every `job` invocation already migrates the schema silently; a conversion that needs a verb is not seamless. It prints what it did.

1. Mint a replica id and `local.json`. Move the default identity and strict flag out of `config`.
2. **Translate every row of the legacy `events` table into the log**, in id order, with `ts = created_at * 1000` and `task` resolved to a short id (deleted tasks included; purged tasks are already `NULL`). These carry `legacy: true`, and apply treats them as history only: they are inserted into the cache's `events` table so `log`, `show`, `tail` and the scrubber see them unchanged, but they do not touch the state tables. Legacy payloads were never replayable, and this is what makes that not matter.
3. **Write one `snapshot` event** carrying the full current state: every task with status, claim, kind, timestamps and sort key, every block, label, criterion and provenance row. Apply overwrites the state tables from it.
4. Rebuild the cache from the new log into a fresh file, dump both databases table by table, and compare. Any difference aborts adoption, leaves the legacy file untouched, and reports the diff. On success the legacy file becomes `.jobs.db.pre-adopt` and the rebuilt cache takes its place.

The snapshot event is also the future compaction primitive: `job compact` would write one and archive the files it summarizes. Parked in the backlog until a log is big enough to want it.

### `job merge`: two databases that diverged

For the repo whose `.jobs.db` was copied and then written on both machines, and as the recovery path when a log is lost and only caches remain.

```
job merge <other.jobs.db> [--dry-run]
```

The two files share an identical event prefix up to the copy. Merge works on state, with events as evidence:

- Find the common prefix by walking both `events` tables until a row differs on `(short_id, type, actor, created_at, detail)`. Everything after it on either side is that side's tail.
- **Tasks only on one side** are copied over whole, with their labels, blocks, criteria, provenance and events.
- **Tasks on both sides** merge per table: the task row from the side with the later `updated_at`, labels and blocks as a union, criteria by short id with the later `updated_at` winning, notes and events as a union deduplicated on the tuple above.
- Claims: a live claim on either side survives unless the other side closed the task.
- The report lists every task that existed on one side only, every task both sides touched and which side won each field, and every claim it dropped. `--dry-run` prints the report and writes nothing.
- The result is written into the local database, and the next open adopts it into a store as above.

A first-class verb rather than a script: it is the recovery tool, it needs the same table-dump comparison adoption uses, and it deserves tests.

### Cursors: `tail`, the SSE feed and the scrubber

`tail` and the dashboard's live feed poll for events after the last seen row id, and the scrubber addresses history as `?at=<row id>`. A rebuild renumbers rows: a foreign event that sorts earlier shifts every later id by one. Both move to the log position `(ts, rep, seq)` as their cursor; the scrubber's URL carries it encoded. Row ids stay internal.

### Agents and worktrees

Nothing changes for the current workflow: a subagent passing an absolute `--db` at the main checkout still writes to the main checkout's store, the integrator's `job tail` still sees it live, and `.jobs.db` still does not exist in a worktree. What becomes *possible* is an agent working entirely inside its worktree, where `.jobs/log/` is checked out, minting its own replica, and having its task events ride the squash-merge back to `main`. That trades live visibility for isolation; the rules in `project/agents/jobs.md` keep recommending the absolute `--db` until there is a reason to switch.

### What gets committed

`.jobs/log/*.jsonl`, by the human, whenever. Jobs never runs git. A commit that closes a task and the commit that did the work can be the same commit, or not; the log does not care. The one hygiene rule: don't hand-edit a log file. `job rebuild` will reject a line that fails to parse and name it.

## Rollout

The order is chosen so that every step leaves `main` shippable and the record is never in two places at once.

1. `job merge` first. It works on legacy databases and unblocks the copied repo today.
2. The pure library: envelope, clock, codec, ordering. No SQLite.
3. Typed payloads and short-id references, then fractional sort keys, as ordinary migrations while the tables are still the record.
4. The apply function, one verb family at a time, with the determinism test running from the first family: shuffle arrival order, rebuild, compare dumps.
5. The store, adoption, and the cursor change. From here the log is the record.
6. Two-replica end-to-end test, docs, README.

Risks worth naming: the apply refactor touches nearly every file in `internal/job`, so it is one writer, serialized; the determinism test is the thing that catches an apply that reads the clock or the row id; and adoption's dump comparison is what stops a translation bug from silently changing a user's history.

## Decisions

1. **Short id width.** Options: keep five characters and rely on detection, or mint six for new tasks. Six brings the 5,000-task collision odds from 1.4% to 0.02%, at the cost of mixed widths in every listing during the transition. **Ruled 2026-09-01: tasks mint six characters; criterion ids stay three and become unique per task rather than per table.** Existing five-character ids are untouched. One guard did check width, `looksLikeShortID` in `cmd/job/commands.go`, which `done` uses to tell an id from prose; it now accepts five or six. The criterion index moved to `(task_id, short_id)` in migration 0008. Criteria never need global addressability, since every reference already carries the task; if that ever changes the address is task id plus criterion id, which the per-task scope already supports.
2. **Local state lives in `local.json`, not the cache.** The cache is disposable, so a default identity stored in it would vanish with `rm .jobs.db`.
3. **Adoption is automatic, not a verb.** Consistent with "any invocation migrates"; it prints a notice and keeps a backup.
4. **Apply never derives; reconcile repairs.** Beat deriving cascades inside apply: that hides state changes from the log, and the log is the record.

## Plan

```yaml
tasks:
  - title: Git-native event log
    desc: |
      Make `.jobs/log/*.jsonl` the record and `.jobs.db` a disposable cache rebuilt from it, so a project's tasks travel through git across machines with no cloud, no CRDT and no merge conflicts. The vision, the design, every merge rule and the alternatives each decision beat are in project/2026-09-01-git-native-event-log.md — read it before starting any leaf. The apply refactor touches nearly every file in internal/job; those leaves are one writer, serialized.
    labels: [store, sync]
    children:
      - title: Decide short id width under independent minting
        ref: width
        labels: [decision, store]
        desc: |
          Two replicas can mint the same five-character short id while apart, and a collision cannot be remapped safely after the fact. The doc's table gives the odds at 1k, 5k and 10k tasks. Criterion ids are three characters checked for uniqueness across the whole table, which is the larger hazard. Ruled: tasks mint six, criteria stay three and become unique per task. Close this leaf with the ruling in the note; the store leaf reads it.
        criteria:
          - The ruling and its reason are recorded in the closing note
      - title: Add job merge for two divergent .jobs.db copies
        ref: merge
        labels: [cli, store, recovery]
        desc: |
          `job merge <other.jobs.db> [--dry-run]` in internal/job/merge.go and cmd/job/merge.go. Find the common event prefix, copy tasks present only on one side whole, merge tasks present on both per the doc's table (later updated_at wins the row, labels and blocks union, criteria by short id, events deduplicated on the tuple), keep live claims unless the other side closed the task. Print the report described in the doc; --dry-run writes nothing. Tests build two databases from one seed, diverge them, merge, and assert every rule; a second merge of the same pair is a no-op. Reference entry in the docs.
        criteria:
          - A task created only in the other database arrives with its labels, blocks, criteria, provenance and events
          - A task edited on both sides keeps the later edit and the union of labels and blocks
          - Notes from both sides survive and none is duplicated
          - --dry-run prints the report and leaves both files byte-identical
          - Merging the same pair twice changes nothing
      - title: Event envelope, hybrid clock, JSONL codec and ordering
        ref: codec
        labels: [store, library]
        desc: |
          A new package with no SQLite dependency: the Envelope struct (v, rep, seq, ts, actor, type, task, data), the hybrid logical clock (max of wall and last_seen+1, persisted last_seen), replica id minting, a per-replica appender that takes the store lock and assigns gapless seq, a reader that parses every file under .jobs/log and returns the union sorted by (ts, rep, seq), and a parse error that names file and line. Tests: round-trip every field, ordering is total and stable under shuffle, a truncated line is reported by file and line, two appenders under the lock never interleave or repeat a seq, the clock never goes backwards across a read of a future timestamp.
        criteria:
          - The union of shuffled files sorts identically to the unshuffled union
          - Concurrent appends under the lock produce gapless seq with no interleaved lines
          - A malformed line is reported with file and line number
          - Reading an event with a future ts advances the clock past it
      - title: Typed event payloads referencing tasks by short id
        ref: payloads
        labels: [store, refactor]
        desc: |
          One file of payload structs, one per event type, replacing every map[string]any at every recordEvent call site; every task or criterion reference inside a payload is a short id. The events table gains nothing yet — this is still the audit log — but the dashboard's live modules and the replay-snapshot script read the same shapes, so update the JS test that scrapes recordEvent sites. Add a test that marshals every payload type and asserts no field named task_id or criterion_id carries an integer.
        criteria:
          - No recordEvent call site passes a map
          - Every payload reference to a task or criterion is a short id
          - The existing suite and make test-js pass unchanged
      - title: Replace sort_order with fractional sort keys
        ref: sortkeys
        blockedBy: [payloads]
        labels: [store, refactor]
        desc: |
          A key generator that yields a string strictly between any two neighbours, a migration that derives keys from the existing integer order for tasks and criteria, and every writer — add --before, move, reparent, split, import, criteria add — computing a key at command time and carrying it in the event. Apply-side there is no shifting of siblings. Listing order must be byte-identical before and after the migration on this repo's own database; test that with a dump.
        criteria:
          - Listing order on a copy of this repo's database is identical before and after migration
          - Two keys generated for the same gap by different replicas are distinct and ordered by rep
          - No writer updates the sort key of any row other than the one it moves
      - title: Apply function for the task family
        ref: apply-tasks
        blockedBy: [codec, payloads, sortkeys]
        labels: [store, refactor]
        desc: |
          Introduce apply(tx, Envelope) as the only writer of the state tables and move created, edited, done, reopened, canceled, purged, moved, reparented and noted onto it: each handler validates against the cache, builds its events (cascade closes included, as explicit events), and calls append-then-apply. Task timestamps come from event ts. Land the determinism test here: build a log through the handlers, shuffle it, rebuild into a fresh cache, compare full dumps. Every existing test in this family keeps passing.
        criteria:
          - No handler in the family writes a state table directly
          - Rebuilding a shuffled log yields a dump identical to the original
          - created_at, updated_at and closed on tasks equal their events' ts
      - title: Apply function for claims
        ref: apply-claims
        blockedBy: [apply-tasks]
        labels: [store, refactor]
        desc: |
          Move claimed, released, claim_expired and heartbeat onto apply. claimed carries an absolute until. Expiry stays a read-time check but the claim_expired event it records goes through the same path. Extend the determinism test with claim sequences.
        criteria:
          - No claim handler writes a state table directly
          - The determinism test covers claim, heartbeat, expire and release
      - title: Apply function for relations, criteria, provenance and kind
        ref: apply-relations
        blockedBy: [apply-tasks]
        labels: [store, refactor]
        desc: |
          Move blocked, unblocked, labeled, unlabeled, criteria_added, criterion_state, found_in_set, found_in_cleared and kind_changed onto apply, and import onto the same path as a batch of events. Extend the determinism test to an imported plan with blocks and criteria.
        criteria:
          - No relation, criteria, provenance or kind handler writes a state table directly
          - An imported plan rebuilds identically from its events
      - title: Move focus and config to local.json
        ref: local
        labels: [store]
        desc: |
          Focus, default identity and strict mode leave the events and config tables for .jobs/local.json, alongside the replica id and clock. focus_* events stop being recorded; the dashboard and the SSE type list drop them. The identity verbs and job status read the file. Losing the cache must not lose the default identity — test that by deleting the cache and reading it back.
        criteria:
          - Deleting .jobs.db does not change the default identity, strict flag or focus
          - No focus event is recorded anywhere
      - title: The store — replica files, watermark, rebuild on open, reconcile
        ref: store
        blockedBy: [width, local, apply-claims, apply-relations]
        labels: [store, cli]
        desc: |
          Opening a database resolves the .jobs/ store beside it, mints local.json on first use, compares every log file's size to its watermark, and rebuilds when they differ. Handlers append to this replica's file before applying. Add job rebuild. Add the reconcile pass: after ingesting foreign events, close parents whose children all closed elsewhere, purge children of purged roots, release the later of two live claims with reason lost-merge, and print each repair. Detect two created events for one short id from different replicas and fail the rebuild naming both replicas and titles; add job rekey <rep>:<id>, which reads the raw log and appends a rekeyed event so every later event from that replica for the old id applies to a fresh id on every machine. The lock is .jobs.db.lock, beside the cache, so the .jobs.db* ignore pattern covers it. Apply the width ruling: tasks mint six characters, criterion ids are unique per task. job status gains a store line: replica id, log files, cache state. Tests: a crash simulated between append and apply heals on the next open; a foreign file appearing triggers a rebuild; each reconcile rule.
        criteria:
          - A fresh clone containing only .jobs/log builds a working cache on the first command
          - Appending to a file without applying is healed on the next open
          - A parent whose last child closed on another replica is closed by reconcile with an event in the log
          - Two live claims on one task leave the earlier one and log a release for the later
          - Two created events for one short id from different replicas fail the rebuild naming both
          - After job rekey the rebuild succeeds and both replicas' caches dump identically
          - A new task id is six characters and two tasks may share a criterion id
      - title: Adopt a legacy .jobs.db into a store
        ref: adopt
        blockedBy: [store]
        labels: [store, migration]
        desc: |
          On opening a .jobs.db with no .jobs/ beside it: translate the events table into the log as legacy history-only entries, write one snapshot event carrying the full state, rebuild into a fresh cache, dump and compare against the legacy tables, and on success keep .jobs.db.pre-adopt as a backup. Any difference aborts with the diff and leaves the legacy file untouched. Update job gitignore to .jobs.db* and .jobs/local.json. Test against a copy of this repo's own database and against a database that has purged tasks, criteria and provenance rows.
        criteria:
          - Adopting a copy of this repo's database yields a cache whose dump equals the legacy dump
          - log, show and the scrubber render legacy events unchanged after adoption
          - A translation that would change state aborts and leaves the legacy file untouched
          - job gitignore writes the two new patterns and no longer writes the old three
      - title: Key tail, the SSE feed and the scrubber on log position
        ref: cursors
        blockedBy: [store]
        labels: [store, web]
        desc: |
          tail, /events and the dashboard's ?at cursor stop using event row ids, which a rebuild renumbers, and use (ts, rep, seq) instead, encoded in the URL for the scrubber. The replay buffer in the JS and the replay-snapshot script take the same cursor. Test that a rebuild that inserts an earlier foreign event does not make tail replay or skip anything.
        criteria:
          - A rebuild that inserts an earlier foreign event neither repeats nor skips events in a running tail
          - The scrubber URL survives a rebuild and lands on the same event
          - make test-js passes
      - title: Two-replica end-to-end test
        blockedBy: [adopt, cursors]
        labels: [store, test]
        desc: |
          Two stores in two directories standing in for two clones. Write on both, copy log files across as git would, open both, and assert identical dumps; then every conflict case in the doc's table: concurrent edits, done versus note, claim versus claim, cascade split across replicas, purge versus add-child. Drive the built binary, not the library, for at least the happy path.
        criteria:
          - After exchanging log files both caches dump identically
          - Every row of the merge-rule table has a passing case
          - The happy path passes against the built binary
      - title: Docs, README and the agent rules for the store
        blockedBy: [merge, adopt, cursors]
        labels: [docs]
        desc: |
          A concepts page on the store: what is the record, what is the cache, what to commit, what a rebuild is, how two machines cooperate. Getting started shows clone-then-status working. Reference entries for merge and rebuild and the new gitignore patterns. README gains the two-line version, since new users must know what to commit. project/agents/jobs.md is a generated region — flag the --db and worktree paragraph for the shared rules rather than editing it. Run scripts/verify-getting-started.sh against the rebuilt binary.
        criteria:
          - A concepts page explains record versus cache and what to commit
          - merge and rebuild are in the command reference
          - README says what to commit
          - scripts/verify-getting-started.sh passes against the rebuilt binary
```
