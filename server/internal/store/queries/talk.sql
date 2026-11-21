-- Comments, picks and activity on albums (shared ones, mostly). Who may see and write them is
-- settled by the API (AlbumRole) before these run.

-- name: AddAlbumComment :one
INSERT INTO album_comments (album_id, asset_id, author_id, body)
SELECT @album_id, sqlc.narg('asset_id'), @author_id, @body
WHERE sqlc.narg('asset_id')::uuid IS NULL
   OR EXISTS (SELECT 1 FROM album_assets aa WHERE aa.album_id = @album_id AND aa.asset_id = sqlc.narg('asset_id'))
RETURNING id, created_at;

-- name: AlbumComment :one
SELECT c.id, c.album_id, c.author_id, al.owner_id FROM album_comments c JOIN albums al ON al.id = c.album_id WHERE c.id = @id;

-- name: DeleteAlbumComment :exec
DELETE FROM album_comments WHERE id = @id;

-- name: AlbumComments :many
-- An album's comments (about one item, or all of them), oldest first.
SELECT c.id, c.asset_id, c.author_id, coalesce(nullif(u.display_name, ''), split_part(u.email, '@', 1))::text AS author, u.role AS author_role,
       c.body, c.created_at
FROM album_comments c JOIN users u ON u.id = c.author_id
WHERE c.album_id = @album_id AND (sqlc.narg('asset_id')::uuid IS NULL OR c.asset_id = sqlc.narg('asset_id'))
ORDER BY c.created_at
LIMIT 500;

-- name: AlbumActivity :many
-- What happened in an album, newest first: items added (by whom, a day's additions together) and
-- comments.
SELECT * FROM (
    SELECT 'added'::text AS kind, aa.added_by AS who_id,
           coalesce(nullif(u.display_name, ''), split_part(u.email, '@', 1), '')::text AS who,
           max(aa.added_at)::timestamptz AS at, count(*)::integer AS items,
           (array_agg(aa.asset_id ORDER BY aa.added_at DESC))[1:6]::uuid[] AS assets, ''::text AS body, NULL::uuid AS asset_id,
           NULL::uuid AS comment_id
    FROM album_assets aa LEFT JOIN users u ON u.id = aa.added_by
    WHERE aa.album_id = @album_id AND (aa.added_via IS NULL OR aa.added_via <> 'immich')
    GROUP BY aa.added_by, u.display_name, u.email, date_trunc('day', aa.added_at)
    UNION ALL
    SELECT 'comment', c.author_id, coalesce(nullif(u.display_name, ''), split_part(u.email, '@', 1))::text, c.created_at, 0,
           '{}'::uuid[], c.body, c.asset_id, c.id
    FROM album_comments c JOIN users u ON u.id = c.author_id
    WHERE c.album_id = @album_id
) a
ORDER BY at DESC
LIMIT 100;

-- name: SetAlbumPick :exec
INSERT INTO album_picks (album_id, asset_id, user_id)
SELECT @album_id, @asset_id, @user_id WHERE EXISTS (SELECT 1 FROM album_assets WHERE album_id = @album_id AND asset_id = @asset_id)
ON CONFLICT DO NOTHING;

-- name: RemoveAlbumPick :exec
DELETE FROM album_picks WHERE album_id = @album_id AND asset_id = @asset_id AND user_id = @user_id;

-- name: AlbumPicks :many
-- Who picked what in an album.
SELECT p.asset_id, p.user_id, coalesce(nullif(u.display_name, ''), split_part(u.email, '@', 1))::text AS who, p.created_at
FROM album_picks p JOIN users u ON u.id = p.user_id
WHERE p.album_id = @album_id
ORDER BY p.created_at;
