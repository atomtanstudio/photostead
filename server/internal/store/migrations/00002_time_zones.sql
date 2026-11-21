-- Per-user time zones (chosen at onboarding) and how each asset's date was decided, so dates
-- can follow the owner's time zone and be recomputed when it changes. Nothing defaults to UTC.

-- +goose Up
-- NULL until the person chooses one; until then the server's configured zone is used (never UTC
-- unless the server itself is set to UTC), and the app asks.
ALTER TABLE users ADD COLUMN time_zone text;

-- recorded_offset: the file says which UTC offset it was taken at; the day is the day *there*.
-- camera_clock:    a camera's local clock with no offset; the day is the day it shows, and the
--                  instant assumes the owner's time zone.
-- instant:         a moment in time (video UTC clocks, file dates); the day is that moment's day
--                  in the owner's time zone.
ALTER TABLE assets ADD COLUMN date_basis text;
-- (A file date's offset is just the zone of whatever read it, so file dates are instants.)
UPDATE assets SET date_basis = CASE
    WHEN date_source IN ('quicktime', 'matroska', 'file-modified') THEN 'instant'
    WHEN taken_offset_min IS NOT NULL THEN 'recorded_offset'
    ELSE 'camera_clock' END;
UPDATE assets SET taken_offset_min = NULL WHERE date_basis = 'instant';
ALTER TABLE assets ALTER COLUMN date_basis SET NOT NULL;
ALTER TABLE assets ADD CONSTRAINT assets_date_basis CHECK (date_basis IN ('recorded_offset', 'camera_clock', 'instant'));

-- +goose Down
ALTER TABLE assets DROP CONSTRAINT assets_date_basis;
ALTER TABLE assets DROP COLUMN date_basis;
ALTER TABLE users DROP COLUMN time_zone;
