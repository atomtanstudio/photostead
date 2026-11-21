-- +goose Up
-- The gate's album part starts from the albums (shared with you, or yours with others' additions)
-- rather than looking through every item that isn't yours: a library of 100,000 items took twice
-- as long to read through the gate the other way.
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
    WHERE a.id IN (SELECT aa.asset_id FROM album_members am JOIN album_assets aa ON aa.album_id = am.album_id WHERE am.user_id = viewer
                   UNION
                   SELECT aa.asset_id FROM albums al JOIN album_assets aa ON aa.album_id = al.id WHERE al.owner_id = viewer AND aa.added_by <> viewer)
      AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility IN ('timeline', 'archived')
      AND NOT EXISTS (SELECT 1 FROM household_members hm JOIN spaces sp ON sp.household_id = hm.household_id
                      JOIN space_assets sa ON sa.space_id = sp.id
                      WHERE hm.user_id = viewer AND sa.asset_id = a.id AND a.visibility = 'timeline')
    UNION ALL
    SELECT m.* FROM assets m JOIN assets a ON a.id = m.motion_of
    WHERE a.id IN (SELECT aa.asset_id FROM album_members am JOIN album_assets aa ON aa.album_id = am.album_id WHERE am.user_id = viewer
                   UNION
                   SELECT aa.asset_id FROM albums al JOIN album_assets aa ON aa.album_id = al.id WHERE al.owner_id = viewer AND aa.added_by <> viewer)
      AND a.owner_id <> viewer AND a.trashed_at IS NULL AND a.visibility IN ('timeline', 'archived')
      AND NOT EXISTS (SELECT 1 FROM household_members hm JOIN spaces sp ON sp.household_id = hm.household_id
                      JOIN space_assets sa ON sa.space_id = sp.id
                      WHERE hm.user_id = viewer AND sa.asset_id = a.id AND a.visibility = 'timeline')
$$;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
