-- Working out who's in what: maintenance of a person's own library, so these may name the assets
-- table (see gate_test.go). What a viewer reads is in collections.sql and timeline.sql.

-- name: CharacterOwners :many
-- Whose characters and start images to work out: anyone with a character or a ComfyUI video.
SELECT owner_id FROM characters
UNION
SELECT a.owner_id FROM generations g JOIN assets a ON a.id = g.asset_id WHERE a.type = 'video' AND g.source = 'comfyui';

-- name: CharacterRules :many
SELECT id, name, loras FROM characters WHERE owner_id = @owner_id ORDER BY name;

-- name: LoraNames :many
SELECT id, name FROM ai_models WHERE kind = 'lora';

-- name: LoraAssets :many
-- The owner's items made with these LoRAs.
SELECT gl.lora_id, gl.asset_id FROM generation_loras gl JOIN assets a ON a.id = gl.asset_id
WHERE gl.lora_id = ANY(@ids::uuid[]) AND a.owner_id = @owner_id;

-- name: LineageVideos :many
-- The owner's ComfyUI videos with a graph or workflow to read reference images from.
SELECT a.id, a.taken_at, g.graph, g.workflow, coalesce(m.name, '')::text AS model
FROM generations g JOIN assets a ON a.id = g.asset_id LEFT JOIN ai_models m ON m.id = g.model_id
WHERE a.owner_id = @owner_id AND a.type = 'video' AND g.source = 'comfyui' AND (g.graph IS NOT NULL OR g.workflow IS NOT NULL);

-- name: ImageNames :many
-- Every image file name in the owner's library, with its item, when it was taken and whether it
-- carries AI evidence: what a reference image's name can resolve to.
SELECT f.rel_path, a.id AS asset_id, a.taken_at, (g.asset_id IS NOT NULL)::boolean AS ai
FROM files f JOIN assets a ON a.id = f.asset_id LEFT JOIN generations g ON g.asset_id = a.id
WHERE a.owner_id = @owner_id AND f.state = 'present' AND f.kind = 'image';

-- name: ClearAutomaticMembers :exec
DELETE FROM character_assets ca USING characters c
WHERE ca.character_id = c.id AND c.owner_id = @owner_id AND ca.basis IN ('metadata', 'lineage');

-- name: AddMembers :exec
-- Matched members; an item the owner confirmed or rejected keeps that.
INSERT INTO character_assets (character_id, asset_id, basis)
SELECT unnest(@character_ids::uuid[]), unnest(@asset_ids::uuid[]), @basis
ON CONFLICT (character_id, asset_id) DO NOTHING;

-- name: ClearStarts :exec
DELETE FROM generation_starts gs USING assets a WHERE a.id = gs.video_id AND a.owner_id = @owner_id;

-- name: AddStarts :exec
-- Clips and the images they were made from, with how alike they look (NaN when not compared).
INSERT INTO generation_starts (video_id, image_id, look)
SELECT p.v, p.i, nullif(p.l, 'NaN')
FROM (SELECT unnest(@video_ids::uuid[]) AS v, unnest(@image_ids::uuid[]) AS i, unnest(@looks::real[]) AS l) p;

-- name: CreateCharacter :one
INSERT INTO characters (owner_id, name, loras) VALUES (@owner_id, @name, @loras) RETURNING id;

-- name: UpdateCharacter :execrows
UPDATE characters SET name = @name, loras = @loras, updated_at = now() WHERE id = @id AND owner_id = @owner_id;

-- name: DeleteCharacter :execrows
DELETE FROM characters WHERE id = @id AND owner_id = @owner_id;

-- name: SetMemberBasis :execrows
-- The owner confirms (or rejects) items for one of their characters.
INSERT INTO character_assets (character_id, asset_id, basis)
SELECT c.id, a.id, @basis FROM characters c JOIN assets a ON a.owner_id = c.owner_id
WHERE c.id = @character_id AND c.owner_id = @owner_id AND a.id = ANY(@asset_ids::uuid[])
ON CONFLICT (character_id, asset_id) DO UPDATE SET basis = EXCLUDED.basis;

-- name: LookAlike :many
-- How alike pairs of items look, by their search embeddings (only pairs where both have one).
SELECT p.a::uuid AS a, p.b::uuid AS b, (1 - (ea.embedding <=> eb.embedding))::real AS sim
FROM (SELECT unnest(@a::uuid[]) AS a, unnest(@b::uuid[]) AS b) p
JOIN asset_embeddings ea ON ea.asset_id = p.a AND ea.model = @model::text
JOIN asset_embeddings eb ON eb.asset_id = p.b AND eb.model = @model::text;
