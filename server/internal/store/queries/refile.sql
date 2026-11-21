-- Re-filing an owner's items in their managed share after a date change (internal/refile). Not
-- through the gate: a job working for the owner, on files only.

-- name: RefileCandidates :many
-- The owner's present files, in writable managed libraries, of these items, with the day each
-- belongs under: a Live Photo's clip goes with its still (and into photos/), a RAW with the JPEG
-- it's stacked under.
SELECT f.id, f.library_id, l.root_path, f.rel_path, a.type,
       coalesce(still.local_date, cover.local_date, a.local_date)::date AS day,
       (still.id IS NOT NULL)::boolean AS clip
FROM files f
JOIN libraries l ON l.id = f.library_id
JOIN assets a ON a.id = f.asset_id
LEFT JOIN assets still ON still.id = a.motion_of
LEFT JOIN LATERAL (
    SELECT c.local_date FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id JOIN assets c ON c.id = st.cover_asset_id
    WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.kind = 'raw_jpeg' AND st.cover_asset_id <> a.id
    LIMIT 1
) cover ON true
WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner_id AND a.trashed_at IS NULL
  AND l.kind = 'managed' AND NOT l.read_only AND f.state = 'present'
ORDER BY a.taken_at, f.rel_path;

-- name: FileAtPath :one
-- Whether the library has a file row at a path (any state; compared without case, as the share's
-- users on Windows and macOS would).
SELECT EXISTS (SELECT 1 FROM files WHERE library_id = @library_id AND lower(rel_path) = lower(@rel_path))::boolean;

-- name: SidecarFile :one
SELECT id FROM files WHERE library_id = @library_id AND rel_path = @rel_path;

-- name: PlanRefile :one
INSERT INTO refiles (file_id, from_rel, to_rel, sidecar) VALUES (@file_id, @from_rel, @to_rel, @sidecar) RETURNING id;

-- name: OpenRefiles :many
-- Moves written down but not finished (a restart cut them short), for these files.
SELECT r.id, r.file_id, r.from_rel, r.to_rel, r.sidecar, l.root_path, f.library_id
FROM refiles r JOIN files f ON f.id = r.file_id JOIN libraries l ON l.id = f.library_id
WHERE r.state = 'planned' AND r.file_id = ANY(@file_ids::uuid[])
ORDER BY r.id;

-- name: MoveFileRow :execrows
-- The file is at its new path now (and seen there, so a scan running alongside doesn't call it missing).
UPDATE files SET rel_path = @to_rel, last_seen_at = now() WHERE id = @id AND rel_path = @from_rel;

-- name: FinishRefile :exec
UPDATE refiles SET state = @state, error = sqlc.narg(error), done_at = now() WHERE id = @id;
