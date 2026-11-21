-- AI provenance, written by ingest and the provenance pass over a person's own library, so these
-- may name the assets table (see gate_test.go). What a viewer reads goes through the gate
-- (GenerationForAsset).

-- name: UpsertAIModel :one
INSERT INTO ai_models (kind, name, display) VALUES (@kind, @name, @display)
ON CONFLICT (kind, name) DO UPDATE SET name = EXCLUDED.name
RETURNING id;

-- name: UpsertPrompt :one
INSERT INTO prompts (owner_id, text, normalized_hash) VALUES (@owner_id, @text, @hash)
ON CONFLICT (owner_id, normalized_hash) DO UPDATE SET owner_id = EXCLUDED.owner_id
RETURNING id;

-- name: SaveGeneration :exec
INSERT INTO generations (asset_id, kind, source, evidence, tool, tool_version, model_id, prompt_id, negative, seed,
    sampler, scheduler, steps, cfg, gen_width, gen_height, settings, graph, workflow, needs_file, parser_version, updated_at)
VALUES (@asset_id, @kind, @source, @evidence, sqlc.narg(tool), sqlc.narg(tool_version), sqlc.narg(model_id), sqlc.narg(prompt_id),
    sqlc.narg(negative), sqlc.narg(seed), sqlc.narg(sampler), sqlc.narg(scheduler), sqlc.narg(steps), sqlc.narg(cfg),
    sqlc.narg(gen_width), sqlc.narg(gen_height), @settings, sqlc.narg(graph), sqlc.narg(workflow), @needs_file, @parser_version, now())
ON CONFLICT (asset_id) DO UPDATE SET
    kind = EXCLUDED.kind, source = EXCLUDED.source, evidence = EXCLUDED.evidence, tool = EXCLUDED.tool,
    tool_version = EXCLUDED.tool_version, model_id = EXCLUDED.model_id, prompt_id = EXCLUDED.prompt_id,
    negative = EXCLUDED.negative, seed = EXCLUDED.seed, sampler = EXCLUDED.sampler, scheduler = EXCLUDED.scheduler,
    steps = EXCLUDED.steps, cfg = EXCLUDED.cfg, gen_width = EXCLUDED.gen_width, gen_height = EXCLUDED.gen_height,
    settings = EXCLUDED.settings,
    -- a graph or workflow already read in full is kept when a later reading has only the index's cut copy
    graph = coalesce(EXCLUDED.graph, generations.graph), workflow = coalesce(EXCLUDED.workflow, generations.workflow),
    needs_file = EXCLUDED.needs_file, parser_version = EXCLUDED.parser_version, updated_at = now();

-- name: GivenPrompt :one
-- The prompt that came with an item's upload, if one did.
SELECT text FROM given_prompts WHERE asset_id = @asset_id;

-- name: GivePrompt :exec
INSERT INTO given_prompts (asset_id, text) VALUES (@asset_id, @text)
ON CONFLICT (asset_id) DO UPDATE SET text = EXCLUDED.text, updated_at = now();

-- name: GenerationPrompt :one
-- An item's generation and its prompt's hash (NULL: none), to know whether a given prompt may fill it.
SELECT g.prompt_id, p.normalized_hash FROM generations g LEFT JOIN prompts p ON p.id = g.prompt_id WHERE g.asset_id = @asset_id;

-- name: SetGenerationPrompt :exec
UPDATE generations SET prompt_id = @prompt_id, updated_at = now() WHERE asset_id = @asset_id;

-- name: ClearGenerationLoras :exec
DELETE FROM generation_loras WHERE asset_id = @asset_id;

-- name: AddGenerationLora :exec
INSERT INTO generation_loras (asset_id, lora_id, weight) VALUES (@asset_id, @lora_id, sqlc.narg(weight))
ON CONFLICT (asset_id, lora_id) DO UPDATE SET weight = EXCLUDED.weight;

-- name: DeleteGeneration :exec
DELETE FROM generations WHERE asset_id = @asset_id;

-- name: SetProvenance :exec
-- The item's source type follows its generation: AI with one, and back to unknown if an earlier
-- reading had called it AI and this one finds no evidence.
UPDATE assets SET provenance_version = @version, provenance_needs_file = @needs_file,
    source_type = CASE WHEN @ai::text <> '' THEN @ai::text
                       WHEN source_type IN ('ai_generated', 'ai_edited') THEN 'unknown'
                       ELSE source_type END
WHERE id = @id;

-- name: ProvenancePending :many
-- Items the current parser hasn't read yet, with their indexed metadata, a batch at a time.
SELECT a.id, a.owner_id, a.type, m.raw
FROM assets a JOIN asset_metadata m ON m.asset_id = a.id
WHERE (a.provenance_version IS NULL OR a.provenance_version < @version) AND a.id > @after
ORDER BY a.id
LIMIT @lim;

-- name: ProvenanceNeedsFile :many
-- Items whose evidence was cut short in the index, with the file to read, a batch at a time.
SELECT a.id, a.owner_id, a.type, f.rel_path, l.root_path
FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
WHERE a.provenance_needs_file AND a.id > @after
ORDER BY a.id
LIMIT @lim;

-- name: ProvenanceFile :one
SELECT a.id, a.owner_id, a.type, f.rel_path, l.root_path
FROM assets a JOIN files f ON f.id = a.primary_file_id JOIN libraries l ON l.id = f.library_id
WHERE a.id = @id;

-- name: ProvenanceCounts :one
SELECT count(*) FILTER (WHERE provenance_version IS NULL OR provenance_version < @version)::integer AS pending,
       count(*) FILTER (WHERE provenance_needs_file)::integer AS needs_file,
       count(*) FILTER (WHERE source_type = 'ai_generated')::integer AS generated,
       count(*) FILTER (WHERE source_type = 'ai_edited')::integer AS edited
FROM assets;

-- name: SetProvenanceNeedsFile :exec
UPDATE assets SET provenance_needs_file = @needs_file WHERE id = @id;
