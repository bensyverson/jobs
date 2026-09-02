-- The events table becomes the cache's copy of the log's lines.
--
-- The record is .jobs/log/*.jsonl, one file per replica, and every line is an
-- envelope: (rep, seq) identifies it globally, ts is a hybrid logical clock in
-- milliseconds, and the global order is (ts, rep, seq)
-- (project/2026-09-01-git-native-event-log.md, "The event"). The cache holds
-- the same fields so `log`, `tail`, the scrubber and a rebuild all address
-- events by position rather than by a row id a rebuild renumbers.
--
-- created_at stays, in seconds, because every reader already uses it; new rows
-- write it as ts/1000.
--
-- Rows written before the store existed keep rep '' and seq 0. That is the
-- legacy marker: those payloads were never replayable, so apply is never
-- called on them and they are history only. Their ts is derived from
-- created_at so they still sort into the timeline.

ALTER TABLE events ADD COLUMN rep TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN ts INTEGER NOT NULL DEFAULT 0;

UPDATE events SET ts = created_at * 1000 WHERE ts = 0;

CREATE INDEX IF NOT EXISTS idx_events_position ON events(ts, rep, seq);
