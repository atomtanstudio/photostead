-- Removing a file checks which items point at it (assets.primary_file_id ON DELETE SET NULL).
-- Without this index that check scans every item, once per file removed.

-- +goose Up
CREATE INDEX assets_primary_file ON assets (primary_file_id);

-- +goose Down
DROP INDEX assets_primary_file;
