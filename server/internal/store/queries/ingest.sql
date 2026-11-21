-- Writes made while indexing. This is the only query file allowed to touch the assets table
-- directly; everything that reads assets for a viewer goes through visible_assets() (see gate_test.go).

-- name: UpsertSeenFile :one
-- Records a file found by a scan. A new, changed, failed or reappearing file goes (back) to pending.
INSERT INTO files (library_id, rel_path, size, mtime, kind, state, reason)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (library_id, rel_path) DO UPDATE SET
    last_seen_at = now(),
    size = EXCLUDED.size,
    mtime = EXCLUDED.mtime,
    kind = EXCLUDED.kind,
    reason = CASE WHEN EXCLUDED.state = 'skipped' THEN EXCLUDED.reason ELSE files.reason END,
    state = CASE
        WHEN EXCLUDED.state = 'skipped' THEN 'skipped'
        WHEN files.size <> EXCLUDED.size OR files.mtime <> EXCLUDED.mtime
          OR files.state IN ('failed', 'missing', 'skipped') THEN 'pending'
        ELSE files.state
    END
RETURNING id, state;

-- name: MarkMissing :execrows
UPDATE files SET state = 'missing'
WHERE library_id = $1 AND last_seen_at < $2 AND state NOT IN ('missing', 'deleted');

-- name: MarkMissingIn :execrows
-- After scanning one folder (prefix is "folder/"): its files, and unless shallow its
-- subfolders' files, that the scan didn't see.
UPDATE files SET state = 'missing'
WHERE library_id = @library_id AND last_seen_at < @started_at AND state NOT IN ('missing', 'deleted')
  AND left(rel_path, length(@prefix::text)) = @prefix::text
  AND (NOT @shallow::boolean OR strpos(substr(rel_path, length(@prefix::text) + 1), '/') = 0);

-- name: PendingFiles :many
SELECT id, kind FROM files WHERE library_id = $1 AND state = 'pending' ORDER BY rel_path;

-- name: GetFileForIngest :one
SELECT f.id, f.rel_path, f.size, f.mtime, f.kind, f.state,
       l.id AS library_id, l.root_path, l.owner_id, l.read_only, u.time_zone AS owner_time_zone
FROM files f JOIN libraries l ON l.id = f.library_id JOIN users u ON u.id = l.owner_id
WHERE f.id = $1;

-- name: SetFilePresent :exec
UPDATE files SET state = 'present', reason = NULL, sha256 = $2, alt_hashes = $3, mime = $4,
    asset_id = $5, last_verified_at = now()
WHERE id = $1;

-- Items with no date inside their file are dated from their copies' file times and names
-- (metadata.DateFromCopies, decision 1.7).

-- name: DerivedDateCopies :many
SELECT a.id AS asset_id, u.time_zone, f.rel_path, f.mtime
FROM assets a JOIN users u ON u.id = a.owner_id JOIN files f ON f.asset_id = a.id AND f.state = 'present'
WHERE a.date_source IN ('file-modified', 'name') AND (@everything::boolean OR a.id = ANY(@ids::uuid[]))
ORDER BY a.id;

-- name: SetDerivedDate :execrows
UPDATE assets SET taken_at = @taken_at, local_date = @local_date, date_source = @date_source, date_basis = @date_basis,
    taken_offset_min = NULL, updated_at = now()
WHERE id = @id AND date_source IN ('file-modified', 'name')
  AND (date_trunc('second', taken_at) <> date_trunc('second', @taken_at::timestamptz)
       OR local_date <> @local_date OR date_source <> @date_source);

-- name: SetFileState :exec
UPDATE files SET state = $2, reason = $3 WHERE id = $1;

-- name: FindAssetByHash :one
SELECT id, render_state FROM assets WHERE owner_id = $1 AND sha256 = $2;

-- name: InsertAsset :one
INSERT INTO assets (owner_id, type, sha256, primary_file_id, mime, taken_at, taken_offset_min,
    local_date, date_source, date_basis, width, height, orientation, duration_ms, content_identifier)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (owner_id, sha256) DO UPDATE SET updated_at = now()
RETURNING id, render_state, (xmax = 0)::boolean AS created;

-- name: UpsertAssetMetadata :exec
INSERT INTO asset_metadata (asset_id, make, model, lens, focal_mm, fnumber, exposure_s, iso,
    lat, lon, altitude, title, description, raw)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (asset_id) DO UPDATE SET make = EXCLUDED.make, model = EXCLUDED.model, lens = EXCLUDED.lens,
    focal_mm = EXCLUDED.focal_mm, fnumber = EXCLUDED.fnumber, exposure_s = EXCLUDED.exposure_s,
    iso = EXCLUDED.iso, lat = EXCLUDED.lat, lon = EXCLUDED.lon, altitude = EXCLUDED.altitude,
    title = EXCLUDED.title, description = EXCLUDED.description, raw = EXCLUDED.raw;

-- name: SetAssetRendered :exec
UPDATE assets SET thumbhash = $2, render_state = $3, render_error = $4,
    width = coalesce(sqlc.narg(width), width), height = coalesce(sqlc.narg(height), height), updated_at = now()
WHERE id = $1;

-- name: AssetsToRender :many
SELECT a.id, a.type FROM assets a WHERE a.render_state = ANY(@states::text[]) ORDER BY a.id;

-- name: RenderedVideos :many
-- Videos with a poster, and its ThumbHash (to find the blank ones).
SELECT a.id, a.thumbhash FROM assets a
WHERE a.type = 'video' AND a.render_state = 'done' AND a.motion_of IS NULL AND a.purged_at IS NULL ORDER BY a.id;

-- name: ForgetPictureAnalyses :exec
-- A re-render changed the picture (a video's new poster frame): what was read from the old one is
-- read again from the new: the search embedding, text, faces, pets, labels, adult content and look-alikes.
WITH e AS (DELETE FROM asset_embeddings x WHERE x.asset_id = @id::uuid),
     s AS (DELETE FROM adult_scans x WHERE x.asset_id = @id::uuid),
     o AS (DELETE FROM asset_ocr x WHERE x.asset_id = @id::uuid),
     f AS (DELETE FROM face_scans x WHERE x.asset_id = @id::uuid),
     p AS (DELETE FROM pet_scans x WHERE x.asset_id = @id::uuid),
     l AS (DELETE FROM labels_checked x WHERE x.asset_id = @id::uuid)
DELETE FROM near_duplicate_checks x WHERE x.asset_id = @id::uuid;

-- name: GetAssetForRender :one
SELECT a.id, a.type, a.duration_ms, a.width, a.height, a.thumbhash, f.rel_path, l.root_path
FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
WHERE a.id = $1;

-- Time zone changes. A camera clock keeps its wall-clock time and moves to the new zone; an
-- instant keeps its moment and gets the day it falls on in the new zone. Times recorded with
-- their own offset don't change.

-- name: ShiftCameraClockAssets :execrows
UPDATE assets SET taken_at = (taken_at AT TIME ZONE @old_zone::text) AT TIME ZONE @new_zone::text, updated_at = now()
WHERE owner_id = @owner_id AND date_basis = 'camera_clock';

-- name: RedateInstantAssets :execrows
UPDATE assets SET local_date = (taken_at AT TIME ZONE @new_zone::text)::date, updated_at = now()
WHERE owner_id = @owner_id AND date_basis = 'instant'
  AND local_date <> (taken_at AT TIME ZONE @new_zone::text)::date;

-- Excluding a folder: its files leave the index; items that still have a copy elsewhere keep it.

-- name: DeleteFilesUnder :many
DELETE FROM files WHERE library_id = @library_id AND starts_with(rel_path, @folder::text || '/')
RETURNING asset_id;

-- name: RepointPrimary :exec
-- For items that lost their primary file: point at a remaining copy (the oldest).
WITH remaining AS (
    SELECT f.asset_id, (array_agg(f.id ORDER BY f.mtime, f.id))[1] AS first_file
    FROM files f
    WHERE f.asset_id = ANY(@asset_ids::uuid[]) AND f.state = 'present'
    GROUP BY f.asset_id
)
UPDATE assets a SET primary_file_id = r.first_file, updated_at = now()
FROM remaining r
WHERE a.id = r.asset_id AND a.primary_file_id IS NULL;

-- name: DeleteOrphanAssets :many
DELETE FROM assets a
WHERE a.id = ANY(@asset_ids::uuid[]) AND NOT EXISTS (SELECT 1 FROM files f WHERE f.asset_id = a.id)
RETURNING id;

-- name: SeedMarks :exec
-- Stars, a reject flag or a color label written into a new item's file (Lightroom, Bridge, a
-- camera), as its owner's. Anything the owner set here already stays.
INSERT INTO asset_user_state (asset_id, user_id, rating, flag, color_label)
VALUES (@asset_id, @owner_id, sqlc.narg(rating)::smallint, sqlc.narg(flag)::text, sqlc.narg(color)::text)
ON CONFLICT (asset_id, user_id) DO NOTHING;
