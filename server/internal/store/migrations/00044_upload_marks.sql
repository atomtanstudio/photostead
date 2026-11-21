-- +goose Up
-- More of what can come with an upload, for importers (Google Takeout's JSON, XMP sidecars): a
-- favorite, the archive, stars, and where it was taken when the file doesn't say.
ALTER TABLE uploads ADD COLUMN favorite boolean NOT NULL DEFAULT false;
ALTER TABLE uploads ADD COLUMN archived boolean NOT NULL DEFAULT false;
ALTER TABLE uploads ADD COLUMN rating smallint;
ALTER TABLE uploads ADD COLUMN lat double precision;
ALTER TABLE uploads ADD COLUMN lon double precision;

-- +goose Down
ALTER TABLE uploads DROP COLUMN favorite, DROP COLUMN archived, DROP COLUMN rating, DROP COLUMN lat, DROP COLUMN lon;
