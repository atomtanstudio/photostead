-- +goose Up
-- What came from where on a person's phones: an item in an iPhone's Photos library (its local
-- identifier, and which of its files: "…/L0/001|photo") and the file it was. A phone asks which of
-- its items Photostead has before sending them, so that iOS's own background uploads, which can't
-- hash a file first, don't send what's here already. Kept when an item is deleted, as the upload
-- check counts deleted files: they aren't sent again.
CREATE TABLE device_items (
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    item       text NOT NULL,
    sha256     bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, item, sha256)
);

-- +goose Down
DROP TABLE device_items;
