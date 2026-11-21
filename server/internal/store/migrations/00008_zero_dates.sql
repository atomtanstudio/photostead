-- QuickTime's and Unix's zero moments (1904-01-01 and 1970-01-01, midnight UTC) mean "not set".
-- Items dated by one fall back to the earliest file time among their copies.

-- +goose Up
WITH earliest AS (
    SELECT f.asset_id, min(f.mtime) AS mtime FROM files f
    WHERE f.asset_id IS NOT NULL AND f.state = 'present' GROUP BY f.asset_id
)
UPDATE assets a
SET taken_at = e.mtime, taken_offset_min = NULL, date_source = 'file-modified', date_basis = 'instant',
    local_date = (e.mtime AT TIME ZONE u.time_zone)::date, updated_at = now()
FROM earliest e, users u
WHERE a.id = e.asset_id AND u.id = a.owner_id AND u.time_zone IS NOT NULL
  AND a.taken_at IN ('1904-01-01 00:00:00+00', '1970-01-01 00:00:00+00');

-- +goose Down
SELECT 1;
