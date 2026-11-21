-- +goose Up
-- The integrity scrub: files read again now and then and checked against the hash taken when they
-- came in. damaged_at: when one that looks the same (size and time) no longer matches.
ALTER TABLE files ADD COLUMN damaged_at timestamptz;
CREATE INDEX files_damaged ON files (damaged_at) WHERE damaged_at IS NOT NULL;
CREATE INDEX files_verify ON files (last_verified_at NULLS FIRST, id) WHERE state = 'present' AND sha256 IS NOT NULL;

-- +goose Down
DROP INDEX files_verify;
DROP INDEX files_damaged;
ALTER TABLE files DROP COLUMN damaged_at;
