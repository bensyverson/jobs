-- Fractional sort keys replace integer sort_order on tasks and criteria.
--
-- Two machines inserting under one parent both shift the same integer rows,
-- so ordering cannot merge. A sort key is a string chosen so a new key can
-- always be generated strictly between any two neighbours, which makes
-- placing a row a plain column write that touches no sibling
-- (project/2026-09-01-git-native-event-log.md, "Ordering within a parent").
--
-- This migration only adds the columns. Deriving each row's key from its old
-- sort_order needs the key generator, so backfillSortKeys in
-- internal/job/database.go runs immediately after the migrator and drops the
-- dead sort_order columns once the keys are in place.

ALTER TABLE tasks ADD COLUMN sort_key TEXT NOT NULL DEFAULT '';
ALTER TABLE task_criteria ADD COLUMN sort_key TEXT NOT NULL DEFAULT '';
