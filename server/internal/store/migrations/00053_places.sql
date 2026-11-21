-- +goose Up
-- Places: where items were taken, named offline from GeoNames (see internal/places). An item with a
-- location gets the nearest town within 60 km, with its region and country; place_version says
-- which matching looked at it, and a changed location is looked at again.
CREATE TABLE places (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    geoname_id   integer NOT NULL UNIQUE,
    name         text NOT NULL,
    admin1       text NOT NULL DEFAULT '',
    country      text NOT NULL DEFAULT '',
    country_code text NOT NULL DEFAULT '',
    lat          double precision NOT NULL,
    lon          double precision NOT NULL
);

ALTER TABLE asset_metadata ADD COLUMN place_id uuid REFERENCES places ON DELETE SET NULL;
ALTER TABLE asset_metadata ADD COLUMN place_version smallint NOT NULL DEFAULT 0;
CREATE INDEX asset_metadata_place ON asset_metadata (place_id) WHERE place_id IS NOT NULL;
CREATE INDEX asset_metadata_located ON asset_metadata (asset_id) WHERE lat IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION place_moved() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.place_id := NULL;
    NEW.place_version := 0;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER place_moved BEFORE UPDATE OF lat, lon ON asset_metadata FOR EACH ROW
    WHEN (NEW.lat IS DISTINCT FROM OLD.lat OR NEW.lon IS DISTINCT FROM OLD.lon) EXECUTE FUNCTION place_moved();

-- +goose Down
DROP TRIGGER place_moved ON asset_metadata;
DROP FUNCTION place_moved();
DROP INDEX asset_metadata_located;
DROP INDEX asset_metadata_place;
ALTER TABLE asset_metadata DROP COLUMN place_version;
ALTER TABLE asset_metadata DROP COLUMN place_id;
DROP TABLE places;
