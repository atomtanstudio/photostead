-- +goose Up
-- The folder watcher. Indexed libraries are watched; the managed share isn't, because
-- Photostead writes it itself and records what it writes.
UPDATE libraries SET watch = true WHERE kind = 'indexed';
-- When the watcher last confirmed it was watching a library. After a restart, folders changed
-- since then are scanned, so nothing added while the server was down waits for the nightly
-- rescan.
ALTER TABLE libraries ADD COLUMN watched_at timestamptz;

-- +goose Down
ALTER TABLE libraries DROP COLUMN watched_at;
UPDATE libraries SET watch = false;
