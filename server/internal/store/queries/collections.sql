-- Reads of a viewer's collections: favorites, the archive, albums, people, and the utility
-- collections (screenshots and the like, adult and suggestive, AI-generated, captured by a camera).
-- hide_adult leaves adult items out (a viewer's "Adult content: hidden"). Every query here
-- goes through visible_assets(viewer).
--
-- A collection shows exactly the items in it, stack members included, but never a Live Photo's
-- motion clip on its own. Favorites, people, prompts, models and LoRAs show what's in the
-- timeline, the archive shows archived items, and an album shows both.
--
-- The kind picks one branch of the members list; Postgres skips the others outright (a one-time
-- filter on a parameter), so each collection is read from its own index.

-- name: CollectionBuckets :many
WITH members AS (
    SELECT s.asset_id FROM asset_user_state s WHERE @kind::text = 'favorites' AND s.user_id = @viewer::uuid AND s.favorite
    UNION ALL
    SELECT aa.asset_id FROM album_assets aa JOIN albums al ON al.id = aa.album_id
    WHERE @kind::text = 'album' AND aa.album_id = @ref::uuid
      AND (al.owner_id = @viewer::uuid OR EXISTS (SELECT 1 FROM album_members am WHERE am.album_id = al.id AND am.user_id = @viewer::uuid))
    UNION ALL
    SELECT pi.asset_id FROM person_items pi JOIN people p ON p.id = pi.person_id
    WHERE @kind::text = 'person' AND pi.person_id = @ref::uuid AND p.owner_id = @viewer::uuid
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x WHERE @kind::text = 'archive' AND x.visibility = 'archived'
    UNION ALL
    SELECT g.asset_id FROM generations g JOIN prompts p ON p.id = g.prompt_id
    WHERE @kind::text = 'prompt' AND g.prompt_id = @ref::uuid AND p.owner_id = @viewer::uuid
    UNION ALL
    SELECT g.asset_id FROM generations g WHERE @kind::text = 'model' AND g.model_id = @ref::uuid
    UNION ALL
    SELECT gl.asset_id FROM generation_loras gl WHERE @kind::text = 'lora' AND gl.lora_id = @ref::uuid
    UNION ALL
    SELECT ca.asset_id FROM character_assets ca JOIN characters c ON c.id = ca.character_id
    WHERE @kind::text = 'character' AND ca.character_id = @ref::uuid AND c.owner_id = @viewer::uuid
      AND ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed')
    UNION ALL
    SELECT l.asset_id FROM asset_labels l
    WHERE @kind::text IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code', 'adult', 'suggestive') AND l.label = @kind::text
      AND l.basis IN ('metadata', 'visual', 'confirmed')
    UNION ALL
    SELECT m.asset_id FROM asset_metadata m WHERE @kind::text = 'place' AND m.place_id = @ref::uuid
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x WHERE @kind::text = 'ai' AND x.source_type IN ('ai_generated', 'ai_edited')
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x JOIN asset_metadata m ON m.asset_id = x.id
    WHERE @kind::text = 'captured' AND (m.make IS NOT NULL OR m.model IS NOT NULL)
      AND x.source_type NOT IN ('ai_generated', 'ai_edited', 'screenshot')
    UNION ALL
    SELECT DISTINCT f.asset_id FROM files f JOIN libraries lb ON lb.id = f.library_id
    WHERE @kind::text = 'folder' AND f.library_id = @ref::uuid AND lb.owner_id = @viewer::uuid AND f.state = 'present'
      AND f.asset_id IS NOT NULL AND left(f.rel_path, length(@folder::text)) = @folder::text
      AND strpos(substr(f.rel_path, length(@folder::text) + 1), '/') = 0
)
SELECT to_char(a.local_date, 'YYYY-MM')::text AS month, count(*)::integer AS count
FROM members m JOIN visible_assets(@viewer::uuid) a ON a.id = m.asset_id
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND a.visibility = ANY (CASE WHEN @kind::text IN ('album', 'folder') THEN ARRAY['timeline', 'archived']
                                           WHEN @kind::text = 'archive' THEN ARRAY['archived'] ELSE ARRAY['timeline'] END)
  AND coalesce(us.rating, 0) >= @min_rating::integer
  AND (@flag::text = '' OR (@flag::text = 'unrejected' AND us.flag IS DISTINCT FROM 'reject') OR us.flag = @flag::text)
  AND (@color::text = '' OR us.color_label = @color::text)
  AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
GROUP BY 1
ORDER BY 1 DESC;

-- name: CollectionRange :many
WITH members AS (
    SELECT s.asset_id FROM asset_user_state s WHERE @kind::text = 'favorites' AND s.user_id = @viewer::uuid AND s.favorite
    UNION ALL
    SELECT aa.asset_id FROM album_assets aa JOIN albums al ON al.id = aa.album_id
    WHERE @kind::text = 'album' AND aa.album_id = @ref::uuid
      AND (al.owner_id = @viewer::uuid OR EXISTS (SELECT 1 FROM album_members am WHERE am.album_id = al.id AND am.user_id = @viewer::uuid))
    UNION ALL
    SELECT pi.asset_id FROM person_items pi JOIN people p ON p.id = pi.person_id
    WHERE @kind::text = 'person' AND pi.person_id = @ref::uuid AND p.owner_id = @viewer::uuid
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x WHERE @kind::text = 'archive' AND x.visibility = 'archived'
    UNION ALL
    SELECT g.asset_id FROM generations g JOIN prompts p ON p.id = g.prompt_id
    WHERE @kind::text = 'prompt' AND g.prompt_id = @ref::uuid AND p.owner_id = @viewer::uuid
    UNION ALL
    SELECT g.asset_id FROM generations g WHERE @kind::text = 'model' AND g.model_id = @ref::uuid
    UNION ALL
    SELECT gl.asset_id FROM generation_loras gl WHERE @kind::text = 'lora' AND gl.lora_id = @ref::uuid
    UNION ALL
    SELECT ca.asset_id FROM character_assets ca JOIN characters c ON c.id = ca.character_id
    WHERE @kind::text = 'character' AND ca.character_id = @ref::uuid AND c.owner_id = @viewer::uuid
      AND ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed')
    UNION ALL
    SELECT l.asset_id FROM asset_labels l
    WHERE @kind::text IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code', 'adult', 'suggestive') AND l.label = @kind::text
      AND l.basis IN ('metadata', 'visual', 'confirmed')
    UNION ALL
    SELECT m.asset_id FROM asset_metadata m WHERE @kind::text = 'place' AND m.place_id = @ref::uuid
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x WHERE @kind::text = 'ai' AND x.source_type IN ('ai_generated', 'ai_edited')
    UNION ALL
    SELECT x.id FROM visible_assets(@viewer::uuid) x JOIN asset_metadata m ON m.asset_id = x.id
    WHERE @kind::text = 'captured' AND (m.make IS NOT NULL OR m.model IS NOT NULL)
      AND x.source_type NOT IN ('ai_generated', 'ai_edited', 'screenshot')
    UNION ALL
    SELECT DISTINCT f.asset_id FROM files f JOIN libraries lb ON lb.id = f.library_id
    WHERE @kind::text = 'folder' AND f.library_id = @ref::uuid AND lb.owner_id = @viewer::uuid AND f.state = 'present'
      AND f.asset_id IS NOT NULL AND left(f.rel_path, length(@folder::text)) = @folder::text
      AND strpos(substr(f.rel_path, length(@folder::text) + 1), '/') = 0
)
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       coalesce(us.favorite, false)::boolean AS favorite, a.source_type, us.rating, us.flag, us.color_label,
       EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = a.id)::boolean AS shared, (a.owner_id <> @viewer::uuid)::boolean AS from_others
FROM members m JOIN visible_assets(@viewer::uuid) a ON a.id = m.asset_id
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND a.visibility = ANY (CASE WHEN @kind::text IN ('album', 'folder') THEN ARRAY['timeline', 'archived']
                                           WHEN @kind::text = 'archive' THEN ARRAY['archived'] ELSE ARRAY['timeline'] END)
  AND a.local_date >= @from_date::date AND a.local_date < @to_date::date
  AND coalesce(us.rating, 0) >= @min_rating::integer
  AND (@flag::text = '' OR (@flag::text = 'unrejected' AND us.flag IS DISTINCT FROM 'reject') OR us.flag = @flag::text)
  AND (@color::text = '' OR us.color_label = @color::text)
  AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC;

-- name: LibraryCounts :one
-- The rail's counts. "all", "image" and "video" follow the timeline's rules (no motion clips, only
-- the top of each stack).
SELECT
    count(*) FILTER (WHERE a.visibility = 'timeline' AND NOT a.stacked)::integer AS all_items,
    count(*) FILTER (WHERE a.visibility = 'timeline' AND NOT a.stacked AND a.type = 'image')::integer AS images,
    count(*) FILTER (WHERE a.visibility = 'timeline' AND NOT a.stacked AND a.type = 'video')::integer AS videos,
    count(*) FILTER (WHERE a.visibility = 'timeline' AND a.favorite)::integer AS favorites,
    count(*) FILTER (WHERE a.visibility = 'archived')::integer AS archived,
    (SELECT count(*) FROM albums al WHERE al.owner_id = @viewer::uuid)::integer AS albums,
    (SELECT count(*) FROM people p WHERE p.owner_id = @viewer::uuid AND NOT p.hidden AND p.character_id IS NULL
       AND EXISTS (SELECT 1 FROM person_items pi WHERE pi.person_id = p.id))::integer AS people,
    (SELECT count(DISTINCT g.prompt_id) FROM generations g JOIN visible_assets(@viewer::uuid) v ON v.id = g.asset_id
     WHERE g.prompt_id IS NOT NULL AND v.visibility = 'timeline' AND v.motion_of IS NULL)::integer AS prompts,
    (SELECT count(*) FROM (
        SELECT g.model_id FROM generations g JOIN visible_assets(@viewer::uuid) v ON v.id = g.asset_id
        WHERE g.model_id IS NOT NULL AND v.visibility = 'timeline' AND v.motion_of IS NULL
        UNION
        SELECT gl.lora_id FROM generation_loras gl JOIN visible_assets(@viewer::uuid) v ON v.id = gl.asset_id
        WHERE v.visibility = 'timeline' AND v.motion_of IS NULL) mm)::integer AS models,
    (SELECT count(*) FROM characters c WHERE c.owner_id = @viewer::uuid)::integer AS characters,
    (SELECT count(*) FROM trashed_assets(@viewer::uuid) t WHERE t.motion_of IS NULL)::integer AS trash,
    count(*) FILTER (WHERE a.visibility = 'timeline' AND a.source_type IN ('ai_generated', 'ai_edited'))::integer AS ai,
    (SELECT count(*) FROM asset_metadata m JOIN visible_assets(@viewer::uuid) v ON v.id = m.asset_id
     WHERE (m.make IS NOT NULL OR m.model IS NOT NULL) AND v.visibility = 'timeline' AND v.motion_of IS NULL
       AND v.source_type NOT IN ('ai_generated', 'ai_edited', 'screenshot'))::integer AS captured,
    (SELECT coalesce(jsonb_object_agg(x.label, x.n), '{}')::jsonb FROM (
        SELECT l.label, count(*) AS n FROM asset_labels l JOIN visible_assets(@viewer::uuid) v ON v.id = l.asset_id
        WHERE l.basis IN ('metadata', 'visual', 'confirmed') AND v.visibility = 'timeline' AND v.motion_of IS NULL
        GROUP BY 1) x) AS labels
FROM (
    SELECT a.visibility, a.type, a.source_type,
           EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                   WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id) AS stacked,
           EXISTS (SELECT 1 FROM asset_user_state s WHERE s.asset_id = a.id AND s.user_id = @viewer::uuid AND s.favorite) AS favorite
    FROM library_assets(@viewer::uuid) a
    WHERE a.motion_of IS NULL AND a.visibility IN ('timeline', 'archived')
    OFFSET 0 -- keeps this subquery whole, so "stacked" is worked out once per item, not once per count using it
) a;

-- name: ListAlbums :many
-- Albums with their counts, dates and a cover (the chosen one, else the newest item), newest first:
-- one album by id, every album (all), or the albums in one (parent; none for the top level). A smart
-- album's items come from its rules, worked out by the caller.
-- Counted for every album in one pass (st, cv) rather than once per album: each pass reads the
-- viewer's whole library, so per album it took a second or more on a library of 100,000.
WITH st AS (
    SELECT aa.album_id, count(*)::integer AS item_count, min(a.local_date)::date AS first_date, max(a.local_date)::date AS last_date
    FROM album_assets aa JOIN visible_assets(@viewer::uuid) a ON a.id = aa.asset_id
    WHERE a.motion_of IS NULL AND a.visibility IN ('timeline', 'archived')
    GROUP BY aa.album_id
), cv AS (
    SELECT DISTINCT ON (aa.album_id) aa.album_id, a.id, a.thumbhash
    FROM album_assets aa JOIN albums ca ON ca.id = aa.album_id JOIN visible_assets(@viewer::uuid) a ON a.id = aa.asset_id
    WHERE a.motion_of IS NULL AND a.visibility IN ('timeline', 'archived') AND a.render_state = 'done'
    ORDER BY aa.album_id, (a.id = ca.cover_asset_id) DESC, a.local_date DESC, a.taken_at DESC
)
SELECT al.id, al.title, al.description, al.created_at, al.updated_at, al.parent_id, al.kind, al.query,
       (SELECT count(*) FROM albums c WHERE c.parent_id = al.id)::integer AS children,
       al.owner_id, (SELECT coalesce(nullif(u.display_name, ''), u.username) FROM users u WHERE u.id = al.owner_id)::text AS owner_name,
       coalesce((SELECT am.role FROM album_members am WHERE am.album_id = al.id AND am.user_id = @viewer::uuid), '')::text AS my_role,
       (SELECT count(*) FROM album_members am WHERE am.album_id = al.id)::integer AS members,
       coalesce(st.item_count, 0)::integer AS item_count, st.first_date, st.last_date,
       coalesce(cv.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS cover_id, cv.thumbhash AS cover_thumbhash
FROM albums al
LEFT JOIN st ON st.album_id = al.id
LEFT JOIN cv ON cv.album_id = al.id
WHERE (al.owner_id = @viewer::uuid OR EXISTS (SELECT 1 FROM album_members am WHERE am.album_id = al.id AND am.user_id = @viewer::uuid))
  AND CASE WHEN @id::uuid <> '00000000-0000-0000-0000-000000000000' THEN al.id = @id::uuid
           WHEN @everything::boolean THEN true
           WHEN @parent::uuid = '00000000-0000-0000-0000-000000000000' THEN al.parent_id IS NULL OR al.owner_id <> @viewer::uuid -- shared with you: at the top
           ELSE al.parent_id = @parent::uuid AND al.owner_id = @viewer::uuid END
ORDER BY st.last_date DESC NULLS LAST, al.title;

-- name: AlbumPath :many
-- An album's ancestors, outermost first (for its heading and the picker).
WITH RECURSIVE up AS (
    SELECT al.id, al.parent_id, al.title, 0 AS depth FROM albums al WHERE al.id = @id::uuid AND al.owner_id = @viewer::uuid
    UNION ALL
    SELECT p.id, p.parent_id, p.title, up.depth + 1 FROM albums p JOIN up ON p.id = up.parent_id
)
SELECT id, title FROM up WHERE depth > 0 ORDER BY depth DESC;

-- name: ListFolders :many
-- The folders directly inside one of the viewer's libraries' folders ("" for its top), with how
-- many photos and videos are under each.
SELECT split_part(substr(f.rel_path, length(@folder::text) + 1), '/', 1)::text AS name, count(*)::integer AS files
FROM files f JOIN libraries lb ON lb.id = f.library_id
WHERE f.library_id = @library_id::uuid AND lb.owner_id = @viewer::uuid AND f.state = 'present' AND f.kind IN ('image', 'video')
  AND left(f.rel_path, length(@folder::text)) = @folder::text
  AND strpos(substr(f.rel_path, length(@folder::text) + 1), '/') > 0
GROUP BY 1
ORDER BY 1;

-- name: FolderLibraries :many
-- The viewer's libraries, with how many photos and videos each holds.
SELECT lb.id, lb.name, lb.kind, lb.read_only,
       (SELECT count(*) FROM files f WHERE f.library_id = lb.id AND f.state = 'present' AND f.kind IN ('image', 'video'))::integer AS files
FROM libraries lb WHERE lb.owner_id = @viewer::uuid
ORDER BY (lb.kind = 'managed') DESC, lb.name;

-- name: ListPeople :many
-- People: the named, then the ones grouping found, most items first (person_items says who is in
-- what). The cover is their chosen face, else (someone known only from an import so far) their newest
-- item. Hidden people are listed only when asked for, or by id.
-- Counted for everyone in one pass (st, cv) rather than once per person: each pass reads the
-- viewer's whole library, so with a thousand people the list took over half a minute.
WITH st AS (
    SELECT m.person_id, count(DISTINCT a.id)::integer AS item_count,
           count(DISTINCT a.id) FILTER (WHERE m.by_face)::integer AS face_items
    FROM person_items m JOIN visible_assets(@viewer::uuid) a ON a.id = m.asset_id
    WHERE a.motion_of IS NULL AND a.visibility = 'timeline'
      AND (@id::uuid = '00000000-0000-0000-0000-000000000000' OR m.person_id = @id::uuid)
    GROUP BY m.person_id
), cv AS (
    SELECT DISTINCT ON (m.person_id) m.person_id, a.id, a.thumbhash
    FROM person_items m JOIN visible_assets(@viewer::uuid) a ON a.id = m.asset_id
    WHERE a.motion_of IS NULL AND a.visibility = 'timeline' AND a.render_state = 'done'
      AND m.person_id IN (SELECT pc.id FROM people pc WHERE pc.owner_id = @viewer::uuid AND pc.cover_face_id IS NULL
                                                       AND (@id::uuid = '00000000-0000-0000-0000-000000000000' OR pc.id = @id::uuid))
    ORDER BY m.person_id, a.local_date DESC, a.taken_at DESC
)
SELECT p.id, p.name, p.birth_date, p.origin, p.hidden, p.kind, p.species, p.in_memories,
       coalesce(st.item_count, 0)::integer AS item_count, coalesce(st.face_items, 0)::integer AS face_items,
       coalesce(p.cover_face_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS cover_face_id,
       coalesce(cv.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS cover_id, cv.thumbhash AS cover_thumbhash
FROM people p
LEFT JOIN st ON st.person_id = p.id
LEFT JOIN cv ON cv.person_id = p.id AND p.cover_face_id IS NULL
WHERE p.owner_id = @viewer::uuid AND p.character_id IS NULL
  AND CASE WHEN @id::uuid = '00000000-0000-0000-0000-000000000000' THEN p.hidden = @hidden::boolean AND coalesce(st.item_count, 0) > 0 ELSE p.id = @id::uuid END
ORDER BY (p.name <> '') DESC, coalesce(st.item_count, 0) DESC, p.name, p.id;

-- name: AssetAlbums :many
-- The albums an item is in. The caller has already checked the item through the gate.
SELECT al.id, al.title FROM album_assets aa JOIN albums al ON al.id = aa.album_id
WHERE aa.asset_id = @asset_id AND al.owner_id = @viewer::uuid
ORDER BY al.title;

-- name: AssetPeople :many
-- The people in an item (person_items). The caller has already checked the item through the gate.
SELECT p.id, p.name, p.kind, CASE WHEN bool_or(pi.by_face) THEN 'faces' ELSE min(ps.source) END::text AS source
FROM person_items pi JOIN people p ON p.id = pi.person_id
LEFT JOIN person_suggestions ps ON ps.person_id = pi.person_id AND ps.asset_id = pi.asset_id AND NOT pi.by_face
WHERE pi.asset_id = @asset_id AND p.owner_id = @viewer::uuid AND NOT p.hidden AND p.character_id IS NULL
GROUP BY p.id, p.name, p.kind
ORDER BY (p.name <> '') DESC, p.name;

-- name: ListCharacters :many
-- Characters with how many items show them and when, newest activity first.
-- Counted for every character in one pass (st), not once per character.
WITH st AS (
    SELECT ca.character_id, count(*)::integer AS item_count, min(a.local_date)::date AS first_date, max(a.local_date)::date AS last_date
    FROM character_assets ca JOIN visible_assets(@viewer::uuid) a ON a.id = ca.asset_id
    WHERE ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed') AND a.visibility = 'timeline' AND a.motion_of IS NULL
      AND (@id::uuid = '00000000-0000-0000-0000-000000000000' OR ca.character_id = @id::uuid)
    GROUP BY ca.character_id
)
SELECT c.id, c.name, c.loras, coalesce(st.item_count, 0)::integer AS item_count, st.first_date, st.last_date,
       (SELECT count(*) FROM people p WHERE p.character_id = c.id)::integer AS face_groups
FROM characters c
LEFT JOIN st ON st.character_id = c.id
WHERE c.owner_id = @viewer::uuid AND (@id::uuid = '00000000-0000-0000-0000-000000000000' OR c.id = @id::uuid)
ORDER BY coalesce(st.item_count, 0) DESC, c.name;

-- name: CharacterSamples :many
-- Up to four recent items for each character, stills first; none that's adult when the viewer hides
-- those.
SELECT character_id, id, thumbhash FROM (
    SELECT ca.character_id, a.id, a.thumbhash,
           row_number() OVER (PARTITION BY ca.character_id ORDER BY (a.type = 'image') DESC, a.local_date DESC, a.taken_at DESC, a.id) AS n
    FROM character_assets ca JOIN visible_assets(@viewer::uuid) a ON a.id = ca.asset_id
    WHERE ca.character_id = ANY(@ids::uuid[]) AND ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed')
      AND a.visibility = 'timeline' AND a.motion_of IS NULL AND a.render_state = 'done'
      AND (NOT @hide_adult::boolean OR NOT EXISTS (
        SELECT 1 FROM asset_labels x WHERE x.asset_id = a.id AND x.label = 'adult' AND x.basis IN ('visual', 'confirmed')))
) x
WHERE n <= 4
ORDER BY character_id, n;

-- name: CharacterLoras :many
-- The LoRAs behind each character's items, most used first.
SELECT ca.character_id, m.display, count(*)::integer AS uses
FROM character_assets ca JOIN visible_assets(@viewer::uuid) a ON a.id = ca.asset_id
JOIN generation_loras gl ON gl.asset_id = ca.asset_id JOIN ai_models m ON m.id = gl.lora_id
JOIN characters c ON c.id = ca.character_id
WHERE ca.character_id = ANY(@ids::uuid[]) AND ca.basis = 'metadata' AND a.visibility = 'timeline'
GROUP BY 1, 2
ORDER BY 1, 3 DESC;

-- name: AssetCharacters :many
-- The characters an item shows. The caller has already checked the item through the gate.
SELECT c.id, c.name, ca.basis FROM character_assets ca JOIN characters c ON c.id = ca.character_id
WHERE ca.asset_id = @asset_id AND c.owner_id = @viewer::uuid AND ca.basis IN ('metadata', 'lineage', 'faces', 'confirmed')
ORDER BY c.name;

-- The trash: trashed items not deleted yet, through trashed_assets(viewer), laid out like the
-- timeline. A stack member is folded under its top only when that's in the trash too.

-- name: TrashBuckets :many
SELECT to_char(a.local_date, 'YYYY-MM')::text AS month, count(*)::integer AS count
FROM trashed_assets(@viewer::uuid) a
WHERE a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  JOIN trashed_assets(@viewer::uuid) c ON c.id = st.cover_asset_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
GROUP BY 1
ORDER BY 1 DESC;

-- name: TrashRange :many
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       coalesce(us.favorite, false)::boolean AS favorite, a.source_type, us.rating, us.flag, us.color_label,
       false::boolean AS shared, false::boolean AS from_others
FROM trashed_assets(@viewer::uuid) a
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
WHERE a.motion_of IS NULL AND (@type::text = '' OR a.type = @type::text)
  AND a.local_date >= @from_date::date AND a.local_date < @to_date::date
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  JOIN trashed_assets(@viewer::uuid) c ON c.id = st.cover_asset_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC;

-- name: TrashedAsset :one
-- One item in the viewer's trash (the viewer opened from the trash shows it).
SELECT a.id FROM trashed_assets(@viewer::uuid) a WHERE a.id = @id::uuid;

-- name: AlbumRules :one
-- Whether an album of the viewer's is smart, and its rules.
SELECT kind, query FROM albums WHERE id = @id AND owner_id = @viewer::uuid;
