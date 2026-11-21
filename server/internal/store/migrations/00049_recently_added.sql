-- +goose Up
-- Recently added: the library newest-arrived first (when each item came into Photostead, by upload,
-- capture, import or a scan finding it), paged by (created_at, id).
CREATE INDEX assets_added ON assets (owner_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX assets_added;
