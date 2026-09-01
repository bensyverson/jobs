-- Criterion short ids are unique per task, not per table. Every lookup
-- and every event already carries the task, so a table-wide scope bought
-- nothing and, at three characters, was the larger cross-replica
-- collision hazard once two machines mint apart
-- (project/2026-09-01-git-native-event-log.md, decision 1).
DROP INDEX IF EXISTS idx_task_criteria_short_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_task_criteria_task_short_id
    ON task_criteria(task_id, short_id) WHERE short_id IS NOT NULL;
