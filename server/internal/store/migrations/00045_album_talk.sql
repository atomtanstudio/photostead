-- +goose Up
-- Talking about a shared album, and choosing from it: comments (on the album, or on an item in it)
-- by anyone it's shared with, guests included; and picks, each person's own choice of items, as a
-- photographer's client would make (proofing). With who added what (album_assets.added_by), they
-- make the album's activity.
CREATE TABLE album_comments (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    album_id   uuid NOT NULL REFERENCES albums ON DELETE CASCADE,
    asset_id   uuid REFERENCES assets ON DELETE CASCADE, -- none: about the album
    author_id  uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    body       text NOT NULL CHECK (length(body) BETWEEN 1 AND 2000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX album_comments_album ON album_comments (album_id, created_at DESC);
CREATE INDEX album_comments_asset ON album_comments (asset_id, created_at) WHERE asset_id IS NOT NULL;

CREATE TABLE album_picks (
    album_id   uuid NOT NULL REFERENCES albums ON DELETE CASCADE,
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (album_id, asset_id, user_id)
);

-- +goose Down
DROP TABLE album_picks;
DROP TABLE album_comments;
