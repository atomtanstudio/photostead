-- The sync feed: what a phone keeps of the library, through the gate (visible_assets(viewer)),
-- and what changed since it last read.

-- name: SyncCursor :one
-- Where this read stands: the oldest transaction still running (everything before it is over).
SELECT pg_snapshot_xmin(pg_current_snapshot())::text;

-- name: SyncChanged :many
-- What changed in transactions from cursor on (a few may have been read before).
SELECT DISTINCT kind, id FROM sync_changes WHERE txid >= CAST(@since::text AS xid8) ORDER BY kind, id;

-- name: PruneSyncChanges :one
-- Changes older than before go; returns the newest transaction among them ('' if none).
WITH gone AS (DELETE FROM sync_changes WHERE at < @before RETURNING txid)
SELECT coalesce(max(txid)::text, '')::text FROM gone;

-- name: SyncAssets :many
-- Items as the viewer sees them: a page of all of them (by id, after after), or these ids.
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id, a.motion_of, a.owner_id, a.visibility, a.source_type, a.mime,
       encode(a.sha256, 'hex')::text AS sha256, coalesce(f.rel_path, '')::text AS rel_path,
       coalesce(us.favorite, false)::boolean AS favorite, us.rating, us.flag, us.color_label,
       EXISTS (SELECT 1 FROM space_assets sx WHERE sx.asset_id = coalesce(a.motion_of, a.id))::boolean AS shared,
       (a.owner_id = @viewer::uuid OR (a.visibility = 'timeline' AND EXISTS (
            SELECT 1 FROM space_assets sx JOIN spaces sp ON sp.id = sx.space_id JOIN household_members hm ON hm.household_id = sp.household_id
            WHERE hm.user_id = @viewer::uuid AND sx.asset_id = coalesce(a.motion_of, a.id))))::boolean AS in_library,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
               WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)::boolean AS stacked,
       EXISTS (SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id AND l.label = 'adult' AND l.basis IN ('visual', 'confirmed'))::boolean AS adult
FROM visible_assets(@viewer::uuid) a
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @viewer::uuid
LEFT JOIN files f ON f.id = a.primary_file_id
WHERE (cardinality(@ids::uuid[]) = 0 AND a.id > @after::uuid) OR a.id = ANY(@ids::uuid[])
ORDER BY a.id
LIMIT @lim;

-- name: SyncAlbums :many
-- The viewer's albums and the ones shared with them: all, or these ids.
SELECT al.id, al.title, al.description, al.owner_id, al.parent_id, al.kind, al.cover_asset_id, al.updated_at,
       (CASE WHEN al.owner_id = @viewer::uuid THEN 'owner' ELSE am.role END)::text AS role
FROM albums al LEFT JOIN album_members am ON am.album_id = al.id AND am.user_id = @viewer::uuid
WHERE (al.owner_id = @viewer::uuid OR am.user_id IS NOT NULL)
  AND (cardinality(@ids::uuid[]) = 0 OR al.id = ANY(@ids::uuid[]))
ORDER BY al.id;

-- name: SyncAlbumItems :many
-- The items of these albums the viewer may see, in the album's order.
SELECT aa.album_id, aa.asset_id FROM album_assets aa JOIN visible_assets(@viewer::uuid) a ON a.id = aa.asset_id
WHERE aa.album_id = ANY(@ids::uuid[])
ORDER BY aa.album_id, aa.position NULLS LAST, aa.added_at, aa.asset_id;
