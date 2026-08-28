-- found_in records where a task was surfaced without parenting it there and
-- without creating any blocking relationship. One source per task: work is
-- found in one place. The row outlives every status change of the source
-- (done, canceled, canceled by cascade) and its soft delete, because nothing
-- in the close or cancel paths touches this table. ON DELETE CASCADE only
-- fires for `cancel --purge`, which erases the task row outright — a
-- reference to an erased task is not provenance worth keeping.
CREATE TABLE IF NOT EXISTS found_in (
    task_id    INTEGER PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    source_id  INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s','now')),
    CHECK (task_id != source_id)
);
CREATE INDEX IF NOT EXISTS idx_found_in_source_id ON found_in(source_id);
