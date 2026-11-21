-- +goose Up
-- Prompts that come with an upload, from an importer that knows what an AI item was asked for when
-- its file doesn't say (a ChatGPT data export: its pictures carry Content Credentials but no
-- prompt). Kept per item, and shown as its prompt only when the item's own evidence shows it's AI
-- and gives none, so reading the file again with a better parser keeps it.
ALTER TABLE uploads ADD COLUMN prompt text NOT NULL DEFAULT '';

CREATE TABLE given_prompts (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    text       text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE given_prompts;
ALTER TABLE uploads DROP COLUMN prompt;
