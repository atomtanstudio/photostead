-- API keys and uploads: a person's own programs adding to their own library.

-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, token_hash) VALUES (@user_id, @name, @token_hash) RETURNING id;

-- name: UserByAPIKey :one
SELECT k.id AS key_id, k.name, k.user_id, k.last_used_at FROM api_keys k JOIN users u ON u.id = k.user_id
WHERE k.token_hash = @token_hash AND k.revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = now() WHERE id = @id;

-- name: ListAPIKeys :many
SELECT name, created_at, last_used_at FROM api_keys WHERE user_id = @user_id AND revoked_at IS NULL ORDER BY created_at;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = now() WHERE user_id = @user_id AND name = @name AND revoked_at IS NULL;

-- name: ManagedLibrary :one
-- Where a person's uploads go: their managed library (the photostead share), if it's writable.
SELECT id, root_path FROM libraries WHERE owner_id = @owner_id AND kind = 'managed' AND NOT read_only ORDER BY created_at LIMIT 1;

-- name: KnownHashes :many
-- Which of these files a person already has (deleted ones included, so they aren't sent again).
SELECT id, sha256 FROM assets WHERE owner_id = @owner_id AND sha256 = ANY(@hashes::bytea[]);

-- name: AssetByHash :one
SELECT id FROM assets WHERE owner_id = @owner_id AND sha256 = @sha256;

-- name: RecordUpload :one
INSERT INTO uploads (owner_id, file_id, asset_id, sha256, file_name, client, description, source_url, keywords, album,
                     favorite, archived, rating, lat, lon, prompt, applied_at)
VALUES (@owner_id, sqlc.narg('file_id'), sqlc.narg('asset_id'), @sha256, @file_name, @client, @description, @source_url, @keywords,
        @album, @favorite, @archived, sqlc.narg('rating'), sqlc.narg('lat'), sqlc.narg('lon'), @prompt,
        CASE WHEN sqlc.narg('asset_id')::uuid IS NULL THEN NULL ELSE now() END)
RETURNING id;

-- name: WaitingUploads :many
SELECT id, owner_id, description, source_url, keywords, album, favorite, archived, rating, lat, lon, prompt FROM uploads WHERE file_id = @file_id AND applied_at IS NULL;

-- name: UploadApplied :exec
UPDATE uploads SET asset_id = @asset_id, applied_at = now() WHERE id = @id;

-- name: AddUploadDescription :exec
-- An upload's description: added to what an import brought over or earlier uploads said (the same
-- picture posted twice keeps both), never replacing one written here.
INSERT INTO asset_descriptions (asset_id, text, source) VALUES (@asset_id, @text, 'upload')
ON CONFLICT (asset_id) DO UPDATE SET text = asset_descriptions.text || E'\n\n' || EXCLUDED.text, updated_at = now()
WHERE asset_descriptions.source <> 'user' AND strpos(asset_descriptions.text, EXCLUDED.text) = 0;

-- name: AlbumByTitle :one
SELECT id FROM albums WHERE owner_id = @owner_id AND kind = 'manual' AND lower(title) = lower(@title) ORDER BY created_at LIMIT 1;

-- Resumable uploads.

-- name: CreateUploadSession :one
INSERT INTO upload_sessions (token, owner_id, client, meta, length, path, expires_at)
VALUES (@token, @owner_id, @client, @meta, sqlc.narg('length'), @path, @expires_at)
RETURNING *;

-- name: UploadSession :one
-- An upload of this owner's, while it lasts.
SELECT * FROM upload_sessions WHERE token = @token AND owner_id = @owner_id AND expires_at > now();

-- name: SetUploadLength :exec
UPDATE upload_sessions SET length = @length WHERE id = @id AND length IS NULL;

-- name: CompleteUploadSession :exec
UPDATE upload_sessions SET completed_at = now(), reply_status = @reply_status, reply = @reply WHERE id = @id;

-- name: DeleteUploadSession :exec
DELETE FROM upload_sessions WHERE id = @id;

-- name: ExpiredUploadSessions :many
-- Uploads past their time, with their partial files to remove.
DELETE FROM upload_sessions WHERE expires_at <= now() RETURNING path, (completed_at IS NOT NULL)::boolean AS completed;

-- name: RecordDeviceItem :exec
-- A phone's item, sent: its file.
INSERT INTO device_items (owner_id, item, sha256) VALUES (@owner_id, @item, @sha256) ON CONFLICT DO NOTHING;

-- name: KnownDeviceItems :many
-- Which of a phone's items the person has sent (or said they had).
SELECT DISTINCT item FROM device_items WHERE owner_id = @owner_id AND item = ANY(@items::text[]);

-- name: RecordDeviceItems :execrows
-- A phone telling which of its items it sent before uploads said so: kept only for files the
-- person has.
INSERT INTO device_items (owner_id, item, sha256)
SELECT @owner_id::uuid, i.item, h.sha256
FROM unnest(@items::text[]) WITH ORDINALITY AS i(item, n)
JOIN unnest(@hashes::bytea[]) WITH ORDINALITY AS h(sha256, n) ON h.n = i.n
WHERE EXISTS (SELECT 1 FROM assets a WHERE a.owner_id = @owner_id::uuid AND a.sha256 = h.sha256)
   OR EXISTS (SELECT 1 FROM uploads u WHERE u.owner_id = @owner_id::uuid AND u.sha256 = h.sha256)
ON CONFLICT DO NOTHING;

-- name: SendingDeviceItems :many
-- A phone's items with an upload begun in the last two hours and not finished (by the app, or by
-- iOS's own uploads), with its partial file: whether bytes still come in is the file's to say
-- (api.sendingNow). from_app: the app's own upload (it carries the file's hash).
SELECT (meta->>'deviceItem')::text AS item, path, created_at, (coalesce(meta->>'sha256', '') <> '')::boolean AS from_app
FROM upload_sessions
WHERE owner_id = @owner_id AND completed_at IS NULL AND created_at > now() - interval '2 hours'
  AND meta->>'deviceItem' = ANY(@items::text[]);

-- name: DeviceItemKnown :one
-- Whether Photostead has a phone's item already.
SELECT EXISTS (SELECT 1 FROM device_items d WHERE d.owner_id = @owner_id AND d.item = @item::text) AS known;

-- name: SetUploadedLocation :exec
-- Where an uploaded item was taken, from what came with it, when its file doesn't say.
UPDATE asset_metadata SET lat = @lat, lon = @lon WHERE asset_id = @asset_id AND lat IS NULL AND lon IS NULL;
