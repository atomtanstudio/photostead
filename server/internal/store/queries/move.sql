-- Planning the move into the managed share. Reads a person's own library for maintenance, so it
-- names the assets table directly (allowed in gate_test.go); it never serves a viewer.

-- name: MoveSources :many
-- One source file per item: the oldest copy (its name is the one most likely to be original),
-- with what the plan needs to place it.
WITH ranked AS (
    SELECT f.id, f.asset_id, f.library_id, f.rel_path, f.size,
           row_number() OVER (PARTITION BY f.asset_id ORDER BY f.mtime, length(f.rel_path), f.rel_path) AS rn,
           count(*) OVER (PARTITION BY f.asset_id) AS copies,
           sum(f.size) OVER (PARTITION BY f.asset_id) AS all_bytes
    FROM files f
    WHERE f.state = 'present' AND f.kind IN ('image', 'video') AND f.asset_id IS NOT NULL
)
SELECT a.id AS asset_id, a.type, a.local_date, a.taken_at, a.live_motion_id, a.motion_of,
       st.cover_asset_id AS stack_cover,
       r.id AS file_id, r.library_id, l.name AS library_name, r.rel_path, r.size,
       r.copies::integer AS copies, (r.all_bytes - r.size)::bigint AS other_copies_bytes
FROM assets a
JOIN ranked r ON r.asset_id = a.id AND r.rn = 1
JOIN libraries l ON l.id = r.library_id
LEFT JOIN stack_assets sa ON sa.asset_id = a.id
LEFT JOIN stacks st ON st.id = sa.stack_id AND st.state = 'confirmed' AND st.kind = 'raw_jpeg'
WHERE a.owner_id = @owner_id AND a.trashed_at IS NULL
ORDER BY a.local_date, a.taken_at, a.id;

-- name: MoveSidecars :many
SELECT s.id, s.library_id, l.name AS library_name, s.rel_path, s.size
FROM files s JOIN libraries l ON l.id = s.library_id
WHERE s.kind = 'sidecar' AND l.owner_id = @owner_id;

-- name: CreateMovePlan :one
INSERT INTO move_plans (owner_id, summary) VALUES (@owner_id, @summary) RETURNING id, created_at;

-- name: MoveCopies :many
-- Every present copy of every item, so a sidecar next to any copy can travel with the item.
SELECT f.asset_id, f.library_id, l.name AS library_name, f.rel_path
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE f.state = 'present' AND f.kind IN ('image', 'video') AND f.asset_id IS NOT NULL AND l.owner_id = @owner_id;

-- name: GetMovePlan :one
SELECT p.id, p.owner_id, p.created_at, u.username FROM move_plans p JOIN users u ON u.id = p.owner_id WHERE p.id = @id;

-- name: PendingMoveEntries :many
-- The next batch to copy: originals and clips before sidecars, so a sidecar's file is in place first.
SELECT e.role, e.asset_id, e.file_id, e.src_rel, e.dst_rel, e.size, l.root_path,
       f.sha256 AS src_sha256, f.mtime AS src_mtime, f.kind AS src_kind
FROM move_plan_entries e
JOIN libraries l ON l.id = e.library_id
LEFT JOIN files f ON f.id = e.file_id
WHERE e.plan_id = @plan_id AND e.state = 'pending'
ORDER BY (e.role = 'sidecar'), e.dst_rel
LIMIT @batch;

-- name: FinishMoveEntry :exec
UPDATE move_plan_entries SET state = @state, error = @error, done_at = now(), new_file_id = @new_file_id
WHERE plan_id = @plan_id AND dst_rel = @dst_rel;

-- name: MoveProgress :many
SELECT state, role, count(*)::bigint AS files, coalesce(sum(size), 0)::bigint AS bytes
FROM move_plan_entries WHERE plan_id = @plan_id GROUP BY 1, 2 ORDER BY 1, 2;

-- name: MoveFailures :many
SELECT src_rel, dst_rel, error FROM move_plan_entries WHERE plan_id = @plan_id AND state = 'failed' ORDER BY dst_rel LIMIT @lim;

-- name: EnsureManagedLibrary :one
-- Each person's folder in the managed share is a library of its own, owned by them.
INSERT INTO libraries (owner_id, name, kind, root_path, read_only, sidecar_mode)
VALUES (@owner_id, 'photostead', 'managed', @root_path, false, 'beside')
ON CONFLICT (root_path) DO UPDATE SET read_only = false
RETURNING id;

-- name: RecordMovedFile :one
INSERT INTO files (library_id, rel_path, size, mtime, sha256, kind, role, state, asset_id, last_verified_at)
VALUES (@library_id, @rel_path, @size, @mtime, @sha256, @kind, @role, 'present', @asset_id, now())
ON CONFLICT (library_id, rel_path) DO UPDATE SET size = EXCLUDED.size, mtime = EXCLUDED.mtime, sha256 = EXCLUDED.sha256,
    state = 'present', asset_id = EXCLUDED.asset_id, last_verified_at = now(), last_seen_at = now()
RETURNING id;

-- name: UseMovedFile :exec
-- The item is now served from its copy in the managed share.
UPDATE assets SET primary_file_id = @file_id, updated_at = now() WHERE id = @asset_id;

-- name: LatestMoveProgress :one
-- The newest plan a person has started carrying out, and how far it has got.
SELECT p.id, count(*)::bigint AS total, count(*) FILTER (WHERE e.state = 'done')::bigint AS done,
       count(*) FILTER (WHERE e.state = 'failed')::bigint AS failed,
       coalesce(sum(e.size), 0)::bigint AS bytes, coalesce(sum(e.size) FILTER (WHERE e.state = 'done'), 0)::bigint AS bytes_done
FROM move_plans p JOIN move_plan_entries e ON e.plan_id = p.id
WHERE p.owner_id = @owner_id
  AND EXISTS (SELECT 1 FROM move_plan_entries x WHERE x.plan_id = p.id AND x.state <> 'pending')
GROUP BY p.id, p.created_at
ORDER BY p.created_at DESC
LIMIT 1;

-- name: LibraryOnlyCopies :one
-- Items a library holds the only present copy of (removing it would lose them; purged ones aside).
SELECT count(DISTINCT f.asset_id)::integer FROM files f JOIN assets a ON a.id = f.asset_id
WHERE f.library_id = @library_id AND f.state = 'present' AND a.purged_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM files g WHERE g.asset_id = f.asset_id AND g.state = 'present' AND g.library_id <> f.library_id);

-- name: RepointPrimaryFiles :execrows
-- Items served from a library about to go are served from another present copy: the managed share's
-- first, then the oldest.
UPDATE assets a SET primary_file_id = (
        SELECT g.id FROM files g JOIN libraries m ON m.id = g.library_id
        WHERE g.asset_id = a.id AND g.state = 'present' AND g.library_id <> @library_id AND g.kind IN ('image', 'video')
        ORDER BY (m.kind = 'managed') DESC, g.first_seen_at, g.id LIMIT 1),
    updated_at = now()
WHERE a.primary_file_id IN (SELECT f.id FROM files f WHERE f.library_id = @library_id)
  AND EXISTS (SELECT 1 FROM files g WHERE g.asset_id = a.id AND g.state = 'present' AND g.library_id <> @library_id AND g.kind IN ('image', 'video'));

-- name: DeleteLibrary :exec
-- A library's records go (its files' rows with it); nothing on disk is touched.
DELETE FROM libraries WHERE id = @id;
