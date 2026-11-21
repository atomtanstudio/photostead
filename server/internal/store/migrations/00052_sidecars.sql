-- +goose Up
-- XMP sidecars (decision 1.4): what people add in Photostead is written beside the original as
-- <file name>.xmp, so other apps (Lightroom, digiKam, darktable, Bridge) and a future without
-- Photostead keep it. Originals are never written to. A read-only library's sidecars go to the data
-- folder instead, mirroring its paths (libraries.sidecar_mode 'shadow'); 'off' writes none.
--
-- Written: the owner's stars, pick or reject, color label and favorite; keywords; the description;
-- a date the owner set; the people named in it; how an AI item was made; a photo's edit. Triggers
-- queue an item when any of those change (a change while it's being written queues it again); a job
-- writes the queue every few minutes, skipping an item whose sidecar would come out the same as last time.
CREATE TABLE sidecar_queue (
    asset_id  uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    queued_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sidecar_writes (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    path       text NOT NULL,
    hash       bytea NOT NULL, -- of what was written, to skip writing the same again
    written_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION queue_sidecar() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        INSERT INTO sidecar_queue (asset_id) SELECT OLD.asset_id WHERE EXISTS (SELECT 1 FROM assets WHERE id = OLD.asset_id)
        ON CONFLICT (asset_id) DO UPDATE SET queued_at = now();
        RETURN OLD;
    END IF;
    INSERT INTO sidecar_queue (asset_id) VALUES (NEW.asset_id) ON CONFLICT (asset_id) DO UPDATE SET queued_at = now();
    RETURN NEW;
END $$;

CREATE FUNCTION queue_sidecar_asset() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sidecar_queue (asset_id) VALUES (NEW.id) ON CONFLICT (asset_id) DO UPDATE SET queued_at = now();
    RETURN NEW;
END $$;

CREATE FUNCTION queue_sidecar_person() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO sidecar_queue (asset_id) SELECT DISTINCT f.asset_id FROM faces f WHERE f.person_id = NEW.id
    ON CONFLICT (asset_id) DO UPDATE SET queued_at = now();
    RETURN NEW;
END $$;
-- +goose StatementEnd

CREATE TRIGGER sidecar_marks AFTER INSERT OR UPDATE OR DELETE ON asset_user_state FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_keywords AFTER INSERT OR DELETE ON asset_keywords FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_descriptions AFTER INSERT OR UPDATE OR DELETE ON asset_descriptions FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_edits AFTER INSERT OR UPDATE OR DELETE ON asset_edits FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_given_prompts AFTER INSERT OR UPDATE OR DELETE ON given_prompts FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_faces AFTER INSERT OR DELETE OR UPDATE OF person_id ON faces FOR EACH ROW EXECUTE FUNCTION queue_sidecar();
CREATE TRIGGER sidecar_dates AFTER UPDATE OF taken_at, taken_offset_min, date_source ON assets
    FOR EACH ROW WHEN (NEW.date_source = 'user' OR OLD.date_source = 'user') EXECUTE FUNCTION queue_sidecar_asset();
CREATE TRIGGER sidecar_person_name AFTER UPDATE OF name, hidden ON people FOR EACH ROW EXECUTE FUNCTION queue_sidecar_person();

-- What's already there: every item with something of its owner's to write.
INSERT INTO sidecar_queue (asset_id)
SELECT s.asset_id FROM asset_user_state s JOIN assets a ON a.id = s.asset_id
WHERE s.user_id = a.owner_id AND (s.favorite OR s.rating > 0 OR s.flag IS NOT NULL OR s.color_label IS NOT NULL)
UNION SELECT asset_id FROM asset_keywords
UNION SELECT asset_id FROM asset_descriptions
UNION SELECT asset_id FROM asset_edits
UNION SELECT asset_id FROM given_prompts
UNION SELECT f.asset_id FROM faces f JOIN people p ON p.id = f.person_id WHERE p.name <> ''
UNION SELECT id FROM assets WHERE date_source = 'user'
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TRIGGER sidecar_person_name ON people;
DROP TRIGGER sidecar_dates ON assets;
DROP TRIGGER sidecar_faces ON faces;
DROP TRIGGER sidecar_given_prompts ON given_prompts;
DROP TRIGGER sidecar_edits ON asset_edits;
DROP TRIGGER sidecar_descriptions ON asset_descriptions;
DROP TRIGGER sidecar_keywords ON asset_keywords;
DROP TRIGGER sidecar_marks ON asset_user_state;
DROP FUNCTION queue_sidecar_person();
DROP FUNCTION queue_sidecar_asset();
DROP FUNCTION queue_sidecar();
DROP TABLE sidecar_writes;
DROP TABLE sidecar_queue;
