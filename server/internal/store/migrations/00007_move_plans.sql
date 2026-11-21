-- Move plans (dry runs): where every original will go in the managed `photostead` share.
-- A plan is reviewed by a person before anything is copied. See docs/architecture.md, "Storage layout".

-- +goose Up
CREATE TABLE move_plans (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    summary    jsonb NOT NULL
);

CREATE TABLE move_plan_entries (
    plan_id    uuid NOT NULL REFERENCES move_plans ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('original', 'motion', 'sidecar')),
    asset_id   uuid,
    file_id    uuid NOT NULL,
    library_id uuid NOT NULL,
    src_rel    text NOT NULL,
    dst_rel    text NOT NULL,
    size       bigint NOT NULL,
    renamed    boolean NOT NULL DEFAULT false,
    PRIMARY KEY (plan_id, dst_rel)
);

-- +goose Down
DROP TABLE move_plan_entries;
DROP TABLE move_plans;
