-- Tree kind. Only a root (parent_id IS NULL) carries a meaningful kind; the
-- root-only invariant is enforced in Go, because SQLite cannot express
-- "parent_id IS NULL OR kind = 'task'" as an ALTER TABLE column CHECK.
-- NOT NULL is what makes the CHECK bite: a CHECK passes on NULL.
ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'task'
    CHECK (kind IN ('task', 'issue'));
