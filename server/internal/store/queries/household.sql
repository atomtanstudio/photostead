-- The household: its members and invitations, sharing into its space, and auto-share rules. Reads
-- of what's shared go through visible_assets(viewer), which lets members see the space.

-- name: Household :one
-- The viewer's household, its space, and their role in it.
SELECT h.id, h.name, sp.id AS space_id, hm.role,
       (SELECT count(*) FROM space_assets sa WHERE sa.space_id = sp.id)::integer AS shared
FROM household_members hm
JOIN households h ON h.id = hm.household_id
JOIN spaces sp ON sp.household_id = h.id AND sp.kind = 'household'
WHERE hm.user_id = @viewer::uuid;

-- name: HouseholdMembers :many
SELECT u.id, u.email, u.display_name, hm.role, hm.joined_at,
       (SELECT count(*) FROM space_assets sa JOIN spaces sp ON sp.id = sa.space_id
        WHERE sp.household_id = hm.household_id AND sa.added_by = u.id)::integer AS shared
FROM household_members hm JOIN users u ON u.id = hm.user_id
WHERE hm.household_id = @household_id
ORDER BY hm.joined_at;

-- name: RenameHousehold :exec
UPDATE households SET name = @name WHERE id = @id;

-- name: SetMemberRole :execrows
UPDATE household_members SET role = @role WHERE household_id = @household_id AND user_id = @user_id AND role <> 'owner';

-- name: ShareItems :many
-- Into the space: the viewer's own items (in their timeline or archive), with their Live Photo clips
-- riding along by the gate, and none they or a rule took out unless shared by hand now.
WITH picked AS (
    SELECT a.id FROM assets a
    WHERE a.id = ANY(@ids::uuid[]) AND a.owner_id = @viewer::uuid AND a.trashed_at IS NULL
      AND a.visibility IN ('timeline', 'archived') AND a.motion_of IS NULL
),
cleared AS (
    DELETE FROM space_exclusions x USING picked WHERE x.space_id = @space_id AND x.asset_id = picked.id
)
INSERT INTO space_assets (space_id, asset_id, added_by)
SELECT @space_id, picked.id, @viewer::uuid FROM picked
ON CONFLICT DO NOTHING
RETURNING asset_id;

-- name: UnshareItems :many
-- Out of the space (any member may), and kept out: rules won't put them back.
WITH gone AS (
    DELETE FROM space_assets sa WHERE sa.space_id = @space_id AND sa.asset_id = ANY(@ids::uuid[])
    RETURNING sa.asset_id
),
kept AS (
    INSERT INTO space_exclusions (space_id, asset_id) SELECT @space_id, asset_id FROM gone ON CONFLICT DO NOTHING
)
SELECT asset_id FROM gone;

-- name: ShareRule :one
SELECT id, from_date, people, include_ai, enabled, updated_at FROM share_rules WHERE user_id = @viewer::uuid AND space_id = @space_id;

-- name: SetShareRule :one
INSERT INTO share_rules (user_id, space_id, from_date, people, include_ai, enabled)
VALUES (@viewer::uuid, @space_id, sqlc.narg(from_date), @people::uuid[], @include_ai, @enabled)
ON CONFLICT (user_id, space_id) DO UPDATE SET from_date = EXCLUDED.from_date, people = EXCLUDED.people,
    include_ai = EXCLUDED.include_ai, enabled = EXCLUDED.enabled, updated_at = now()
RETURNING id;

-- name: ApplyShareRules :execrows
-- Every enabled rule's items into its space: the rule's owner's items in their timeline, rendered,
-- from its day on, with any of its people (or anyone), not AI-made unless it says so, not a
-- screenshot or document, not a Live Photo's clip or under another in a stack, and not taken out.
INSERT INTO space_assets (space_id, asset_id, added_by, via_rule_id)
SELECT r.space_id, a.id, r.user_id, r.id
FROM share_rules r
JOIN assets a ON a.owner_id = r.user_id
WHERE r.enabled AND (cardinality(@rules::uuid[]) = 0 OR r.id = ANY(@rules::uuid[]))
  AND a.visibility = 'timeline' AND a.trashed_at IS NULL AND a.motion_of IS NULL AND a.render_state = 'done'
  AND (r.from_date IS NULL OR a.local_date >= r.from_date)
  AND (r.include_ai OR a.source_type NOT IN ('ai_generated', 'ai_edited'))
  AND (cardinality(r.people) = 0 OR EXISTS (SELECT 1 FROM person_items pi WHERE pi.asset_id = a.id AND pi.person_id = ANY(r.people)))
  AND NOT EXISTS (SELECT 1 FROM asset_labels l WHERE l.asset_id = a.id
                  AND l.label IN ('screenshot', 'document', 'receipt', 'id_card', 'qr_code') AND l.basis IN ('metadata', 'visual', 'confirmed'))
  AND NOT EXISTS (SELECT 1 FROM stack_assets sk JOIN stacks st ON st.id = sk.stack_id
                  WHERE sk.asset_id = a.id AND st.state = 'confirmed' AND st.cover_asset_id <> a.id)
  AND NOT EXISTS (SELECT 1 FROM space_exclusions x WHERE x.space_id = r.space_id AND x.asset_id = a.id)
ON CONFLICT DO NOTHING;

-- name: UnshareFromRule :execrows
-- A rule turned off or narrowed: what it alone shared comes out (by hand stays; nothing is
-- excluded, so turning it on again shares them again).
DELETE FROM space_assets sa WHERE sa.via_rule_id = @rule_id;

-- name: SpaceAssetIDs :many
-- Which of these items are in the space.
SELECT asset_id FROM space_assets WHERE space_id = @space_id AND asset_id = ANY(@ids::uuid[]);

-- name: PendingInvites :many
-- Invitations not used or run out yet (the household is the server's one).
SELECT id, data, expires_at FROM auth_flows WHERE kind = 'invite' AND expires_at > now() ORDER BY expires_at;

-- name: CancelInvite :execrows
DELETE FROM auth_flows WHERE id = @id AND kind = 'invite';

-- name: CancelGuestInvite :execrows
-- A guest's invitation to an album, cancelled by the album's owner.
DELETE FROM auth_flows f USING albums al
WHERE f.id = @id AND f.kind = 'invite' AND f.data->>'role' = 'guest'
  AND f.data->>'album' = al.id::text AND al.id = @album_id AND al.owner_id = @owner_id;

-- name: LeaveSpaces :exec
-- Items going into the vault leave every space, and stay out (rules won't share them again).
WITH gone AS (
    DELETE FROM space_assets sa WHERE sa.asset_id = ANY(@ids::uuid[]) RETURNING sa.space_id, sa.asset_id
)
INSERT INTO space_exclusions (space_id, asset_id) SELECT space_id, asset_id FROM gone ON CONFLICT DO NOTHING;

-- name: AlbumMembers :many
-- Who an album is shared with (its owner asking).
SELECT u.id, u.email, coalesce(nullif(u.display_name, ''), u.username)::text AS name, u.role AS account, am.role
FROM album_members am JOIN users u ON u.id = am.user_id JOIN albums al ON al.id = am.album_id
WHERE am.album_id = @album_id AND al.owner_id = @owner_id
ORDER BY am.added_at;

-- name: ShareCandidates :many
-- People an owner can share an album with: the household, and guests already in one of their albums.
SELECT DISTINCT u.id, u.email, coalesce(nullif(u.display_name, ''), u.username)::text AS name, u.role AS account
FROM users u
WHERE u.id <> @owner_id AND (
      EXISTS (SELECT 1 FROM household_members a JOIN household_members b ON b.household_id = a.household_id
              WHERE a.user_id = @owner_id AND b.user_id = u.id)
   OR EXISTS (SELECT 1 FROM album_members am JOIN albums al ON al.id = am.album_id WHERE al.owner_id = @owner_id AND am.user_id = u.id))
ORDER BY name;

-- name: SetAlbumMember :execrows
INSERT INTO album_members (album_id, user_id, role, added_by)
SELECT al.id, @user_id, @role, @owner_id FROM albums al
WHERE al.id = @album_id AND al.owner_id = @owner_id AND al.kind = 'manual' AND @user_id <> @owner_id
ON CONFLICT (album_id, user_id) DO UPDATE SET role = EXCLUDED.role;

-- name: RemoveAlbumMember :execrows
DELETE FROM album_members am USING albums al
WHERE am.album_id = al.id AND al.id = @album_id AND al.owner_id = @owner_id AND am.user_id = @user_id;

-- name: AlbumRole :one
-- What someone may do with an album: owner, contributor or viewer (no row: nothing).
SELECT al.owner_id, al.title, al.kind,
       CASE WHEN al.owner_id = @viewer::uuid THEN 'owner' ELSE am.role END::text AS role
FROM albums al LEFT JOIN album_members am ON am.album_id = al.id AND am.user_id = @viewer::uuid
WHERE al.id = @album_id AND (al.owner_id = @viewer::uuid OR am.user_id IS NOT NULL);

-- name: CreateGuest :one
INSERT INTO users (email, username, display_name, role) VALUES (@email, @username, @display_name, 'guest')
RETURNING *;

-- name: Guests :many
-- Every guest on the server, for its admin: who shared with them first (made their personal link),
-- how many albums are shared with them, and how many browsers and devices are signed in as them.
SELECT u.id, u.email, coalesce(nullif(u.display_name, ''), u.username)::text AS name, u.created_at,
       coalesce((SELECT coalesce(nullif(c.display_name, ''), c.username) FROM personal_links pl JOIN users c ON c.id = pl.created_by WHERE pl.user_id = u.id),
                (SELECT coalesce(nullif(c.display_name, ''), c.username) FROM album_members am JOIN users c ON c.id = am.added_by
                 WHERE am.user_id = u.id ORDER BY am.added_at LIMIT 1), '')::text AS added_by,
       (SELECT count(*) FROM album_members am WHERE am.user_id = u.id)::integer AS albums,
       (SELECT count(*) FROM sessions s WHERE s.user_id = u.id AND s.expires_at > now())::integer AS devices
FROM users u
WHERE u.role = 'guest'
ORDER BY u.created_at;

-- name: GuestToRemove :one
-- Someone about to be removed as a guest: their role, and what they own that removing them would
-- take or that would stop it. A guest owns none of it (no library, uploads go into the album
-- owner's), unless given a library or an API key from the command line.
SELECT u.role, coalesce(nullif(u.display_name, ''), u.username)::text AS name,
       (SELECT count(*) FROM libraries l WHERE l.owner_id = u.id)::integer AS libraries,
       (SELECT count(*) FROM assets a WHERE a.owner_id = u.id)::integer AS assets,
       (SELECT count(*) FROM albums al WHERE al.owner_id = u.id)::integer AS albums
FROM users u
WHERE u.id = @id;

-- name: RemoveGuest :execrows
-- A guest goes, and with them (ON DELETE CASCADE) their sign-ins and devices, personal link,
-- album shares, comments, picks, marks and sign-ins under way. What they added to an album stays
-- there, no longer noted as theirs (album_assets.added_by is SET NULL).
DELETE FROM users WHERE id = @id AND role = 'guest';

-- Personal links (sharing with someone by name, no account to make).

-- name: CreatePersonalLink :one
INSERT INTO personal_links (user_id, token, created_by) VALUES (@user_id, @token, @created_by)
RETURNING *;

-- name: ReplacePersonalLink :one
-- A new link for a guest (the old one stops working), by whoever made it.
UPDATE personal_links SET token = @token, created_at = now() WHERE user_id = @user_id AND created_by = @created_by
RETURNING *;

-- name: PersonalLinkByToken :one
-- A personal link, held while a device is added to it: who it's for and who shared it.
SELECT pl.user_id, pl.max_devices, coalesce(nullif(c.display_name, ''), c.username)::text AS shared_by
FROM personal_links pl JOIN users c ON c.id = pl.created_by
WHERE pl.token = @token
FOR UPDATE OF pl;

-- name: PersonalLinkDevices :one
SELECT count(*)::int FROM sessions WHERE user_id = @user_id AND via_link AND expires_at > now();

-- name: CreateLinkSession :one
INSERT INTO sessions (token_hash, user_id, expires_at, via_link, user_agent) VALUES (@token_hash, @user_id, @expires_at, true, sqlc.narg(user_agent))
RETURNING id;

-- name: SignOutLinkDevices :execrows
-- Signs a personal link's devices out (they can open it again), by whoever made the link.
DELETE FROM sessions s USING personal_links pl
WHERE s.user_id = pl.user_id AND s.via_link AND pl.user_id = @user_id AND pl.created_by = @created_by;

-- name: PersonalLinksBy :many
-- The personal links someone made: how many devices have each open, and when one was last used.
SELECT pl.user_id, pl.token, pl.max_devices,
       (count(s.id) FILTER (WHERE s.expires_at > now()))::int AS devices,
       (count(s.id) > 0)::boolean AS opened,
       coalesce(max(s.last_seen_at), pl.created_at)::timestamptz AS last_seen_at
FROM personal_links pl LEFT JOIN sessions s ON s.user_id = pl.user_id AND s.via_link
WHERE pl.created_by = @created_by
GROUP BY pl.user_id;

-- name: PersonalLinkFor :one
SELECT user_id FROM personal_links WHERE token = @token;
