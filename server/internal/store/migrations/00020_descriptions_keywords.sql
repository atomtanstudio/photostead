-- +goose Up
-- Descriptions and keywords (tags) that live outside the files: brought over by an import, sent
-- with an upload (Photostead Capture's post text), or written here. A file's own description stays
-- in asset_metadata; this one, when there is one, is what's shown.
CREATE TABLE asset_descriptions (
    asset_id   uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    text       text NOT NULL,
    source     text NOT NULL CHECK (source IN ('immich', 'upload', 'user')),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX asset_descriptions_words ON asset_descriptions USING gin (to_tsvector('simple', text));

CREATE TABLE keywords (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    name       text NOT NULL, -- as given; nested tags keep their path ("travel/japan")
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, name)
);

CREATE TABLE asset_keywords (
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    keyword_id uuid NOT NULL REFERENCES keywords ON DELETE CASCADE,
    source     text NOT NULL CHECK (source IN ('immich', 'upload', 'user')),
    PRIMARY KEY (asset_id, keyword_id)
);
CREATE INDEX asset_keywords_keyword ON asset_keywords (keyword_id);

-- +goose Down
DROP TABLE asset_keywords;
DROP TABLE keywords;
DROP TABLE asset_descriptions;
