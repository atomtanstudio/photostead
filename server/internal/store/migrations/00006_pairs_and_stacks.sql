-- Things that belong together: a Live Photo's still and its motion clip, and RAW+JPEG pairs
-- (stacks, decision 3.3). See docs/architecture.md, "Files and assets" and "Organizing".

-- +goose Up
-- Each file's path without its extension, lower-cased, for matching IMG_0001.HEIC with IMG_0001.MOV.
ALTER TABLE files ADD COLUMN stem text GENERATED ALWAYS AS (lower(regexp_replace(rel_path, '\.[^./]+$', ''))) STORED;
CREATE INDEX files_stem ON files (library_id, stem) WHERE state = 'present';

-- Apple writes the same ContentIdentifier into both halves of a Live Photo.
ALTER TABLE assets ADD COLUMN content_identifier text;
UPDATE assets a SET content_identifier = coalesce(m.raw->>'Apple:ContentIdentifier', m.raw->>'Keys:ContentIdentifier', m.raw->>'QuickTime:ContentIdentifier')
FROM asset_metadata m WHERE m.asset_id = a.id;
CREATE INDEX assets_content_identifier ON assets (owner_id, content_identifier) WHERE content_identifier IS NOT NULL;

-- still.live_motion_id -> its motion clip; clip.motion_of -> its still. A clip that is part of a
-- Live Photo isn't shown on its own in the timeline.
ALTER TABLE assets ADD COLUMN live_motion_id uuid REFERENCES assets ON DELETE SET NULL;
ALTER TABLE assets ADD COLUMN motion_of uuid REFERENCES assets ON DELETE SET NULL;
CREATE INDEX assets_motion_of ON assets (motion_of) WHERE motion_of IS NOT NULL;

CREATE TABLE stacks (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id       uuid NOT NULL REFERENCES users,
    kind           text NOT NULL CHECK (kind IN ('raw_jpeg', 'burst', 'near_duplicate', 'ai_batch', 'manual')),
    cover_asset_id uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    state          text NOT NULL CHECK (state IN ('suggested', 'confirmed')),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE stack_assets (
    stack_id uuid NOT NULL REFERENCES stacks ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    position integer NOT NULL,
    PRIMARY KEY (stack_id, asset_id)
);
CREATE UNIQUE INDEX stack_assets_one_stack ON stack_assets (asset_id); -- an item is in at most one stack
CREATE INDEX stacks_cover ON stacks (cover_asset_id);

-- +goose Down
DROP TABLE stack_assets;
DROP TABLE stacks;
DROP INDEX assets_motion_of;
ALTER TABLE assets DROP COLUMN motion_of;
ALTER TABLE assets DROP COLUMN live_motion_id;
DROP INDEX assets_content_identifier;
ALTER TABLE assets DROP COLUMN content_identifier;
DROP INDEX files_stem;
ALTER TABLE files DROP COLUMN stem;
