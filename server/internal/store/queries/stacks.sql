-- Stacks beyond RAW+JPEG (internal/stacks): AI batches, camera bursts and near-duplicates, found by
-- a job working for each owner (so not through the gate), and an owner's own changes to stacks.
-- Reads for a viewer go through visible_assets.

-- name: AIBatchCandidates :many
-- Every AI image or clip made from a prompt, grouped by owner, kind, prompt and model, in the
-- order they were made, with the stack each is in (if any) and whether it was taken out of one.
SELECT a.id, a.owner_id, a.type, a.taken_at, g.prompt_id, g.model_id,
       sa.stack_id, st.kind AS stack_kind,
       EXISTS (SELECT 1 FROM stack_exclusions x WHERE x.asset_id = a.id AND x.kind = 'ai_batch')::boolean AS excluded,
       coalesce(us.favorite, false)::boolean AS favorite, coalesce(us.rating, 0)::integer AS rating
FROM generations g
JOIN assets a ON a.id = g.asset_id
LEFT JOIN stack_assets sa ON sa.asset_id = a.id
LEFT JOIN stacks st ON st.id = sa.stack_id
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = a.owner_id
WHERE g.kind = 'generated' AND g.prompt_id IS NOT NULL
  AND a.trashed_at IS NULL AND a.purged_at IS NULL AND a.motion_of IS NULL AND a.visibility = 'timeline'
ORDER BY a.owner_id, a.type, g.prompt_id, g.model_id NULLS FIRST, a.taken_at, a.id;

-- name: BurstCandidates :many
-- Camera photos, by owner and camera, in the order they were taken, with a burst identifier when
-- the camera wrote one.
SELECT a.id, a.owner_id, a.taken_at, coalesce(m.make, '')::text AS make, coalesce(m.model, '')::text AS model,
       coalesce(m.raw->>'Apple:BurstUUID', m.raw->>'XMP-apple-fi:BurstUUID', m.raw->>'MakerNotes:BurstUUID', '')::text AS burst,
       sa.stack_id, st.kind AS stack_kind,
       EXISTS (SELECT 1 FROM stack_exclusions x WHERE x.asset_id = a.id AND x.kind = 'burst')::boolean AS excluded
FROM assets a
JOIN asset_metadata m ON m.asset_id = a.id
LEFT JOIN stack_assets sa ON sa.asset_id = a.id
LEFT JOIN stacks st ON st.id = sa.stack_id
WHERE a.type = 'image' AND a.source_type NOT IN ('ai_generated', 'ai_edited') AND (m.make IS NOT NULL OR m.model IS NOT NULL)
  AND a.trashed_at IS NULL AND a.purged_at IS NULL AND a.motion_of IS NULL AND a.visibility = 'timeline'
ORDER BY a.owner_id, m.make, m.model, a.taken_at, a.id;

-- name: NewStack :one
INSERT INTO stacks (owner_id, kind, cover_asset_id, state) VALUES (@owner_id, @kind, @cover, @state) RETURNING id;

-- name: AddStackMembers :exec
-- Items into a stack, in this order after those already there. An item already in a stack stays
-- where it is.
INSERT INTO stack_assets (stack_id, asset_id, position)
SELECT @stack_id::uuid, x.id, coalesce((SELECT max(position) FROM stack_assets WHERE stack_id = @stack_id::uuid), -1) + x.n
FROM unnest(@asset_ids::uuid[]) WITH ORDINALITY AS x(id, n)
ON CONFLICT DO NOTHING;

-- name: NearDuplicateCandidates :many
-- Real photos and videos not yet compared for near-duplicates, with their search embedding (and
-- ThumbHash, to pass over blank ones).
SELECT a.id, a.owner_id, e.embedding::text AS vec, a.thumbhash
FROM assets a JOIN asset_embeddings e ON e.asset_id = a.id AND e.model = @model::text
WHERE a.source_type NOT IN ('ai_generated', 'ai_edited') AND a.trashed_at IS NULL AND a.purged_at IS NULL
  AND a.motion_of IS NULL AND a.visibility = 'timeline'
  AND NOT EXISTS (SELECT 1 FROM near_duplicate_checks c WHERE c.asset_id = a.id)
ORDER BY a.created_at
LIMIT @lim;

-- name: NearDuplicatePeers :many
-- What the found neighbours are: same owner, real, in no stack or in a suggested look-alike stack,
-- and not kept apart.
SELECT a.id, a.owner_id, sa.stack_id, st.kind AS stack_kind, st.state AS stack_state, (a.width * a.height)::bigint AS pixels,
       a.type, coalesce(a.duration_ms, 0)::integer AS duration_ms, a.thumbhash
FROM assets a
LEFT JOIN stack_assets sa ON sa.asset_id = a.id
LEFT JOIN stacks st ON st.id = sa.stack_id
WHERE a.id = ANY(@ids::uuid[]) AND a.source_type NOT IN ('ai_generated', 'ai_edited') AND a.trashed_at IS NULL
  AND a.purged_at IS NULL AND a.motion_of IS NULL AND a.visibility = 'timeline'
  AND NOT EXISTS (SELECT 1 FROM stack_exclusions x WHERE x.asset_id = a.id AND x.kind = 'near_duplicate');

-- name: MarkNearDuplicateChecked :exec
INSERT INTO near_duplicate_checks (asset_id) SELECT unnest(@ids::uuid[]) ON CONFLICT DO NOTHING;

-- name: ReplaceTrashedCovers :exec
-- A stack whose top item went to the trash (and that didn't go with it) gets a new top: the first
-- of the rest that's still here.
UPDATE stacks st SET cover_asset_id = n.id
FROM (
    SELECT DISTINCT ON (sa.stack_id) sa.stack_id, sa.asset_id AS id
    FROM stack_assets sa JOIN assets m ON m.id = sa.asset_id
    WHERE m.trashed_at IS NULL AND m.owner_id = @owner_id
    ORDER BY sa.stack_id, sa.position
) n, assets c
WHERE st.id = n.stack_id AND c.id = st.cover_asset_id AND c.trashed_at IS NOT NULL AND st.owner_id = @owner_id;

-- name: StackOf :one
-- The stack an item the viewer can see is in.
SELECT st.id, st.kind, st.state, st.cover_asset_id
FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id JOIN visible_assets(@viewer::uuid) a ON a.id = sa.asset_id
WHERE sa.asset_id = @asset_id;

-- name: StackMembers :many
-- A stack's items the viewer can see, in order.
SELECT a.id, a.type, a.taken_at, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash, a.render_state, a.source_type, sa.position
FROM stack_assets sa JOIN visible_assets(@viewer::uuid) a ON a.id = sa.asset_id
WHERE sa.stack_id = @stack_id
ORDER BY sa.position, a.taken_at;

-- name: SuggestedStacks :many
-- Look-alike groups waiting for the owner to say yes or no, biggest first.
SELECT st.id, st.cover_asset_id, count(*)::integer AS members
FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN visible_assets(@viewer::uuid) a ON a.id = sa.asset_id
WHERE st.owner_id = @viewer::uuid AND st.state = 'suggested'
GROUP BY st.id
HAVING count(*) >= 2
ORDER BY count(*) DESC, max(a.taken_at) DESC
LIMIT @lim OFFSET @off;

-- name: SuggestedStackCount :one
SELECT count(*)::integer FROM stacks WHERE owner_id = @viewer::uuid AND state = 'suggested';

-- name: SetStackCover :execrows
UPDATE stacks st SET cover_asset_id = @asset_id::uuid
WHERE st.id = @stack_id AND st.owner_id = @owner_id
  AND EXISTS (SELECT 1 FROM stack_assets sa WHERE sa.stack_id = st.id AND sa.asset_id = @asset_id::uuid);

-- name: ConfirmStack :execrows
UPDATE stacks SET state = 'confirmed' WHERE id = @stack_id AND owner_id = @owner_id;

-- name: ExcludeFromStacks :exec
-- These items stay out of stacks of this kind from now on (the owner took them out, or kept them apart).
INSERT INTO stack_exclusions (asset_id, kind)
SELECT a.id, @kind::text FROM assets a WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner_id
ON CONFLICT DO NOTHING;

-- name: RemoveStackMembers :execrows
DELETE FROM stack_assets sa USING stacks st
WHERE sa.stack_id = st.id AND st.id = @stack_id AND st.owner_id = @owner_id AND sa.asset_id = ANY(@ids::uuid[]);

-- name: StackOwned :one
SELECT id, kind, state, cover_asset_id, (SELECT count(*) FROM stack_assets sa WHERE sa.stack_id = stacks.id)::integer AS members
FROM stacks WHERE id = @stack_id AND owner_id = @owner_id;

-- name: StackMemberIDs :many
SELECT asset_id FROM stack_assets WHERE stack_id = @stack_id ORDER BY position;

-- name: DeleteStack :exec
DELETE FROM stacks WHERE id = @stack_id AND owner_id = @owner_id;

-- name: StackedAlready :many
-- Which of these items are in a stack.
SELECT asset_id FROM stack_assets WHERE asset_id = ANY(@ids::uuid[]);

-- name: OwnedVisible :many
-- Which of these items are the owner's and in the timeline (to stack by hand), oldest first.
SELECT a.id FROM assets a
WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner_id AND a.trashed_at IS NULL AND a.purged_at IS NULL
  AND a.motion_of IS NULL AND a.visibility = 'timeline'
ORDER BY a.taken_at, a.id;

-- name: ConfirmAllSuggested :many
-- Yes to every look-alike group waiting for an answer.
UPDATE stacks SET state = 'confirmed' WHERE owner_id = @owner_id AND state = 'suggested' AND kind = 'near_duplicate' RETURNING id;

-- name: UnconfirmStacks :execrows
-- Back to waiting for an answer (undoing "Stack all").
UPDATE stacks SET state = 'suggested'
WHERE owner_id = @owner_id AND id = ANY(@ids::uuid[]) AND state = 'confirmed' AND kind = 'near_duplicate';
