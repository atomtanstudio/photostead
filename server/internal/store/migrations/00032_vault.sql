-- +goose Up
-- The vault. Items moved there (visibility 'vault') are already left out of everything by the gate
-- and the trash; now their owner can see them, in a session unlocked with the vault's PIN. Local
-- analysis (search embeddings, faces) still runs on them, but nothing shows them elsewhere.
ALTER TABLE users ADD COLUMN vault_pin text,                         -- argon2id, like a password
                  ADD COLUMN vault_failures smallint NOT NULL DEFAULT 0, -- wrong PINs in a row
                  ADD COLUMN vault_blocked_until timestamptz;        -- too many: wait until then
ALTER TABLE sessions ADD COLUMN vault_until timestamptz;             -- unlocked until then (it slides with use)

-- The vault, as its owner sees it once it's unlocked: the API passes the viewer only for a session
-- that unlocked it (anyone else gets nothing). Items in the vault's trash too, until deleted.
-- +goose StatementBegin
CREATE FUNCTION vault_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer AND a.visibility = 'vault' AND a.purged_at IS NULL
$$;
-- +goose StatementEnd
CREATE INDEX assets_vault ON assets (owner_id, local_date) WHERE visibility = 'vault';

-- +goose Down
DROP INDEX assets_vault;
DROP FUNCTION vault_assets(uuid);
ALTER TABLE sessions DROP COLUMN vault_until;
ALTER TABLE users DROP COLUMN vault_blocked_until, DROP COLUMN vault_failures, DROP COLUMN vault_pin;
