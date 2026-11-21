-- +goose Up
-- The Household: one per server, with its members and one shared space. Everyone keeps their own
-- library; the space holds what's shared (by hand, or by each person's auto-share rules), and
-- members see it beside their own. Only the person who added an item can trash it; anyone can take
-- it out of the space, and then rules never put it back.
CREATE TABLE households (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE household_members (
    household_id uuid NOT NULL REFERENCES households ON DELETE CASCADE,
    user_id      uuid NOT NULL UNIQUE REFERENCES users ON DELETE CASCADE, -- one household each
    role         text NOT NULL CHECK (role IN ('owner', 'adult', 'member')),
    joined_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (household_id, user_id)
);

-- A table rather than a flag, so more shared spaces can come later.
CREATE TABLE spaces (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    household_id uuid NOT NULL REFERENCES households ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('household')),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE space_assets (
    space_id    uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    asset_id    uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    added_by    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    added_at    timestamptz NOT NULL DEFAULT now(),
    via_rule_id uuid, -- put there by this auto-share rule (null: by hand)
    PRIMARY KEY (space_id, asset_id)
);
CREATE INDEX space_assets_asset ON space_assets (asset_id);

-- Taken out of a space: auto-share rules never put it back.
CREATE TABLE space_exclusions (
    space_id uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    PRIMARY KEY (space_id, asset_id)
);

-- A person's auto-share rule: their items from a day on (or all), only with these people (or
-- anyone), AI-made images only when they say so.
CREATE TABLE share_rules (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    space_id   uuid NOT NULL REFERENCES spaces ON DELETE CASCADE,
    from_date  date,
    people     uuid[] NOT NULL DEFAULT '{}',
    include_ai boolean NOT NULL DEFAULT false,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, space_id)
);

ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check
    CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc', 'pair', 'invite'));

-- The server's household, its space, and everyone already here in it (the first admin owns it).
WITH h AS (INSERT INTO households (name) VALUES ('Household') RETURNING id),
     s AS (INSERT INTO spaces (household_id, kind) SELECT id, 'household' FROM h)
INSERT INTO household_members (household_id, user_id, role)
SELECT h.id, u.id, CASE WHEN u.id = (SELECT id FROM users WHERE role = 'admin' ORDER BY created_at LIMIT 1) THEN 'owner' ELSE 'adult' END
FROM h, users u WHERE u.role <> 'guest';

-- Every account but a guest's is in the household, from the moment it's made: the first one owns it.
-- +goose StatementBegin
CREATE FUNCTION join_household() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.role <> 'guest' THEN
        INSERT INTO household_members (household_id, user_id, role)
        SELECT h.id, NEW.id, CASE WHEN EXISTS (SELECT 1 FROM household_members m WHERE m.household_id = h.id) THEN 'adult' ELSE 'owner' END
        FROM households h ORDER BY h.created_at LIMIT 1
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER users_join_household AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION join_household();

-- The gate, now with the household: your own items (as before), and what's in a space you're a
-- member of from others (in their timeline: not archived, hidden, in the vault or the trash), with
-- a shared Live Photo's clip. Each part reads from its own index.
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION visible_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NULL
      AND a.visibility <> 'vault'
$$;
-- +goose StatementEnd
DROP TRIGGER users_join_household ON users;
DROP FUNCTION join_household();
DELETE FROM auth_flows WHERE kind = 'invite';
ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc', 'pair'));
DROP TABLE share_rules;
DROP TABLE space_exclusions;
DROP TABLE space_assets;
DROP TABLE spaces;
DROP TABLE household_members;
DROP TABLE households;
