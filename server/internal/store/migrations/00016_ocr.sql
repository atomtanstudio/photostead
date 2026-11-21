-- +goose Up
-- Text in images (OCR), read by workers, searchable alongside meaning, prompts and names.
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr'));

CREATE TABLE asset_ocr (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    engine     text NOT NULL,
    text       text NOT NULL, -- '' when no text was read with confidence
    created_at timestamptz NOT NULL DEFAULT now()
);
-- 'simple': no stemming, so any language and any made-up word (a brand, a handle) matches as typed.
CREATE INDEX asset_ocr_words ON asset_ocr USING gin (to_tsvector('simple', text)) WHERE text <> '';

-- +goose Down
DROP TABLE asset_ocr;
DELETE FROM worker_tasks WHERE kind = 'ocr';
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image'));
