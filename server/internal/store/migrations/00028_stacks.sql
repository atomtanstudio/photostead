-- +goose Up
-- More stacks (docs/architecture.md, "Organizing"): AI batches (one prompt and model, made minutes
-- apart) and camera bursts are stacked on their own; near-duplicates are suggested for the owner to
-- confirm. What the owner takes out of a stack, or keeps apart, stays out (stack_exclusions).
CREATE TABLE stack_exclusions (
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('raw_jpeg', 'burst', 'near_duplicate', 'ai_batch', 'manual')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, kind)
);

-- Items already compared for near-duplicates (so each pass looks only at new ones).
CREATE TABLE near_duplicate_checks (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    checked_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stacks_owner_kind ON stacks (owner_id, kind, state);
ALTER TABLE stacks DROP CONSTRAINT stacks_owner_id_fkey;
ALTER TABLE stacks ADD CONSTRAINT stacks_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users ON DELETE CASCADE;

-- +goose Down
ALTER TABLE stacks DROP CONSTRAINT stacks_owner_id_fkey;
ALTER TABLE stacks ADD CONSTRAINT stacks_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users;
DROP INDEX stacks_owner_kind;
DROP TABLE near_duplicate_checks;
DROP TABLE stack_exclusions;
