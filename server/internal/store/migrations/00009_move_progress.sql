-- Carrying out a reviewed move plan: each planned copy records its outcome, so a run can stop
-- and resume at any point.

-- +goose Up
ALTER TABLE move_plan_entries
    ADD COLUMN state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'done', 'failed')),
    ADD COLUMN error text,
    ADD COLUMN done_at timestamptz,
    ADD COLUMN new_file_id uuid REFERENCES files ON DELETE SET NULL;
CREATE INDEX move_plan_entries_pending ON move_plan_entries (plan_id) WHERE state = 'pending';

-- +goose Down
DROP INDEX move_plan_entries_pending;
ALTER TABLE move_plan_entries DROP COLUMN new_file_id, DROP COLUMN done_at, DROP COLUMN error, DROP COLUMN state;
