-- +goose Up
-- Albums shared with people: household members, or guests (accounts with no library of their own,
-- invited to an album). A viewer sees the album; a contributor also adds to it. What's shared this
-- way shows in the album, not in their timeline: the gate splits in two.
CREATE TABLE album_members (
    album_id uuid NOT NULL REFERENCES albums ON DELETE CASCADE,
    user_id  uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    role     text NOT NULL CHECK (role IN ('viewer', 'contributor')),
    added_by uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (album_id, user_id)
);
CREATE INDEX album_members_user ON album_members (user_id);

-- A library: your own items, and the Household's (the timeline, its counts, memories).
-- +goose StatementBegin
CREATE FUNCTION library_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NULL
      AND a.visibility <> 'vault'
    UNION ALL
    SELECT a.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
    UNION ALL
    SELECT m.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    JOIN assets m ON m.motion_of = a.id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
$$;
-- +goose StatementEnd

-- The gate: your library, and others' items in albums shared with you or in your own albums (a
-- contributor's), each once (not again when the Household shows them already), with their Live
-- Photo clips.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION visible_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NULL
      AND a.visibility <> 'vault'
    UNION ALL
    SELECT a.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
    UNION ALL
    SELECT m.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    JOIN assets m ON m.motion_of = a.id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
    UNION ALL
    SELECT a.* FROM assets a
    WHERE a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility IN ('timeline', 'archived')
      AND coalesce(a.motion_of, a.id) IN (
          SELECT aa.asset_id FROM album_members am JOIN album_assets aa ON aa.album_id = am.album_id WHERE am.user_id = viewer
          UNION
          SELECT aa.asset_id FROM albums al JOIN album_assets aa ON aa.album_id = al.id WHERE al.owner_id = viewer)
      AND NOT EXISTS (SELECT 1 FROM household_members hm JOIN spaces sp ON sp.household_id = hm.household_id
                      JOIN space_assets sa ON sa.space_id = sp.id JOIN assets s ON s.id = sa.asset_id
                      WHERE hm.user_id = viewer AND sa.asset_id = coalesce(a.motion_of, a.id) AND s.visibility = 'timeline')
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION visible_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NULL
      AND a.visibility <> 'vault'
    UNION ALL
    SELECT a.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
    UNION ALL
    SELECT m.* FROM household_members hm
    JOIN spaces sp ON sp.household_id = hm.household_id
    JOIN space_assets sa ON sa.space_id = sp.id
    JOIN assets a ON a.id = sa.asset_id
    JOIN assets m ON m.motion_of = a.id
    WHERE hm.user_id = viewer AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility = 'timeline'
$$;
-- +goose StatementEnd
DROP FUNCTION library_assets(uuid);
DROP TABLE album_members;
