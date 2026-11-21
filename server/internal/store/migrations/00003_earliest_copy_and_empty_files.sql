-- Data fixes found by the first full index (slice 1 reconciliation):
-- 1. Items dated from a file's modification time take the *earliest* time among all their copies
--    (a later copy carries the date it was copied, not when it was made).
-- 2. Empty (0-byte) files aren't media: they are skipped with a reason instead of becoming items.

-- +goose Up
WITH earliest AS (
    SELECT f.asset_id, min(f.mtime) AS mtime
    FROM files f
    WHERE f.asset_id IS NOT NULL AND f.state = 'present'
    GROUP BY f.asset_id
)
UPDATE assets a
SET taken_at = e.mtime,
    local_date = CASE WHEN u.time_zone IS NOT NULL THEN (e.mtime AT TIME ZONE u.time_zone)::date ELSE a.local_date END,
    updated_at = now()
FROM earliest e, users u
WHERE a.id = e.asset_id AND u.id = a.owner_id
  AND a.date_source = 'file-modified' AND a.taken_at > e.mtime;

UPDATE files SET state = 'skipped', reason = 'empty file (0 bytes)', asset_id = NULL WHERE size = 0;
DELETE FROM assets a
WHERE a.sha256 = decode('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', 'hex')
  AND NOT EXISTS (SELECT 1 FROM files f WHERE f.asset_id = a.id);

-- +goose Down
-- Nothing to undo: dates and skips are recomputed by the next scan.
SELECT 1;
