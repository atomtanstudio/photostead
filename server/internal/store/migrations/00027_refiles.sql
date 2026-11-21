-- +goose Up
-- Re-filing: when an item's date changes, its files in the managed share move to the new day's
-- folder (docs/architecture.md, "Storage layout"). Each move is written down before it happens, so
-- one cut short by a restart is finished, not repeated, and every move can be looked up later.
CREATE TABLE refiles (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file_id    uuid NOT NULL REFERENCES files ON DELETE CASCADE,
    from_rel   text NOT NULL,
    to_rel     text NOT NULL,
    sidecar    boolean NOT NULL DEFAULT false, -- <name>.xmp travelled along
    state      text NOT NULL DEFAULT 'planned' CHECK (state IN ('planned', 'done', 'failed')),
    error      text,
    created_at timestamptz NOT NULL DEFAULT now(),
    done_at    timestamptz
);
CREATE INDEX refiles_open ON refiles (file_id) WHERE state = 'planned';

-- +goose Down
DROP TABLE refiles;
