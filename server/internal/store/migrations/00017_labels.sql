-- +goose Up
-- Utility collections: screenshots, documents, receipts, ID cards and QR codes, recognized from
-- each item's search embedding, the text read from it and its file name, and corrected by hand.
-- Corrections (confirmed, rejected) outlive every re-run; automatic labels (metadata, visual) are
-- recomputed when the rules change.
CREATE TABLE asset_labels (
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    label      text NOT NULL CHECK (label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code')),
    basis      text NOT NULL CHECK (basis IN ('metadata', 'visual', 'confirmed', 'rejected')),
    score      real,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, label)
);
CREATE INDEX asset_labels_shown ON asset_labels (label, asset_id) WHERE basis <> 'rejected';

-- Which version of the rules last looked at an item, and whether its text had been read by then
-- (when it hadn't, the item is looked at again once it has).
CREATE TABLE labels_checked (
    asset_id uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    version  smallint NOT NULL,
    had_text boolean NOT NULL
);

-- "Captured by me": the few items with a camera's details, found without reading every row.
CREATE INDEX asset_metadata_camera ON asset_metadata (asset_id) WHERE make IS NOT NULL OR model IS NOT NULL;

-- +goose Down
DROP INDEX asset_metadata_camera;
DROP TABLE labels_checked;
DROP TABLE asset_labels;
