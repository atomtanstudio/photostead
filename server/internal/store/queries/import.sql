-- Importing from other apps (Immich first): a person's own library, so these may name the
-- assets table (see gate_test.go). Nothing here is read by a viewer.

-- name: GetConnection :one
SELECT url, secret, updated_at FROM connections WHERE owner_id = @owner_id AND kind = @kind;

-- name: SaveConnection :exec
INSERT INTO connections (owner_id, kind, url, secret) VALUES (@owner_id, @kind, @url, @secret)
ON CONFLICT (owner_id, kind) DO UPDATE SET url = EXCLUDED.url, secret = EXCLUDED.secret, updated_at = now();

-- name: DeleteConnection :exec
DELETE FROM connections WHERE owner_id = @owner_id AND kind = @kind;

-- name: StartImport :one
INSERT INTO imports (owner_id, source, step) VALUES (@owner_id, @source, @step) RETURNING id;

-- name: InterruptedImports :exec
-- Runs a restart cut short (any still marked running but the one starting), marked before a
-- new run starts.
UPDATE imports SET state = 'failed', step = NULL, error = 'Stopped before it finished', finished_at = now()
WHERE owner_id = @owner_id AND source = @source AND state = 'running' AND id <> @keep::uuid;

-- name: ImportStep :exec
UPDATE imports SET step = @step WHERE id = @id;

-- name: FinishImport :exec
UPDATE imports SET state = @state, step = NULL, summary = @summary, error = sqlc.narg(error), finished_at = now()
WHERE id = @id;

-- name: LatestImport :one
SELECT id, state, step, summary, error, started_at, finished_at
FROM imports WHERE owner_id = @owner_id AND source = @source
ORDER BY started_at DESC LIMIT 1;

-- name: ImportFiles :many
-- Every present photo or video file of a person's that belongs to an item, for matching another
-- app's items by path or by content.
SELECT f.library_id, f.rel_path, coalesce(f.alt_hashes->>'sha1', '')::text AS sha1, f.asset_id::uuid AS asset_id
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE l.owner_id = @owner_id AND f.asset_id IS NOT NULL AND f.state = 'present' AND f.kind IN ('image', 'video');

-- name: ImportMap :many
SELECT source_id, target_id FROM import_map WHERE owner_id = @owner_id AND source = @source AND kind = @kind;

-- name: SaveImportMap :exec
INSERT INTO import_map (owner_id, source, kind, source_id, target_id)
SELECT @owner_id, @source, @kind, unnest(@source_ids::text[]), unnest(@target_ids::uuid[])
ON CONFLICT (owner_id, source, kind, source_id) DO UPDATE SET target_id = EXCLUDED.target_id, imported_at = now();

-- name: ImportAssetStates :many
SELECT asset_id, favorite, archived FROM import_asset_state WHERE owner_id = @owner_id AND source = @source;

-- name: SaveImportAssetStates :exec
INSERT INTO import_asset_state (owner_id, source, asset_id, favorite, archived)
SELECT @owner_id, @source, unnest(@asset_ids::uuid[]), unnest(@favorites::boolean[]), unnest(@archived::boolean[])
ON CONFLICT (owner_id, source, asset_id) DO UPDATE SET favorite = EXCLUDED.favorite, archived = EXCLUDED.archived;

-- name: SetFavorite :exec
INSERT INTO asset_user_state (asset_id, user_id, favorite)
SELECT unnest(@asset_ids::uuid[]), @user_id, @favorite
ON CONFLICT (asset_id, user_id) DO UPDATE SET favorite = EXCLUDED.favorite, updated_at = now();

-- name: SetArchived :execrows
-- Moves items between the timeline and the archive; hidden and vault items are never touched.
UPDATE assets SET visibility = CASE WHEN @archived::boolean THEN 'archived' ELSE 'timeline' END, updated_at = now()
WHERE owner_id = @owner_id AND id = ANY(@asset_ids::uuid[]) AND visibility IN ('timeline', 'archived')
  AND visibility <> CASE WHEN @archived::boolean THEN 'archived' ELSE 'timeline' END;

-- name: CreateAlbum :one
INSERT INTO albums (owner_id, title, description, cover_asset_id, created_at)
VALUES (@owner_id, @title, @description, sqlc.narg(cover_asset_id), @created_at)
RETURNING id;

-- name: UpdateImportedAlbum :execrows
-- No row means the album was deleted here; the import then makes it again.
UPDATE albums SET title = @title, description = @description, cover_asset_id = sqlc.narg(cover_asset_id), updated_at = now()
WHERE id = @id AND owner_id = @owner_id;

-- name: AddAlbumAssets :execrows
INSERT INTO album_assets (album_id, asset_id, added_by, added_via)
SELECT @album_id, unnest(@asset_ids::uuid[]), @user_id, @via
ON CONFLICT (album_id, asset_id) DO NOTHING;

-- name: RemoveImportedAlbumAssets :execrows
-- Takes out what an earlier import put in and the other app no longer has; never what was
-- added here.
DELETE FROM album_assets WHERE album_id = @album_id AND added_via = @via AND NOT (asset_id = ANY(@asset_ids::uuid[]));

-- name: CreatePerson :one
INSERT INTO people (owner_id, name, birth_date, hidden, favorite)
VALUES (@owner_id, @name, sqlc.narg(birth_date), @hidden, @favorite)
RETURNING id;

-- name: UpdateImportedPerson :execrows
UPDATE people SET name = @name, birth_date = sqlc.narg(birth_date), hidden = @hidden, favorite = @favorite, updated_at = now()
WHERE id = @id AND owner_id = @owner_id;

-- name: AddPersonSuggestions :execrows
INSERT INTO person_suggestions (person_id, asset_id, source)
SELECT @person_id, unnest(@asset_ids::uuid[]), @source
ON CONFLICT (person_id, asset_id, source) DO NOTHING;

-- name: RemovePersonSuggestions :execrows
DELETE FROM person_suggestions WHERE person_id = @person_id AND source = @source AND NOT (asset_id = ANY(@asset_ids::uuid[]));

-- name: ImportDescriptions :execrows
-- Descriptions from another app. One written or sent here is never replaced.
INSERT INTO asset_descriptions (asset_id, text, source)
SELECT unnest(@asset_ids::uuid[]), unnest(@texts::text[]), @source::text
ON CONFLICT (asset_id) DO UPDATE SET text = EXCLUDED.text, updated_at = now()
WHERE asset_descriptions.source = EXCLUDED.source AND asset_descriptions.text <> EXCLUDED.text;

-- name: EnsureKeywords :many
INSERT INTO keywords (owner_id, name)
SELECT @owner_id::uuid, unnest(@names::text[])
ON CONFLICT (owner_id, name) DO UPDATE SET name = EXCLUDED.name
RETURNING id, name;

-- name: AddAssetKeywords :execrows
INSERT INTO asset_keywords (asset_id, keyword_id, source)
SELECT unnest(@asset_ids::uuid[]), unnest(@keyword_ids::uuid[]), @source::text
ON CONFLICT (asset_id, keyword_id) DO NOTHING;

-- name: ImportRatings :execrows
-- Stars (and rejects) from Immich, for items the owner hasn't rated here.
INSERT INTO asset_user_state (asset_id, user_id, rating, flag)
SELECT x.asset_id, @owner_id::uuid, CASE WHEN x.r BETWEEN 1 AND 5 THEN x.r END, CASE WHEN x.r = -1 THEN 'reject' END
FROM (SELECT unnest(@asset_ids::uuid[]) AS asset_id, unnest(@ratings::smallint[]) AS r) x
ON CONFLICT (asset_id, user_id) DO UPDATE SET rating = coalesce(asset_user_state.rating, EXCLUDED.rating),
    flag = coalesce(asset_user_state.flag, EXCLUDED.flag), updated_at = now()
WHERE asset_user_state.rating IS NULL AND asset_user_state.flag IS NULL;
