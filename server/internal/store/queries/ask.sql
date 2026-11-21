-- Ask: plain-language questions, planned into filters (internal/ask) and run here, through the gate.
--
-- Every filter is optional (an empty value turns it off) and written as an uncorrelated
-- a.id IN (subquery), which Postgres works out once and hashes, so an unused filter costs nothing
-- and a used one reads its own index once rather than once per item.

-- name: AskVocabulary :many
-- Names a question can mention: people, characters, and the models and LoRAs in the viewer's items.
SELECT CAST('person' AS text) AS kind, p.id, p.name FROM people p WHERE p.owner_id = @viewer::uuid AND NOT p.hidden AND p.name <> '' AND p.character_id IS NULL
UNION ALL
SELECT CAST('character' AS text), c.id, c.name FROM characters c WHERE c.owner_id = @viewer::uuid
UNION ALL
SELECT m.kind, m.id, m.name FROM ai_models m
WHERE m.id IN (SELECT g.model_id FROM generations g JOIN visible_assets(@viewer::uuid) v ON v.id = g.asset_id WHERE g.model_id IS NOT NULL)
   OR m.id IN (SELECT gl.lora_id FROM generation_loras gl JOIN visible_assets(@viewer::uuid) v ON v.id = gl.asset_id);

-- name: AskItems :many
-- The listed items' favorite flags are looked up after the limit, not for every match.
SELECT x.id, x.type, x.taken_at, x.local_date, x.width, x.height, x.duration_ms, x.thumbhash, x.render_state, x.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = x.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       x.source_type, x.total
FROM (
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       a.source_type, count(*) OVER ()::integer AS total
FROM visible_assets(@viewer::uuid) a
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL
  AND (@type::text = '' OR a.type = @type::text)
  AND (sqlc.narg('after')::date IS NULL OR a.local_date >= sqlc.narg('after')::date)
  AND (sqlc.narg('before')::date IS NULL OR a.local_date < sqlc.narg('before')::date)
  AND (NOT @ai_only::boolean OR a.source_type IN ('ai_generated', 'ai_edited'))
  AND (NOT @favorite::boolean OR a.id IN (SELECT s.asset_id FROM asset_user_state s WHERE s.user_id = @viewer::uuid AND s.favorite))
  AND (cardinality(@people::uuid[]) = 0 OR a.id IN (
        SELECT pi.asset_id FROM person_items pi JOIN people p ON p.id = pi.person_id
        WHERE pi.person_id = ANY(@people::uuid[]) AND p.owner_id = @viewer::uuid
        GROUP BY pi.asset_id HAVING count(DISTINCT pi.person_id) = cardinality(@people::uuid[])))
  AND (@character::uuid = '00000000-0000-0000-0000-000000000000' OR a.id IN (
        SELECT ca.asset_id FROM character_assets ca JOIN characters c ON c.id = ca.character_id
        WHERE ca.character_id = @character::uuid AND c.owner_id = @viewer::uuid AND ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed')))
  AND (cardinality(@models::uuid[]) = 0 OR a.id IN (SELECT g.asset_id FROM generations g WHERE g.model_id = ANY(@models::uuid[]))
        OR a.id IN (SELECT gl.asset_id FROM generation_loras gl WHERE gl.lora_id = ANY(@models::uuid[])))
  AND (@source::text = '' OR a.id IN (SELECT g.asset_id FROM generations g WHERE g.source = @source::text OR g.tool ILIKE '%' || @source::text || '%'))
  AND (@prompt::text = '' OR a.id IN (
        SELECT g.asset_id FROM prompts p JOIN generations g ON g.prompt_id = p.id
        WHERE p.owner_id = @viewer::uuid AND to_tsvector('english', p.text) @@ websearch_to_tsquery('english', @prompt::text)))
  AND (@ocr::text = '' OR a.id IN (
        SELECT o.asset_id FROM asset_ocr o WHERE o.text <> '' AND to_tsvector('simple', o.text) @@ websearch_to_tsquery('simple', @ocr::text)))
  AND (@filename::text = '' OR a.id IN (SELECT f.asset_id FROM files f WHERE f.rel_path ILIKE @filename::text))
  AND (@label::text = '' OR a.id IN (
        SELECT l.asset_id FROM asset_labels l WHERE l.label = @label::text AND l.basis IN ('metadata', 'visual', 'confirmed')))
ORDER BY CASE WHEN @oldest_first::boolean THEN a.local_date END, CASE WHEN @oldest_first::boolean THEN a.taken_at END,
         a.local_date DESC, a.taken_at DESC, a.id DESC
LIMIT @lim
) x
ORDER BY CASE WHEN @oldest_first::boolean THEN x.local_date END, CASE WHEN @oldest_first::boolean THEN x.taken_at END,
         x.local_date DESC, x.taken_at DESC, x.id DESC;

-- name: AskRank :many
-- Of the items a question's filters found, the ones that look most like its description.
SELECT e.asset_id FROM asset_embeddings e
WHERE e.model = @model::text AND e.asset_id = ANY(@ids::uuid[])
ORDER BY e.embedding <=> CAST(@vec::text AS halfvec(768))
LIMIT @lim;
