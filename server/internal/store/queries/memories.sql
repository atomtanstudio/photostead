-- Memories, read through library_assets(viewer) (your own items and the Household's). What goes in one is the same everywhere: an item
-- in the timeline (not a Live Photo's clip, or under another in a stack), rendered, not a
-- screenshot, document, receipt, ID card, QR code or adult, not rejected, not AI-made unless the viewer
-- wants those, not in a stretch of time they hid, and with no one in it they keep out of memories.
-- The best first: favorites, stars, picks, named people, a date from the file's own metadata
-- (rather than when it was copied), then a shuffle that changes each day (seed).

-- name: OnThisDay :many
-- Items from these days (this day in earlier years), the best of each year first.
WITH picked AS (
    SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state,
           a.live_motion_id, a.source_type, coalesce(us.favorite, false)::boolean AS favorite, us.rating, us.flag, us.color_label,
           row_number() OVER (PARTITION BY a.local_date ORDER BY
               coalesce(us.favorite, false) DESC, coalesce(us.rating, 0) DESC, (us.flag IS NOT DISTINCT FROM 'pick') DESC,
               EXISTS (SELECT 1 FROM person_items pi JOIN people p ON p.id = pi.person_id
                       WHERE pi.asset_id = a.id AND p.name <> '' AND NOT p.hidden) DESC,
               (a.date_source <> 'file-modified') DESC, md5(a.id::text || @seed::text))::integer AS rank,
           count(*) OVER (PARTITION BY a.local_date)::integer AS on_day
    FROM library_assets(@viewer::uuid) a
    LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
    WHERE a.local_date = ANY(@days::date[]) AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
      AND (@include_ai::boolean OR a.source_type NOT IN ('ai_generated', 'ai_edited'))
      AND us.flag IS DISTINCT FROM 'reject'
      AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                      WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
      AND NOT EXISTS (SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id
                      AND l.label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code', 'adult') AND l.basis IN ('metadata', 'visual', 'confirmed'))
      AND NOT EXISTS (SELECT 1 FROM memory_hidden_days h WHERE h.user_id = @viewer::uuid AND a.local_date BETWEEN h.from_date AND h.to_date)
      AND NOT EXISTS (SELECT 1 FROM person_items pi JOIN people p ON p.id = pi.person_id
                      WHERE pi.asset_id = a.id AND p.owner_id = @viewer::uuid AND NOT p.in_memories)
)
SELECT * FROM picked WHERE rank <= @per_day::integer
ORDER BY local_date DESC, rank;

-- name: MemoryPeople :many
-- Named people and pets who can be remembered: shown, not kept out of memories, in at least six
-- items over at least two months.
SELECT p.id, p.name, p.species, count(DISTINCT a.id)::integer AS items,
       min(a.local_date)::date AS first_day, max(a.local_date)::date AS last_day
FROM people p
JOIN person_items pi ON pi.person_id = p.id
JOIN library_assets(@viewer::uuid) a ON a.id = pi.asset_id
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE p.owner_id = @viewer::uuid AND p.name <> '' AND NOT p.hidden AND p.in_memories
  AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
  AND (@include_ai::boolean OR a.source_type NOT IN ('ai_generated', 'ai_edited'))
  AND us.flag IS DISTINCT FROM 'reject'
  AND NOT EXISTS (SELECT 1 FROM memory_hidden_days h WHERE h.user_id = @viewer::uuid AND a.local_date BETWEEN h.from_date AND h.to_date)
GROUP BY p.id, p.name, p.species
HAVING count(DISTINCT a.id) >= 6 AND count(DISTINCT date_trunc('month', a.local_date)) >= 2
ORDER BY p.id;

-- name: PersonMemory :many
-- A person's best items from each month they're in (up to two), oldest first.
WITH picked AS (
    SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state,
           a.live_motion_id, a.source_type, coalesce(us.favorite, false)::boolean AS favorite, us.rating, us.flag, us.color_label,
           row_number() OVER (PARTITION BY date_trunc('month', a.local_date) ORDER BY
               coalesce(us.favorite, false) DESC, coalesce(us.rating, 0) DESC, (us.flag IS NOT DISTINCT FROM 'pick') DESC,
               (a.date_source <> 'file-modified') DESC, md5(a.id::text || @seed::text))::integer AS rank
    FROM person_items pi
    JOIN library_assets(@viewer::uuid) a ON a.id = pi.asset_id
    LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
    WHERE pi.person_id = @person_id::uuid AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
      AND (@include_ai::boolean OR a.source_type NOT IN ('ai_generated', 'ai_edited'))
      AND us.flag IS DISTINCT FROM 'reject'
      AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                      WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
      AND NOT EXISTS (SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id
                      AND l.label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code', 'adult') AND l.basis IN ('metadata', 'visual', 'confirmed'))
      AND NOT EXISTS (SELECT 1 FROM memory_hidden_days h WHERE h.user_id = @viewer::uuid AND a.local_date BETWEEN h.from_date AND h.to_date)
      AND NOT EXISTS (SELECT 1 FROM person_items o JOIN people p ON p.id = o.person_id
                      WHERE o.asset_id = a.id AND p.owner_id = @viewer::uuid AND NOT p.in_memories)
)
SELECT * FROM picked WHERE rank <= 2
ORDER BY local_date, taken_at;

-- name: MemoryHiddenDays :many
SELECT id, from_date, to_date FROM memory_hidden_days WHERE user_id = @viewer::uuid ORDER BY from_date DESC;

-- name: HideMemoryDays :one
INSERT INTO memory_hidden_days (user_id, from_date, to_date) VALUES (@viewer::uuid, @from_date, @to_date) RETURNING id;

-- name: UnhideMemoryDays :execrows
DELETE FROM memory_hidden_days WHERE id = @id AND user_id = @viewer::uuid;

-- name: PeopleKeptOutOfMemories :many
SELECT id, name, species FROM people WHERE owner_id = @viewer::uuid AND NOT in_memories ORDER BY name;

-- name: SetPersonInMemories :execrows
UPDATE people SET in_memories = @in_memories, updated_at = now() WHERE id = @id AND owner_id = @owner_id;
