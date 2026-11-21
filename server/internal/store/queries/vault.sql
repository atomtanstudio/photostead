-- The vault: its PIN, unlocking it per session, and moving items in and out. Reads of what's in it
-- go through vault_assets(viewer), which the API calls with the viewer only once it's unlocked.

-- name: VaultPin :one
SELECT vault_pin, vault_failures, vault_blocked_until FROM users WHERE id = @user_id;

-- name: SetVaultPin :exec
UPDATE users SET vault_pin = @pin_hash, vault_failures = 0, vault_blocked_until = NULL WHERE id = @user_id;

-- name: VaultPinFailed :one
-- A wrong PIN. From the fifth in a row, each makes the next try wait: a minute, then twice as
-- long each time, up to an hour.
UPDATE users SET vault_failures = vault_failures + 1,
    vault_blocked_until = CASE WHEN vault_failures + 1 >= 5
        THEN now() + make_interval(secs => least(60 * power(2, vault_failures + 1 - 5), 3600)) END
WHERE id = @user_id
RETURNING vault_blocked_until;

-- name: UnlockVault :exec
WITH ok AS (UPDATE users u SET vault_failures = 0, vault_blocked_until = NULL WHERE u.id = @user_id::uuid)
UPDATE sessions s SET vault_until = now() + make_interval(mins => @minutes::integer) WHERE s.id = @session_id::uuid AND s.user_id = @user_id::uuid;

-- name: SlideVault :exec
-- Still in use: the vault stays unlocked a while longer.
UPDATE sessions SET vault_until = now() + make_interval(mins => @minutes::integer) WHERE id = @session_id AND vault_until > now();

-- name: LockVault :exec
UPDATE sessions SET vault_until = NULL WHERE id = @session_id;

-- name: LockVaultEverywhere :exec
-- A new PIN locks the vault in every other session.
UPDATE sessions SET vault_until = NULL WHERE user_id = @user_id AND id <> @session_id;

-- name: MoveToVault :many
-- The owner's items (from the timeline or the archive) into the vault, with their Live Photo clips
-- and the RAW of a RAW+JPEG pair they top.
WITH picked AS (
    SELECT a.id FROM assets a
    WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @owner::uuid AND a.trashed_at IS NULL AND a.purged_at IS NULL
      AND a.visibility IN ('timeline', 'archived')
),
everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg'
)
UPDATE assets a SET visibility = 'vault', updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.owner_id = @owner::uuid AND a.visibility <> 'vault'
RETURNING a.id;

-- name: MoveOutOfVault :many
-- Back to the timeline, with whatever went in with them.
WITH picked AS (
    SELECT a.id FROM vault_assets(@owner::uuid) a WHERE a.id = ANY(@ids::uuid[])
),
everything AS (
    SELECT picked.id FROM picked
    UNION SELECT m.id FROM assets m JOIN picked ON m.motion_of = picked.id
    UNION SELECT sa.asset_id FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id JOIN picked ON st.cover_asset_id = picked.id
          WHERE st.state = 'confirmed' AND st.kind = 'raw_jpeg'
)
UPDATE assets a SET visibility = 'timeline', updated_at = now()
FROM everything e
WHERE a.id = e.id AND a.owner_id = @owner::uuid AND a.visibility = 'vault'
RETURNING a.id;

-- name: StacksLeavingForVault :many
-- Stacks (other than RAW+JPEG pairs, which go in whole) that items going into the vault are in:
-- they leave them, so a stack never hides behind, or shows, something in the vault.
SELECT st.id, st.cover_asset_id, sa.asset_id
FROM stacks st JOIN stack_assets sa ON sa.stack_id = st.id
WHERE st.owner_id = @owner::uuid AND st.kind <> 'raw_jpeg' AND sa.asset_id = ANY(@ids::uuid[]);

-- name: VaultBuckets :many
-- The vault (or its trash) by month. A stack member is folded under its top, as in the timeline.
SELECT to_char(a.local_date, 'YYYY-MM')::text AS month, count(*)::integer AS count
FROM vault_assets(@vault_viewer::uuid) a
WHERE a.motion_of IS NULL AND (a.trashed_at IS NOT NULL) = @deleted::boolean AND (@type::text = '' OR a.type = @type::text)
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
GROUP BY 1
ORDER BY 1 DESC;

-- name: VaultRange :many
SELECT a.id, a.type, a.taken_at, a.taken_offset_min, a.local_date, a.width, a.height, a.duration_ms, a.thumbhash,
       a.render_state, a.live_motion_id,
       (SELECT count(*) FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
        WHERE st.cover_asset_id = a.id AND st.state = 'confirmed')::integer AS stack_size,
       coalesce(us.favorite, false)::boolean AS favorite, a.source_type, us.rating, us.flag, us.color_label,
       false::boolean AS shared, false::boolean AS from_others
FROM vault_assets(@vault_viewer::uuid) a
LEFT JOIN asset_user_state us ON us.asset_id = a.id AND us.user_id = @vault_viewer::uuid
WHERE a.motion_of IS NULL AND (a.trashed_at IS NOT NULL) = @deleted::boolean AND (@type::text = '' OR a.type = @type::text)
  AND a.local_date >= @from_date::date AND a.local_date < @to_date::date
  AND NOT EXISTS (SELECT 1 FROM stack_assets sa JOIN stacks st ON st.id = sa.stack_id
                  WHERE sa.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
ORDER BY a.local_date DESC, a.taken_at DESC, a.id DESC;

-- name: VaultCount :one
-- Items in the vault and in its trash (shown only once it's unlocked).
SELECT count(*) FILTER (WHERE a.trashed_at IS NULL)::integer AS items, count(*) FILTER (WHERE a.trashed_at IS NOT NULL)::integer AS deleted
FROM vault_assets(@vault_viewer::uuid) a
WHERE a.motion_of IS NULL;
