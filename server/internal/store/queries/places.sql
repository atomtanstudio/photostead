-- Places (see 00053). Viewers read through library_assets(viewer), like the timeline.

-- name: ToPlace :many
SELECT asset_id, lat, lon FROM asset_metadata
WHERE lat IS NOT NULL AND lon IS NOT NULL AND place_version < @version::smallint
ORDER BY asset_id LIMIT @lim::integer;

-- name: UpsertPlace :one
INSERT INTO places (geoname_id, name, admin1, country, country_code, lat, lon)
VALUES (@geoname_id, @name, @admin1, @country, @country_code, @lat, @lon)
ON CONFLICT (geoname_id) DO UPDATE SET name = EXCLUDED.name, admin1 = EXCLUDED.admin1, country = EXCLUDED.country,
    country_code = EXCLUDED.country_code, lat = EXCLUDED.lat, lon = EXCLUDED.lon
RETURNING id;

-- name: SetPlace :exec
UPDATE asset_metadata SET place_id = sqlc.narg('place_id'), place_version = @version::smallint WHERE asset_id = @asset_id;

-- name: ListPlaces :many
-- The places of the viewer's library, each with its newest item as a cover, by country.
SELECT p.id, p.name, p.admin1, p.country, p.country_code, p.lat, p.lon, count(*)::integer AS count,
       (array_agg(a.id ORDER BY a.taken_at DESC))[1]::uuid AS cover,
       (array_agg(a.thumbhash ORDER BY a.taken_at DESC))[1]::bytea AS cover_hash
FROM library_assets(@viewer::uuid) a
JOIN asset_metadata m ON m.asset_id = a.id
JOIN places p ON p.id = m.place_id
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL
  AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
GROUP BY p.id
ORDER BY p.country, count(*) DESC, p.name;

-- name: MapPoints :many
-- Every item of the viewer's library with a location, for the map.
SELECT a.id, m.lat::double precision AS lat, m.lon::double precision AS lon, a.type, a.thumbhash
FROM library_assets(@viewer::uuid) a
JOIN asset_metadata m ON m.asset_id = a.id
WHERE m.lat IS NOT NULL AND m.lon IS NOT NULL AND a.visibility = 'timeline' AND a.motion_of IS NULL
  AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
ORDER BY a.taken_at DESC;

-- name: AssetPlace :one
SELECT p.id, p.name, p.admin1, p.country FROM asset_metadata m JOIN places p ON p.id = m.place_id WHERE m.asset_id = @asset_id;
