-- The cache's watermark into each replica's log file.
--
-- The record is .jobs/log/*.jsonl; .jobs.db is a disposable cache of it
-- (project/2026-09-01-git-native-event-log.md, "Rebuild, and when it runs").
-- For each log file this table holds the byte offset the cache has applied.
-- On every open, a stat per file compares size to offset: equal everywhere and
-- no unknown file means there is nothing to do, which is the hot path.
--
-- A write appends to the file and advances this offset inside the same
-- transaction that applies the events, so a crash between the two leaves the
-- file longer than the watermark and the next open rebuilds.
--
-- `offset` is bare rather than a rowid alias on purpose: a replica's log file
-- is addressed by its replica id, and there is exactly one row per replica.

CREATE TABLE IF NOT EXISTS log_watermarks (
    rep    TEXT PRIMARY KEY,
    offset INTEGER NOT NULL
);
