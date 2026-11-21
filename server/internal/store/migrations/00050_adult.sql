-- +goose Up
-- Adult content: a worker's model (Freepik's EVA-based NSFW classifier) rates each picture neutral,
-- suggestive, nudity or explicit; a video by the most adult of several frames. Nudity and explicit
-- make an item "adult", suggestive its own label, both kept in asset_labels beside the utility labels
-- (basis 'visual'), so the owner's corrections (confirmed, rejected) work the same way and outlive
-- every re-scan.
ALTER TABLE asset_labels DROP CONSTRAINT asset_labels_label_check;
ALTER TABLE asset_labels ADD CONSTRAINT asset_labels_label_check
    CHECK (label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code', 'adult', 'suggestive'));
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces', 'pets', 'adult'));

-- What the model saw: its four probabilities (neutral, suggestive, nudity, explicit) for the most
-- adult frame looked at, and how many frames that was (1 for a photo). An item is scanned again when
-- its picture changes (the row is dropped) or the engine changes.
CREATE TABLE adult_scans (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    engine     text NOT NULL,
    scores     real[] NOT NULL CHECK (cardinality(scores) = 4),
    frames     smallint NOT NULL,
    scanned_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE adult_scans;
DELETE FROM worker_tasks WHERE kind = 'adult';
ALTER TABLE worker_tasks DROP CONSTRAINT worker_tasks_kind_check;
ALTER TABLE worker_tasks ADD CONSTRAINT worker_tasks_kind_check CHECK (kind IN ('embed_image', 'ocr', 'faces', 'pets'));
DELETE FROM asset_labels WHERE label IN ('adult', 'suggestive');
ALTER TABLE asset_labels DROP CONSTRAINT asset_labels_label_check;
ALTER TABLE asset_labels ADD CONSTRAINT asset_labels_label_check
    CHECK (label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code'));
