-- +goose Up
-- The sync feed's change log: which items and albums changed, so a phone that has the library
-- catches up with just those. Triggers write it; it's kept 30 days. Each row has the transaction
-- that wrote it: a reader carries on from the oldest transaction still running when it last read
-- (pg_snapshot_xmin), so a change that commits after a read is never skipped. Some come twice;
-- the phone just applies them again.
CREATE TABLE sync_changes (
    seq  bigserial PRIMARY KEY,
    txid xid8 NOT NULL DEFAULT pg_current_xact_id(),
    kind text NOT NULL CHECK (kind IN ('asset', 'album')),
    id   uuid NOT NULL,
    at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sync_changes_txid ON sync_changes (txid);
CREATE INDEX sync_changes_at ON sync_changes (at);

-- +goose StatementBegin
CREATE FUNCTION sync_note_asset() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sync_changes (kind, id) VALUES ('asset', CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END);
    RETURN NULL;
END $$;

-- A row about an item (its stars, a space, a stack): that item changed.
CREATE FUNCTION sync_note_asset_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sync_changes (kind, id) VALUES ('asset', CASE WHEN TG_OP = 'DELETE' THEN OLD.asset_id ELSE NEW.asset_id END);
    RETURN NULL;
END $$;

CREATE FUNCTION sync_note_album() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sync_changes (kind, id) VALUES ('album', CASE WHEN TG_OP = 'DELETE' THEN OLD.id ELSE NEW.id END);
    RETURN NULL;
END $$;

-- In or out of an album: the album changed, and who may see the item may have.
CREATE FUNCTION sync_note_album_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r record;
BEGIN
    r := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    INSERT INTO sync_changes (kind, id) VALUES ('album', r.album_id), ('asset', r.asset_id);
    RETURN NULL;
END $$;

-- Shared with someone, or not: the album, and every item in it, for them.
CREATE FUNCTION sync_note_album_member() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE album uuid;
BEGIN
    album := CASE WHEN TG_OP = 'DELETE' THEN OLD.album_id ELSE NEW.album_id END;
    INSERT INTO sync_changes (kind, id) VALUES ('album', album);
    INSERT INTO sync_changes (kind, id) SELECT 'asset', asset_id FROM album_assets WHERE album_id = album;
    RETURN NULL;
END $$;

-- A stack confirmed, undone or given another cover: its items.
CREATE FUNCTION sync_note_stack() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sync_changes (kind, id) SELECT 'asset', asset_id FROM stack_assets WHERE stack_id = NEW.id;
    RETURN NULL;
END $$;

-- Joining or leaving the household: everything shared with it.
CREATE FUNCTION sync_note_household_member() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE household uuid;
BEGIN
    household := CASE WHEN TG_OP = 'DELETE' THEN OLD.household_id ELSE NEW.household_id END;
    INSERT INTO sync_changes (kind, id)
    SELECT 'asset', sa.asset_id FROM spaces sp JOIN space_assets sa ON sa.space_id = sp.id WHERE sp.household_id = household;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER assets_sync AFTER INSERT OR DELETE ON assets FOR EACH ROW EXECUTE FUNCTION sync_note_asset();
CREATE TRIGGER assets_sync_change AFTER UPDATE ON assets FOR EACH ROW
WHEN ((OLD.type, OLD.taken_at, OLD.taken_offset_min, OLD.local_date, OLD.width, OLD.height, OLD.duration_ms, OLD.thumbhash,
       OLD.render_state, OLD.visibility, OLD.trashed_at, OLD.purged_at, OLD.live_motion_id, OLD.motion_of, OLD.source_type,
       OLD.owner_id, OLD.sha256, OLD.mime)
      IS DISTINCT FROM
      (NEW.type, NEW.taken_at, NEW.taken_offset_min, NEW.local_date, NEW.width, NEW.height, NEW.duration_ms, NEW.thumbhash,
       NEW.render_state, NEW.visibility, NEW.trashed_at, NEW.purged_at, NEW.live_motion_id, NEW.motion_of, NEW.source_type,
       NEW.owner_id, NEW.sha256, NEW.mime))
EXECUTE FUNCTION sync_note_asset();
CREATE TRIGGER asset_user_state_sync AFTER INSERT OR UPDATE OR DELETE ON asset_user_state FOR EACH ROW EXECUTE FUNCTION sync_note_asset_id();
CREATE TRIGGER space_assets_sync AFTER INSERT OR DELETE ON space_assets FOR EACH ROW EXECUTE FUNCTION sync_note_asset_id();
CREATE TRIGGER stack_assets_sync AFTER INSERT OR DELETE ON stack_assets FOR EACH ROW EXECUTE FUNCTION sync_note_asset_id();
CREATE TRIGGER stacks_sync AFTER UPDATE ON stacks FOR EACH ROW
WHEN ((OLD.state, OLD.cover_asset_id) IS DISTINCT FROM (NEW.state, NEW.cover_asset_id)) EXECUTE FUNCTION sync_note_stack();
CREATE TRIGGER albums_sync AFTER INSERT OR UPDATE OR DELETE ON albums FOR EACH ROW EXECUTE FUNCTION sync_note_album();
CREATE TRIGGER album_assets_sync AFTER INSERT OR DELETE ON album_assets FOR EACH ROW EXECUTE FUNCTION sync_note_album_asset();
CREATE TRIGGER album_members_sync AFTER INSERT OR UPDATE OR DELETE ON album_members FOR EACH ROW EXECUTE FUNCTION sync_note_album_member();
CREATE TRIGGER household_members_sync AFTER INSERT OR DELETE ON household_members FOR EACH ROW EXECUTE FUNCTION sync_note_household_member();

-- +goose Down
DROP TRIGGER household_members_sync ON household_members;
DROP TRIGGER album_members_sync ON album_members;
DROP TRIGGER album_assets_sync ON album_assets;
DROP TRIGGER albums_sync ON albums;
DROP TRIGGER stacks_sync ON stacks;
DROP TRIGGER stack_assets_sync ON stack_assets;
DROP TRIGGER space_assets_sync ON space_assets;
DROP TRIGGER asset_user_state_sync ON asset_user_state;
DROP TRIGGER assets_sync_change ON assets;
DROP TRIGGER assets_sync ON assets;
DROP FUNCTION sync_note_household_member(), sync_note_stack(), sync_note_album_member(), sync_note_album_asset(), sync_note_album(), sync_note_asset_id(), sync_note_asset();
DROP TABLE sync_changes;
