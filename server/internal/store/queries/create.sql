-- Reads for the Create views: the prompt library and the model & LoRA catalog. Every query goes
-- through visible_assets(viewer), and an item counts once it's in the timeline.

-- name: PromptList :many
-- Prompts with how often and when they were used: most used first, or most recent.
WITH uses AS (
    SELECT g.prompt_id, count(*)::integer AS uses, min(a.local_date) AS first_date, max(a.local_date) AS last_date
    FROM generations g JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
    WHERE g.prompt_id IS NOT NULL AND a.visibility = 'timeline' AND a.motion_of IS NULL
    GROUP BY g.prompt_id
)
SELECT p.id, p.text, u.uses, u.first_date::date AS first_date, u.last_date::date AS last_date, count(*) OVER ()::integer AS total
FROM uses u JOIN prompts p ON p.id = u.prompt_id
WHERE p.owner_id = @viewer::uuid
  AND (@id::uuid = '00000000-0000-0000-0000-000000000000' OR p.id = @id::uuid)
  AND (@pattern::text = '' OR p.text ILIKE @pattern::text)
ORDER BY CASE WHEN @sort::text = 'recent' THEN u.last_date END DESC NULLS LAST, u.uses DESC, u.last_date DESC, p.id
LIMIT @lim OFFSET @off;

-- name: PromptSamples :many
-- Up to four recent items for each prompt; none that's adult when the viewer hides those.
SELECT prompt_id::uuid AS prompt_id, id, thumbhash FROM (
    SELECT g.prompt_id, a.id, a.thumbhash,
           row_number() OVER (PARTITION BY g.prompt_id ORDER BY a.local_date DESC, a.taken_at DESC, a.id) AS n
    FROM generations g JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
    WHERE g.prompt_id = ANY(@ids::uuid[]) AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
      AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
) x
WHERE n <= 4
ORDER BY prompt_id, n;

-- name: PromptTools :many
-- The models and LoRAs each prompt was used with, most used first.
SELECT x.prompt_id::uuid AS prompt_id, x.kind, x.display, x.uses FROM (
    SELECT g.prompt_id, m.kind, m.display, count(*)::integer AS uses
    FROM generations g JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id JOIN ai_models m ON m.id = g.model_id
    WHERE g.prompt_id = ANY(@ids::uuid[]) AND a.visibility = 'timeline' AND a.motion_of IS NULL
    GROUP BY 1, 2, 3
    UNION ALL
    SELECT g.prompt_id, m.kind, m.display, count(*)::integer
    FROM generations g JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
    JOIN generation_loras gl ON gl.asset_id = g.asset_id JOIN ai_models m ON m.id = gl.lora_id
    WHERE g.prompt_id = ANY(@ids::uuid[]) AND a.visibility = 'timeline' AND a.motion_of IS NULL
    GROUP BY 1, 2, 3
) x
ORDER BY x.prompt_id, x.uses DESC, x.display;

-- name: ModelList :many
-- Models and LoRAs with how often and when they were used, most used first.
WITH uses AS (
    SELECT g.model_id AS model_id, a.local_date
    FROM generations g JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
    WHERE g.model_id IS NOT NULL AND a.visibility = 'timeline' AND a.motion_of IS NULL
    UNION ALL
    SELECT gl.lora_id, a.local_date
    FROM generation_loras gl JOIN visible_assets(@viewer::uuid) a ON a.id = gl.asset_id
    WHERE a.visibility = 'timeline' AND a.motion_of IS NULL
)
SELECT m.id, m.kind, m.display, count(*)::integer AS uses, min(u.local_date)::date AS first_date, max(u.local_date)::date AS last_date
FROM uses u JOIN ai_models m ON m.id = u.model_id
WHERE @id::uuid = '00000000-0000-0000-0000-000000000000' OR m.id = @id::uuid
GROUP BY m.id
ORDER BY uses DESC, m.display;

-- name: ModelSamples :many
-- Up to four recent items for each model or LoRA.
WITH uses AS (
    SELECT g.model_id AS model_id, g.asset_id FROM generations g WHERE g.model_id = ANY(@ids::uuid[])
    UNION ALL
    SELECT gl.lora_id, gl.asset_id FROM generation_loras gl WHERE gl.lora_id = ANY(@ids::uuid[])
)
SELECT model_id::uuid AS model_id, id, thumbhash FROM (
    SELECT u.model_id, a.id, a.thumbhash,
           row_number() OVER (PARTITION BY u.model_id ORDER BY a.local_date DESC, a.taken_at DESC, a.id) AS n
    FROM uses u JOIN visible_assets(@viewer::uuid) a ON a.id = u.asset_id
    WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
) x
WHERE n <= 4
ORDER BY model_id, n;

-- name: ModelPairs :many
-- How often each model and LoRA were used together.
SELECT g.model_id::uuid AS model_id, gl.lora_id, count(*)::integer AS uses
FROM generations g JOIN generation_loras gl ON gl.asset_id = g.asset_id
JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
WHERE g.model_id IS NOT NULL AND a.visibility = 'timeline' AND a.motion_of IS NULL
GROUP BY 1, 2;
