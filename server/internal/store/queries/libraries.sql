-- name: CreateLibrary :one
INSERT INTO libraries (owner_id, name, kind, root_path, read_only, sidecar_mode, watch)
VALUES (@owner_id, @name, @kind, @root_path, @read_only, @sidecar_mode, @watch)
RETURNING *;

-- name: ListLibraries :many
SELECT * FROM libraries ORDER BY created_at;

-- name: GetLibrary :one
SELECT * FROM libraries WHERE id = $1;

-- name: GetLibraryByName :one
SELECT * FROM libraries WHERE owner_id = $1 AND name = $2;

-- name: MarkScanStarted :exec
UPDATE libraries SET last_scan_started_at = now() WHERE id = $1;

-- name: MarkScanFinished :exec
UPDATE libraries SET last_scan_finished_at = now() WHERE id = $1;

-- name: DBNow :one
SELECT now()::timestamptz;

-- name: LibraryFileCounts :many
SELECT l.name, f.kind, f.state, count(*)::integer AS count
FROM files f JOIN libraries l ON l.id = f.library_id
GROUP BY 1, 2, 3 ORDER BY 1, 2, 3;

-- name: WatchedLibraries :many
SELECT * FROM libraries WHERE watch AND kind = 'indexed' ORDER BY name;

-- name: SetLibraryWatch :one
UPDATE libraries SET watch = @watch WHERE id = @id RETURNING *;

-- name: MarkWatched :exec
UPDATE libraries SET watched_at = now() WHERE id = ANY(@ids::uuid[]);

-- name: SetLibraryExclude :one
UPDATE libraries SET exclude = @exclude::text[] WHERE id = @id RETURNING *;

-- name: PeopleWithoutShare :many
-- Accounts (not guests) with no writable managed library yet.
SELECT u.id, u.username FROM users u
WHERE u.role <> 'guest'
  AND NOT EXISTS (SELECT 1 FROM libraries l WHERE l.owner_id = u.id AND l.kind = 'managed' AND NOT l.read_only)
ORDER BY u.created_at;

-- name: FirstAdmin :one
SELECT id FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1;

-- name: LibraryRoots :many
SELECT root_path FROM libraries;
