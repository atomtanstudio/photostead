-- Folders inside a library that Photostead leaves alone: never scanned, never indexed, never moved.
-- Each entry is a folder path relative to the library root, e.g. 'lora-datasets'.

-- +goose Up
ALTER TABLE libraries ADD COLUMN exclude text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE libraries DROP COLUMN exclude;
