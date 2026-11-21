-- Share links. What a link shows is its album's items as the album's owner sees them (through
-- visible_assets(owner)): in their timeline or archive, rendered, not a Live Photo's clip on its own.

-- name: CreateLink :one
INSERT INTO share_links (token, owner_id, album_id, password_hash, expires_at, allow_download, allow_upload)
VALUES (@token, @owner_id, @album_id, sqlc.narg(password_hash), sqlc.narg(expires_at), @allow_download, @allow_upload)
RETURNING *;

-- name: AlbumLinks :many
SELECT * FROM share_links WHERE album_id = @album_id AND owner_id = @owner_id ORDER BY created_at;

-- name: UpdateLink :one
UPDATE share_links SET password_hash = CASE WHEN @set_password::boolean THEN sqlc.narg(password_hash) ELSE password_hash END,
    expires_at = sqlc.narg(expires_at), allow_download = @allow_download, allow_upload = @allow_upload
WHERE id = @id AND owner_id = @owner_id
RETURNING *;

-- name: DeleteLink :execrows
DELETE FROM share_links WHERE id = @id AND owner_id = @owner_id;

-- name: LinkByToken :one
-- A link that hasn't run out, with its album and whose it is.
SELECT l.*, al.title AS album_title, al.description AS album_description, al.kind AS album_kind,
       coalesce(nullif(u.display_name, ''), u.username)::text AS owner_name
FROM share_links l JOIN albums al ON al.id = l.album_id JOIN users u ON u.id = l.owner_id
WHERE l.token = @token AND (l.expires_at IS NULL OR l.expires_at > now());

-- name: LinkViewed :exec
UPDATE share_links SET views = views + 1, last_viewed_at = now() WHERE id = @id;

-- name: LinkItems :many
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.live_motion_id
FROM album_assets aa JOIN visible_assets(@owner_id::uuid) a ON a.id = aa.asset_id
WHERE aa.album_id = @album_id::uuid AND a.visibility IN ('timeline', 'archived') AND a.motion_of IS NULL AND a.render_state = 'done'
ORDER BY a.taken_at DESC, a.id
LIMIT 5000;

-- name: LinkItem :one
-- One of a link's items (or a Live Photo clip of one), with its file and whether it records a location.
SELECT a.id, a.type, a.mime, f.rel_path, l.root_path,
       (m.lat IS NOT NULL OR m.lon IS NOT NULL)::boolean AS located
FROM visible_assets(@owner_id::uuid) a
JOIN files f ON f.id = a.primary_file_id
JOIN libraries l ON l.id = f.library_id
LEFT JOIN asset_metadata m ON m.asset_id = a.id
WHERE a.id = @id::uuid AND a.visibility IN ('timeline', 'archived')
  AND EXISTS (SELECT 1 FROM album_assets aa WHERE aa.album_id = @album_id::uuid AND aa.asset_id IN (a.id, a.motion_of));

-- name: LinkAccess :one
-- A browser that gave a link's password: which link it opened (good for a day).
SELECT data FROM auth_flows WHERE token_hash = @token_hash AND kind = 'link_access' AND expires_at > now();
