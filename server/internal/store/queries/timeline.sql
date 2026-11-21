-- Reads for a viewer. Every query here goes through the gate: library_assets(viewer) for the
-- timeline (your own items and the Household's), visible_assets(viewer) for everything you may see
-- (albums shared with you too).

-- An item shows on its own in the timeline unless it's a Live Photo's motion clip or a
-- non-cover member of a confirmed stack.

-- The timeline (and a smart album, which is the timeline narrowed by its rules) can be narrowed
-- by the viewer's marks, favorites, people (all of them), keywords (any), a date range, where
-- items came from (made with AI, not, or by a camera), and whose library (the viewer's own, or the
-- Household's). Each filter is off when empty.

-- name: TimelineBuckets :many
SELECT to_char(a.local_date, 'YYYY-MM')::text AS month, count(*)::integer AS count
FROM library_assets(@viewer::uuid) a
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
  AND (cardinality(@hide::text[]) = 0 OR NOT EXISTS (
        SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id AND l.label = ANY(@hide::text[]) AND l.basis IN ('metadata', 'visual', 'confirmed')))
  AND coalesce(us.rating, 0) >= @min_rating::integer
  AND (@flag::text = '' OR (@flag::text = 'unrejected' AND us.flag IS DISTINCT FROM 'reject') OR us.flag = @flag::text)
  AND (@color::text = '' OR us.color_label = @color::text)
  AND (NOT @favorite::boolean OR coalesce(us.favorite, false))
  AND (coalesce(cardinality(@people::uuid[]), 0) = 0 OR a.id IN (
        SELECT pi.asset_id FROM person_items pi JOIN people p ON p.id = pi.person_id
        WHERE pi.person_id = ANY(@people::uuid[]) AND p.owner_id = @viewer::uuid
        GROUP BY pi.asset_id HAVING count(DISTINCT pi.person_id) = cardinality(@people::uuid[])))
  AND (coalesce(cardinality(@keywords::text[]), 0) = 0 OR a.id IN (
        SELECT ak.asset_id FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id
        WHERE k.owner_id = @viewer::uuid AND lower(k.name) = ANY(@keywords::text[])))
  AND (sqlc.narg('after')::date IS NULL OR a.local_date >= sqlc.narg('after')::date)
  AND (@library::text = '' OR (@library::text = 'mine' AND a.owner_id = @viewer::uuid)
       OR (@library::text = 'household' AND EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id)))
  AND (sqlc.narg('before')::date IS NULL OR a.local_date <= sqlc.narg('before')::date)
  AND (@source::text = ''
       OR (@source::text = 'ai' AND a.source_type IN ('ai_generated', 'ai_edited'))
       OR (@source::text = 'not_ai' AND a.source_type NOT IN ('ai_generated', 'ai_edited'))
       OR (@source::text = 'camera' AND a.source_type NOT IN ('ai_generated', 'ai_edited', 'screenshot')
           AND EXISTS (SELECT 1 FROM asset_metadata md WHERE md.asset_id = a.id AND (md.make IS NOT NULL OR md.model IS NOT NULL))))
GROUP BY 1
ORDER BY 1 DESC;

-- name: TimelineRange :many
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       coalesce(us.favorite, false)::boolean AS favorite, a.source_type, us.rating, us.flag, us.color_label,
       EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id)::boolean AS shared, (a.owner_id <> @viewer::uuid)::boolean AS from_others
FROM library_assets(@viewer::uuid) a
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND a.local_date >= @from_date::date AND a.local_date < @to_date::date
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
  AND (cardinality(@hide::text[]) = 0 OR NOT EXISTS (
        SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id AND l.label = ANY(@hide::text[]) AND l.basis IN ('metadata', 'visual', 'confirmed')))
  AND coalesce(us.rating, 0) >= @min_rating::integer
  AND (@flag::text = '' OR (@flag::text = 'unrejected' AND us.flag IS DISTINCT FROM 'reject') OR us.flag = @flag::text)
  AND (@color::text = '' OR us.color_label = @color::text)
  AND (NOT @favorite::boolean OR coalesce(us.favorite, false))
  AND (coalesce(cardinality(@people::uuid[]), 0) = 0 OR a.id IN (
        SELECT pi.asset_id FROM person_items pi JOIN people p ON p.id = pi.person_id
        WHERE pi.person_id = ANY(@people::uuid[]) AND p.owner_id = @viewer::uuid
        GROUP BY pi.asset_id HAVING count(DISTINCT pi.person_id) = cardinality(@people::uuid[])))
  AND (coalesce(cardinality(@keywords::text[]), 0) = 0 OR a.id IN (
        SELECT ak.asset_id FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id
        WHERE k.owner_id = @viewer::uuid AND lower(k.name) = ANY(@keywords::text[])))
  AND (sqlc.narg('after')::date IS NULL OR a.local_date >= sqlc.narg('after')::date)
  AND (@library::text = '' OR (@library::text = 'mine' AND a.owner_id = @viewer::uuid)
       OR (@library::text = 'household' AND EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id)))
  AND (sqlc.narg('before')::date IS NULL OR a.local_date <= sqlc.narg('before')::date)
  AND (@source::text = ''
       OR (@source::text = 'ai' AND a.source_type IN ('ai_generated', 'ai_edited'))
       OR (@source::text = 'not_ai' AND a.source_type NOT IN ('ai_generated', 'ai_edited'))
       OR (@source::text = 'camera' AND a.source_type NOT IN ('ai_generated', 'ai_edited', 'screenshot')
           AND EXISTS (SELECT 1 FROM asset_metadata md WHERE md.asset_id = a.id AND (md.make IS NOT NULL OR md.model IS NOT NULL))))
ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC;

-- name: GetVisibleAsset :one
-- One item the viewer can see, or one in their own trash (so the trash can be looked through), or
-- in their vault while it's unlocked (vault_viewer is the viewer only then).
SELECT a.id, a.type, a.mime, a.taken_at, a.taken_offset_min, a.local_date, a.date_source,
       a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.render_error, a.source_type,
       f.rel_path, f.size AS file_size, l.root_path, l.name AS library_name,
       m.make, m.model, m.lens, m.focal_mm, m.fnumber, m.exposure_s, m.iso, m.lat, m.lon,
       m.title, m.description, a.visibility, a.trashed_at,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       a.owner_id, coalesce(nullif(ow.display_name, ''), ow.email)::text AS owner_name,
       EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id) AS shared
FROM (SELECT * FROM visible_assets(@viewer::uuid) UNION ALL SELECT * FROM trashed_assets(@viewer::uuid)
      UNION ALL SELECT * FROM vault_assets(@vault_viewer::uuid)) a
JOIN users ow ON ow.id = a.owner_id
LEFT JOIN files f ON f.id = a.primary_file_id
LEFT JOIN libraries l ON l.id = f.library_id
LEFT JOIN asset_metadata m ON m.asset_id = a.id
WHERE a.id = @id::uuid;

-- name: SearchByName :many
-- Items whose file or folder names contain every word (case-insensitive), newest first.
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       a.source_type
FROM visible_assets(@viewer::uuid) a
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND EXISTS (SELECT 1 FROM files f WHERE f.asset_id = a.id AND f.state = 'present' AND f.rel_path ILIKE ALL (@patterns::text[]))
ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC
LIMIT @lim;

-- name: DownloadSources :many
SELECT a.id, f.rel_path, f.mtime, l.root_path
FROM visible_assets(@viewer::uuid) a
JOIN files f ON f.id = a.primary_file_id
JOIN libraries l ON l.id = f.library_id
WHERE a.id = ANY(@ids::uuid[]);

-- name: GenerationForAsset :one
-- How an item was made, if there's evidence. The item goes through the gate.
SELECT g.kind, g.source, g.evidence, g.tool, g.tool_version, g.negative, g.seed, g.sampler, g.scheduler, g.steps, g.cfg,
       g.model_id, g.prompt_id,
       g.gen_width, g.gen_height, g.settings, g.needs_file,
       (g.workflow IS NOT NULL OR g.graph IS NOT NULL)::boolean AS has_workflow,
       m.display AS model, p.text AS prompt,
       (SELECT count(*) FROM generations g2 JOIN visible_assets(@viewer::uuid) a2 ON a2.id = g2.asset_id
        WHERE g2.prompt_id = g.prompt_id)::integer AS prompt_uses
FROM visible_assets(@viewer::uuid) a
JOIN generations g ON g.asset_id = a.id
LEFT JOIN ai_models m ON m.id = g.model_id
LEFT JOIN prompts p ON p.id = g.prompt_id
WHERE a.id = @id::uuid;

-- name: GenerationLoras :many
SELECT m.id, m.display, gl.weight FROM generation_loras gl JOIN ai_models m ON m.id = gl.lora_id
WHERE gl.asset_id = @asset_id ORDER BY m.display;

-- name: GenerationStart :one
-- The image a clip was made from. Both go through the gate, so a start image the viewer can't see
-- is never shown.
SELECT s.id, s.thumbhash
FROM visible_assets(@viewer::uuid) v
JOIN generation_starts gs ON gs.video_id = v.id
JOIN visible_assets(@viewer::uuid) s ON s.id = gs.image_id
WHERE v.id = @id::uuid;

-- name: GenerationWorkflow :one
-- The workflow (or, without one, the API graph) to download. The item goes through the gate.
SELECT coalesce(g.workflow, g.graph)::text AS json, f.rel_path
FROM visible_assets(@viewer::uuid) a
JOIN generations g ON g.asset_id = a.id
LEFT JOIN files f ON f.id = a.primary_file_id
WHERE a.id = @id::uuid AND (g.workflow IS NOT NULL OR g.graph IS NOT NULL);

-- name: SearchByPrompt :many
-- Items whose prompt contains the query's words (English stemming), best match first.
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       a.source_type
FROM prompts p
JOIN generations g ON g.prompt_id = p.id
JOIN visible_assets(@viewer::uuid) a ON a.id = g.asset_id
WHERE p.owner_id = @viewer::uuid AND a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND to_tsvector('english', p.text) @@ websearch_to_tsquery('english', @q::text)
ORDER BY ts_rank_cd(to_tsvector('english', p.text), websearch_to_tsquery('english', @q::text)) DESC, a.local_date DESC, a.id
LIMIT @lim;

-- name: SearchByText :many
-- Items with the query's words in the text read from them, best match first.
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       a.source_type
FROM asset_ocr o
JOIN visible_assets(@viewer::uuid) a ON a.id = o.asset_id
WHERE o.text <> '' AND a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND to_tsvector('simple', o.text) @@ websearch_to_tsquery('simple', @q::text)
ORDER BY ts_rank_cd(to_tsvector('simple', o.text), websearch_to_tsquery('simple', @q::text)) DESC, a.local_date DESC, a.id
LIMIT @lim;

-- name: AssetText :one
-- The text read from an item. The caller has already checked the item through the gate.
SELECT text FROM asset_ocr WHERE asset_id = @asset_id;

-- name: AssetLabels :many
-- The utility labels shown on an item (the caller has already checked it through the gate).
SELECT label, basis FROM asset_labels WHERE asset_id = @asset_id AND basis IN ('metadata', 'visual', 'confirmed') ORDER BY label;

-- name: AssetDescription :one
-- A description from outside the file (the caller has already checked the item through the gate).
SELECT text, source FROM asset_descriptions WHERE asset_id = @asset_id;

-- name: AssetKeywords :many
SELECT k.name FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id WHERE ak.asset_id = @asset_id ORDER BY k.name;

-- name: SearchByDetails :many
-- Items whose description or keywords have the query's words, best match first.
WITH hits AS (
    SELECT d.asset_id, ts_rank_cd(to_tsvector('simple', d.text), websearch_to_tsquery('simple', @q::text))::real AS rank
    FROM asset_descriptions d
    WHERE to_tsvector('simple', d.text) @@ websearch_to_tsquery('simple', @q::text)
    UNION ALL
    SELECT ak.asset_id, 1::real
    FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id
    WHERE k.owner_id = @viewer::uuid AND to_tsvector('simple', replace(k.name, '/', ' ')) @@ websearch_to_tsquery('simple', @q::text)
),
best AS (SELECT asset_id, max(rank) AS rank FROM hits GROUP BY asset_id)
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.live_motion_id,
       EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite,
       a.source_type
FROM best b JOIN visible_assets(@viewer::uuid) a ON a.id = b.asset_id
WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
ORDER BY b.rank DESC, a.local_date DESC, a.id
LIMIT @lim;

-- name: RecentlyAdded :many
-- What the timeline shows, newest-arrived first (by when the item came into Photostead), a page at a
-- time: the ones that arrived before (before_at, before_id), when given. The page is picked first
-- and only its items filled in (stars, stacks, sharing): 70 ms for a 100,000-item library, not 1.7 s.
WITH picked AS (
    SELECT a.id, a.created_at FROM library_assets(@viewer::uuid) a
    WHERE a.visibility = 'timeline' AND a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
      AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                      WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
      AND (sqlc.narg('before_at')::timestamptz IS NULL OR (a.created_at, a.id) < (sqlc.narg('before_at')::timestamptz, @before_id::uuid))
    ORDER BY a.created_at DESC, a.id DESC
    LIMIT @lim::integer
)
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id, p.created_at AS added_at,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       coalesce(us.favorite, false)::boolean AS favorite, a.source_type, us.rating, us.flag, us.color_label,
       EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id)::boolean AS shared, (a.owner_id <> @viewer::uuid)::boolean AS from_others
FROM picked p JOIN assets a ON a.id = p.id -- gated: picked came through library_assets(viewer)
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
ORDER BY p.created_at DESC, p.id DESC;
