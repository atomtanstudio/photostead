-- +goose Up
-- A group of faces can be a character rather than a person (in an AI-heavy library, many are:
-- images whose generation metadata is gone). The group keeps growing as faces are grouped, and its
-- items are the character's, with basis 'faces'. It leaves People while linked.
ALTER TABLE people ADD COLUMN character_id uuid REFERENCES characters ON DELETE SET NULL;
CREATE INDEX people_character ON people (character_id) WHERE character_id IS NOT NULL;

ALTER TABLE character_assets DROP CONSTRAINT character_assets_basis_check;
ALTER TABLE character_assets ADD CONSTRAINT character_assets_basis_check
    CHECK (basis IN ('metadata', 'lineage', 'faces', 'confirmed', 'rejected', 'suggested'));

-- +goose Down
DELETE FROM character_assets WHERE basis = 'faces';
ALTER TABLE character_assets DROP CONSTRAINT character_assets_basis_check;
ALTER TABLE character_assets ADD CONSTRAINT character_assets_basis_check
    CHECK (basis IN ('metadata', 'lineage', 'confirmed', 'rejected', 'suggested'));
DROP INDEX people_character;
ALTER TABLE people DROP COLUMN character_id;
