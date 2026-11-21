-- +goose Up
-- Characters (decision 5.6): AI characters, kept apart from People and private to their creator,
-- formed from generation metadata, never from face similarity alone.
CREATE TABLE characters (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id       uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    name           text NOT NULL,
    -- Patterns for the character's LoRA names (case-insensitive regular expressions, matched
    -- against the catalog's normalized names). Prompt tokens and reference images can join later.
    loras          text[] NOT NULL DEFAULT '{}',
    cover_asset_id uuid REFERENCES assets ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

-- Who's in what, and why: metadata (a LoRA of theirs), lineage (a video made from an image of
-- them), confirmed or rejected by the owner (which the matching never overrides), or suggested
-- by face similarity (later; never a member until confirmed).
CREATE TABLE character_assets (
    character_id uuid NOT NULL REFERENCES characters ON DELETE CASCADE,
    asset_id     uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    basis        text NOT NULL CHECK (basis IN ('metadata', 'lineage', 'confirmed', 'rejected', 'suggested')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (character_id, asset_id)
);
CREATE INDEX character_assets_asset ON character_assets (asset_id);

-- +goose Down
DROP TABLE character_assets;
DROP TABLE characters;
