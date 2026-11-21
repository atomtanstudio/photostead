-- The integrity scrub and the backup manifest.

-- name: FilesToVerify :many
-- Files to read again, the longest unchecked first.
SELECT f.id, l.root_path, f.rel_path, f.size, f.mtime, f.sha256
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE f.state = 'present' AND f.sha256 IS NOT NULL AND (f.last_verified_at IS NULL OR f.last_verified_at < @before)
ORDER BY f.last_verified_at NULLS FIRST, f.id
LIMIT @lim;

-- name: FileVerified :exec
UPDATE files SET last_verified_at = now(), damaged_at = NULL WHERE id = @id;

-- name: FileDamaged :exec
UPDATE files SET last_verified_at = now(), damaged_at = coalesce(damaged_at, now()) WHERE id = @id;

-- name: FileVerifyPassed :exec
-- Moved past without a verdict (missing, or changed since it was indexed: the rescan's to sort out).
UPDATE files SET last_verified_at = now() WHERE id = @id;

-- name: DamagedFiles :many
SELECT l.name AS library, f.rel_path, f.damaged_at::timestamptz AS damaged_at
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE f.damaged_at IS NOT NULL AND f.state = 'present'
ORDER BY f.damaged_at DESC
LIMIT 100;

-- name: IntegrityCounts :one
SELECT count(*) FILTER (WHERE state = 'present' AND sha256 IS NOT NULL)::bigint AS files,
       count(*) FILTER (WHERE state = 'present' AND sha256 IS NOT NULL AND last_verified_at > now() - interval '30 days')::bigint AS checked,
       count(*) FILTER (WHERE state = 'present' AND damaged_at IS NOT NULL)::bigint AS damaged
FROM files;

-- name: ManifestFiles :many
-- Every file Photostead has, for the manifest written with each backup.
SELECT l.name AS library, f.rel_path, f.size, f.mtime, f.sha256, f.asset_id
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE f.state = 'present'
ORDER BY l.name, f.rel_path;

-- name: DamagedCount :one
SELECT count(*) FROM files WHERE damaged_at IS NOT NULL AND state = 'present';
