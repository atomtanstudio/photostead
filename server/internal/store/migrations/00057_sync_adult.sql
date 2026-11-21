-- +goose Up
-- The sync feed says which items are adult (so a phone blurs or hides them by its person's
-- setting): an item found adult, or no longer, has changed.
-- +goose StatementBegin
CREATE FUNCTION sync_note_adult() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.label = 'adult' THEN
            INSERT INTO sync_changes (kind, id) VALUES ('asset', OLD.asset_id);
        END IF;
    ELSIF NEW.label = 'adult' AND (TG_OP = 'INSERT' OR OLD.basis IS DISTINCT FROM NEW.basis) THEN
        INSERT INTO sync_changes (kind, id) VALUES ('asset', NEW.asset_id);
    END IF;
    RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER asset_labels_sync AFTER INSERT OR UPDATE OR DELETE ON asset_labels FOR EACH ROW EXECUTE FUNCTION sync_note_adult();

-- +goose Down
DROP TRIGGER asset_labels_sync ON asset_labels;
DROP FUNCTION sync_note_adult();
