-- name: AssetEdit :one
SELECT recipe, orig_width, orig_height, updated_at FROM asset_edits WHERE asset_id = $1;

-- name: SetAssetEdit :exec
-- The size before the first edit is kept through later ones.
INSERT INTO asset_edits (asset_id, recipe, orig_width, orig_height, updated_by)
VALUES (@asset_id, @recipe, @orig_width, @orig_height, @updated_by)
ON CONFLICT (asset_id) DO UPDATE SET recipe = EXCLUDED.recipe, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteAssetEdit :one
DELETE FROM asset_edits WHERE asset_id = $1 RETURNING orig_width, orig_height;

-- name: EditedAssets :many
-- Which of these items have an edit.
SELECT asset_id FROM asset_edits WHERE asset_id = ANY(@ids::uuid[]);
