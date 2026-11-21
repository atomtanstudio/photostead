-- +goose Up
-- Photo edits: a recipe (turning, a crop, light and color) kept beside the original, which is never
-- changed, and applied when the previews are made. orig_*: the picture's size before the edit, to
-- go back to when it's undone.
CREATE TABLE asset_edits (
    asset_id    uuid PRIMARY KEY REFERENCES assets (id) ON DELETE CASCADE,
    recipe      jsonb NOT NULL,
    orig_width  int NOT NULL,
    orig_height int NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  uuid REFERENCES users (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE asset_edits;
