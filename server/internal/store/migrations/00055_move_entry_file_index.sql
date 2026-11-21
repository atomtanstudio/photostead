-- +goose Up
-- A file's row going (a library removed, say) sets move entries that point at it to NULL; without an
-- index each one read every entry of every plan.
CREATE INDEX move_plan_entries_new_file ON move_plan_entries (new_file_id) WHERE new_file_id IS NOT NULL;

-- +goose Down
DROP INDEX move_plan_entries_new_file;
