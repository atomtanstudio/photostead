-- +goose Up
-- Trash. Trashed items wait 30 days (or until the trash is emptied); then Photostead deletes its
-- own copies of them (files in writable, managed libraries) and their renders. The item itself
-- stays as a record (purged_at), so a copy in a read-only folder Photostead indexes never brings it
-- back.
ALTER TABLE assets ADD COLUMN purged_at timestamptz;
CREATE INDEX assets_trash ON assets (owner_id, trashed_at) WHERE trashed_at IS NOT NULL AND purged_at IS NULL;

ALTER TABLE files DROP CONSTRAINT files_state_check;
ALTER TABLE files ADD CONSTRAINT files_state_check CHECK (state IN ('pending', 'present', 'missing', 'skipped', 'failed', 'deleted'));

-- The trash, as its owner sees it: the gate's counterpart for trashed items not yet deleted.
-- +goose StatementBegin
CREATE FUNCTION trashed_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NOT NULL AND a.purged_at IS NULL
      AND a.visibility <> 'vault'
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION trashed_assets(uuid);
UPDATE files SET state = 'missing' WHERE state = 'deleted';
ALTER TABLE files DROP CONSTRAINT files_state_check;
ALTER TABLE files ADD CONSTRAINT files_state_check CHECK (state IN ('pending', 'present', 'missing', 'skipped', 'failed'));
DROP INDEX assets_trash;
ALTER TABLE assets DROP COLUMN purged_at;
