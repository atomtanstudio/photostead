-- A person's edits to their own items: trash, restore, deleting for good, favorites and albums.
-- Each write is limited to the owner's items (or, for favorites and albums, to items the viewer
-- can see through the gate).

-- name: TrashAssets :many
-- Moves the owner's items to the trash, with their Live Photo clips and the RAW of a RAW+JPEG pair
-- they top (they went in together, so they come back together). Other stacks keep the rest: they
-- get a new top (ReplaceTrashedCovers).
WITH picked AS (
    SELECT a.id FROM assets a
    WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND a.trashed_at IS NULL
),
everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg'
)
UPDATE assets a SET trashed_at = now(), updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.owner_id = @owner::uuid AND a.trashed_at IS NULL
RETURNING a.id;

-- name: RestoreAssets :many
-- Takes the owner's items out of the trash, with whatever went in with them.
WITH picked AS (
    SELECT a.id, a.trashed_at FROM assets a
    WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND a.trashed_at IS NOT NULL AND a.purged_at IS NULL
),
everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id WHERE m.trashed_at = picked.trashed_at
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          JOIN assets x ON x.id = sa.asset_id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg' AND x.trashed_at = picked.trashed_at
)
UPDATE assets a SET trashed_at = NULL, updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.owner_id = @owner::uuid AND a.purged_at IS NULL
RETURNING a.id;

-- name: TrashedOf :many
-- Items in the owner's trash to delete for good: the ones given (with what went in with them), or
-- the whole trash when none are given. The vault's trash is apart: only its own Delete reaches it.
WITH picked AS (
    SELECT a.id, a.trashed_at FROM assets a
    WHERE a.owner_id = @owner::uuid AND a.trashed_at IS NOT NULL AND a.purged_at IS NULL
      AND (a.visibility = 'vault') = @vault::boolean
      AND (cardinality(@ids::uuid[]) = 0 OR a.id = ANY(@ids::uuid[]))
)
SELECT picked.id FROM picked
UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id WHERE m.trashed_at IS NOT NULL AND m.purged_at IS NULL
UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
      JOIN assets x ON x.id = sa.asset_id
      WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg' AND x.trashed_at IS NOT NULL AND x.purged_at IS NULL;

-- name: TrashedBefore :many
-- Everyone's items trashed before a time and not deleted yet (the 30-day clean-up).
SELECT a.id FROM assets a
WHERE a.trashed_at < @before::timestamptz AND a.purged_at IS NULL
ORDER BY a.trashed_at
LIMIT @lim;

-- name: WritableFiles :many
-- The files of these items that Photostead may delete: only in writable, managed libraries.
SELECT f.id, f.asset_id, l.root_path, f.rel_path
FROM files f JOIN libraries l ON l.id = f.library_id
WHERE f.asset_id = ANY(@ids::uuid[]) AND l.kind = 'managed' AND NOT l.read_only AND f.state = 'present';

-- name: MarkPurged :exec
-- Deleted for good: the files Photostead removed are marked so, and the item stays as a record.
WITH gone AS (
    UPDATE files SET state = 'deleted' WHERE id = ANY(@file_ids::uuid[]) RETURNING id
)
UPDATE assets SET purged_at = now(), updated_at = now()
WHERE id = ANY(@ids::uuid[]) AND trashed_at IS NOT NULL AND purged_at IS NULL;

-- name: ForgetPurged :exec
-- What was worked out about deleted items, which nothing will show again.
WITH e AS (DELETE FROM asset_embeddings WHERE asset_id = ANY(@ids::uuid[])),
     o AS (DELETE FROM asset_ocr WHERE asset_id = ANY(@ids::uuid[])),
     l AS (DELETE FROM asset_labels WHERE asset_id = ANY(@ids::uuid[])),
     s AS (DELETE FROM adult_scans WHERE asset_id = ANY(@ids::uuid[])),
     v AS (DELETE FROM video_copies WHERE asset_id = ANY(@ids::uuid[])),
     t AS (DELETE FROM worker_tasks WHERE asset_id = ANY(@ids::uuid[]))
DELETE FROM album_assets WHERE asset_id = ANY(@ids::uuid[]);

-- name: SetFavorites :execrows
-- Favorite (or not) items the viewer can see.
INSERT INTO asset_user_state (asset_id, user_id, favorite)
SELECT v.id, @viewer::uuid, @favorite::boolean FROM visible_assets(@viewer::uuid) v WHERE v.id = ANY(@ids::uuid[])
ON CONFLICT (asset_id, user_id) DO UPDATE SET favorite = EXCLUDED.favorite, updated_at = now();

-- name: NewAlbum :one
-- An album, inside another of the owner's (or at the top), and either kept by hand or smart (its
-- items are those its rules pick).
INSERT INTO albums (owner_id, title, parent_id, kind, query)
SELECT @owner::uuid, @title::text, sqlc.narg(parent_id)::uuid, @kind::text, sqlc.narg(query)::jsonb
WHERE sqlc.narg(parent_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM albums p WHERE p.id = sqlc.narg(parent_id)::uuid AND p.owner_id = @owner::uuid)
RETURNING id;

-- name: UpdateAlbum :execrows
-- A new title, description or (for a smart album) rules; anything left out stays.
UPDATE albums SET title = coalesce(sqlc.narg(title), title), description = coalesce(sqlc.narg(description), description),
    query = CASE WHEN kind = 'smart' THEN coalesce(sqlc.narg(query)::jsonb, query) ELSE query END, updated_at = now()
WHERE id = @id AND owner_id = @owner::uuid;

-- name: MoveAlbum :execrows
-- Puts an album inside another of the owner's (or at the top), never inside itself or one of its own.
WITH RECURSIVE inner_ AS (
    SELECT al.id FROM albums al WHERE al.parent_id = @id::uuid
    UNION ALL
    SELECT al.id FROM albums al JOIN inner_ i ON al.parent_id = i.id
)
UPDATE albums SET parent_id = sqlc.narg(parent_id)::uuid, updated_at = now()
WHERE id = @id::uuid AND owner_id = @owner::uuid
  AND (sqlc.narg(parent_id)::uuid IS NULL OR (
       sqlc.narg(parent_id)::uuid <> @id::uuid
       AND NOT EXISTS (SELECT 1 FROM inner_ WHERE inner_.id = sqlc.narg(parent_id)::uuid)
       AND EXISTS (SELECT 1 FROM albums p WHERE p.id = sqlc.narg(parent_id)::uuid AND p.owner_id = @owner::uuid)));

-- name: LiftChildAlbums :exec
-- Before an album goes, the albums in it move up to where it was.
UPDATE albums c SET parent_id = d.parent_id, updated_at = now()
FROM albums d WHERE d.id = @id::uuid AND d.owner_id = @owner::uuid AND c.parent_id = d.id;

-- name: DeleteAlbum :execrows
-- Only the album goes; its items stay in the library.
DELETE FROM albums WHERE id = @id::uuid AND owner_id = @owner::uuid;

-- name: AddToAlbum :execrows
-- Adds items the viewer can see to one of their albums, or one shared with them as a contributor
-- (kept by hand: a smart album's items are its rules').
INSERT INTO album_assets (album_id, asset_id, added_by)
SELECT al.id, v.id, @viewer::uuid
FROM albums al CROSS JOIN visible_assets(@viewer::uuid) v
WHERE al.id = @album_id::uuid AND al.kind = 'manual' AND v.id = ANY(@ids::uuid[])
  AND (al.owner_id = @viewer::uuid OR EXISTS (SELECT 1 FROM album_members am WHERE am.album_id = al.id AND am.user_id = @viewer::uuid AND am.role = 'contributor'))
ON CONFLICT (album_id, asset_id) DO NOTHING;

-- name: RemoveFromAlbum :execrows
-- The album's owner may take anything out; a contributor, what they put in.
DELETE FROM album_assets aa USING albums al
WHERE aa.album_id = al.id AND al.id = @album_id::uuid AND aa.asset_id = ANY(@ids::uuid[])
  AND (al.owner_id = @viewer::uuid OR aa.added_by = @viewer::uuid);

-- name: TouchAlbum :exec
UPDATE albums SET updated_at = now() WHERE id = @album_id::uuid AND owner_id = @viewer::uuid;

-- name: SetRatings :execrows
-- Stars (1-5, or none) for items the viewer can see: the viewer's own, like favorites.
INSERT INTO asset_user_state (asset_id, user_id, rating)
SELECT v.id, @viewer::uuid, sqlc.narg(rating)::smallint FROM visible_assets(@viewer::uuid) v WHERE v.id = ANY(@ids::uuid[])
ON CONFLICT (asset_id, user_id) DO UPDATE SET rating = EXCLUDED.rating, updated_at = now();

-- name: SetFlags :execrows
-- Pick, reject, or neither, for items the viewer can see.
INSERT INTO asset_user_state (asset_id, user_id, flag)
SELECT v.id, @viewer::uuid, sqlc.narg(flag)::text FROM visible_assets(@viewer::uuid) v WHERE v.id = ANY(@ids::uuid[])
ON CONFLICT (asset_id, user_id) DO UPDATE SET flag = EXCLUDED.flag, updated_at = now();

-- name: SetColorLabels :execrows
-- A color label, or none, for items the viewer can see.
INSERT INTO asset_user_state (asset_id, user_id, color_label)
SELECT v.id, @viewer::uuid, sqlc.narg(color)::text FROM visible_assets(@viewer::uuid) v WHERE v.id = ANY(@ids::uuid[])
ON CONFLICT (asset_id, user_id) DO UPDATE SET color_label = EXCLUDED.color_label, updated_at = now();

-- name: AssetMarks :many
-- The viewer's favorites, stars, flags and labels on these items (for lists that don't join them).
SELECT s.asset_id, s.favorite, s.rating, s.flag, s.color_label FROM asset_user_state s
WHERE s.user_id = @viewer::uuid AND s.asset_id = ANY(@ids::uuid[]);

-- name: AddKeywords :execrows
-- Keywords on the owner's items, by name. A name matching an existing keyword (ignoring case)
-- reuses it; a new one is made.
WITH wanted AS (
    SELECT DISTINCT ON (lower(btrim(n))) btrim(n) AS n FROM unnest(@names::text[]) n WHERE btrim(n) <> ''
), existing AS (
    SELECT DISTINCT ON (lower(k.name)) k.id, lower(k.name) AS l FROM keywords k JOIN wanted w ON lower(k.name) = lower(w.n)
    WHERE k.owner_id = @owner::uuid ORDER BY lower(k.name), k.created_at
), made AS (
    INSERT INTO keywords (owner_id, name)
    SELECT @owner::uuid, w.n FROM wanted w WHERE NOT EXISTS (SELECT 1 FROM existing e WHERE e.l = lower(w.n))
    ON CONFLICT (owner_id, name) DO UPDATE SET name = EXCLUDED.name
    RETURNING id
), kw AS (
    SELECT id FROM existing UNION ALL SELECT id FROM made
)
INSERT INTO asset_keywords (asset_id, keyword_id, source)
SELECT a.id, kw.id, 'user' FROM assets a CROSS JOIN kw WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid
ON CONFLICT (asset_id, keyword_id) DO NOTHING;

-- name: RemoveKeywords :execrows
-- Keywords off the owner's items, by name (ignoring case). The keyword itself stays.
DELETE FROM asset_keywords ak USING keywords k, assets a
WHERE ak.keyword_id = k.id AND ak.asset_id = a.id AND a.owner_id = @owner::uuid AND k.owner_id = @owner::uuid
  AND a.id = ANY(@ids::uuid[]) AND lower(k.name) IN (SELECT lower(btrim(n)) FROM unnest(@names::text[]) n);

-- name: SelectionKeywords :many
-- The keywords on these items of the owner's, with how many of them have each.
SELECT k.name, count(*)::integer AS items
FROM asset_keywords ak JOIN keywords k ON k.id = ak.keyword_id JOIN assets a ON a.id = ak.asset_id
WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid
GROUP BY k.name ORDER BY count(*) DESC, k.name;

-- name: ListKeywords :many
-- The owner's keywords, most used first (for suggestions).
SELECT k.name, count(ak.asset_id)::integer AS items
FROM keywords k LEFT JOIN asset_keywords ak ON ak.keyword_id = k.id
WHERE k.owner_id = @owner::uuid
GROUP BY k.id, k.name
HAVING count(ak.asset_id) > 0
ORDER BY count(ak.asset_id) DESC, k.name
LIMIT 1000;

-- name: SetDescriptions :execrows
-- A description written here for the owner's items: it wins over the file's and an import's, even
-- when empty (an empty one hides them).
INSERT INTO asset_descriptions (asset_id, text, source)
SELECT a.id, @text::text, 'user' FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid
ON CONFLICT (asset_id) DO UPDATE SET text = EXCLUDED.text, source = 'user', updated_at = now();

-- name: EarliestWallTime :one
-- The earliest of the owner's items, as its clock read when it was taken (to set dates from).
SELECT (CASE WHEN a.date_basis = 'recorded_offset'
             THEN (a.taken_at AT TIME ZONE 'UTC') + make_interval(mins => coalesce(a.taken_offset_min, 0))
             ELSE a.taken_at AT TIME ZONE @zone::text END)::timestamp AS wall
FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid
ORDER BY a.taken_at LIMIT 1;

-- name: ShiftDates :many
-- Moves the owner's items in time by the same amount, with their Live Photo clips and the rest of
-- any stack they top; the day each falls on follows. The date is the owner's from then on: nothing
-- read from files changes it again.
WITH picked AS (
    SELECT a.id FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND a.trashed_at IS NULL
), everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg'
)
UPDATE assets a SET
    taken_at = a.taken_at + make_interval(secs => @seconds::double precision),
    local_date = (CASE WHEN a.date_basis = 'recorded_offset'
                       THEN ((a.taken_at + make_interval(secs => @seconds::double precision)) AT TIME ZONE 'UTC')
                            + make_interval(mins => coalesce(a.taken_offset_min, 0))
                       ELSE (a.taken_at + make_interval(secs => @seconds::double precision)) AT TIME ZONE @zone::text END)::date,
    date_source = 'user', updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.owner_id = @owner::uuid
RETURNING a.id;

-- name: ArchiveItems :execrows
-- The owner's items out of the timeline into the archive, or back, with their Live Photo clips and
-- the RAW of a RAW+JPEG pair they top. Others' items, and the vault's and the trash's, stay put.
WITH picked AS (
    SELECT a.id FROM assets a
    WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND a.trashed_at IS NULL AND a.visibility IN ('timeline', 'archived')
),
everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg'
)
UPDATE assets a SET visibility = CASE WHEN @archived::boolean THEN 'archived' ELSE 'timeline' END, updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.visibility IN ('timeline', 'archived')
  AND a.visibility <> CASE WHEN @archived::boolean THEN 'archived' ELSE 'timeline' END;

-- name: SetAssetSize :exec
-- A photo's size after its owner edits it (or undoes the edit).
UPDATE assets SET width = @width, height = @height WHERE id = @id AND owner_id = @owner::uuid;
