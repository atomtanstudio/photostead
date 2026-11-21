-- +goose Up
-- Favorites, albums, people's names and importing them from Immich (milestone 1).

-- What each person has marked on an item (decision 3.1). Favorites for everyone; ratings, flags
-- and color labels come with organizing (milestone 3).
CREATE TABLE asset_user_state (
    asset_id    uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    user_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    favorite    boolean NOT NULL DEFAULT false,
    rating      smallint CHECK (rating BETWEEN 0 AND 5),
    flag        text CHECK (flag IN ('pick', 'reject')),
    color_label text,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, user_id)
);
CREATE INDEX asset_user_state_favorites ON asset_user_state (user_id, asset_id) WHERE favorite;

CREATE TABLE albums (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id           uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    parent_id          uuid REFERENCES albums ON DELETE CASCADE,
    title              text NOT NULL,
    description        text NOT NULL DEFAULT '',
    cover_asset_id     uuid REFERENCES assets ON DELETE SET NULL,
    kind               text NOT NULL DEFAULT 'manual' CHECK (kind IN ('manual', 'smart')),
    query              jsonb,
    hide_from_timeline boolean NOT NULL DEFAULT false,
    sort               text NOT NULL DEFAULT 'newest' CHECK (sort IN ('newest', 'oldest', 'manual')),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX albums_owner ON albums (owner_id);

CREATE TABLE album_assets (
    album_id  uuid NOT NULL REFERENCES albums ON DELETE CASCADE,
    asset_id  uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    added_by  uuid REFERENCES users ON DELETE SET NULL,
    added_via text, -- 'immich' when an import put it there, so a later import may take it out again
    added_at  timestamptz NOT NULL DEFAULT now(),
    position  integer,
    PRIMARY KEY (album_id, asset_id)
);
CREATE INDEX album_assets_asset ON album_assets (asset_id);

-- People, named once. Faces and their clustering arrive with face recognition (milestone 2);
-- households replace owner_id as the scope when they do.
CREATE TABLE people (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    kind       text NOT NULL DEFAULT 'human' CHECK (kind IN ('human', 'pet')),
    name       text NOT NULL,
    birth_date date,
    hidden     boolean NOT NULL DEFAULT false,
    favorite   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX people_owner ON people (owner_id);

-- Items another app thinks show a person. Only ever suggestions: Photostead's own face
-- recognition decides.
CREATE TABLE person_suggestions (
    person_id  uuid NOT NULL REFERENCES people ON DELETE CASCADE,
    asset_id   uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    source     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (person_id, asset_id, source)
);
CREATE INDEX person_suggestions_asset ON person_suggestions (asset_id);

-- Where another app lives and the key to read it. The key never leaves the server.
CREATE TABLE connections (
    owner_id   uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('immich')),
    url        text NOT NULL,
    secret     text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, kind)
);

-- Each import run, with its step while running and what it found when done.
CREATE TABLE imports (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    source      text NOT NULL CHECK (source IN ('immich', 'takeout', 'apple_photos', 'sidecar')),
    state       text NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'done', 'failed')),
    step        text,
    summary     jsonb,
    error       text,
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX imports_owner ON imports (owner_id, started_at DESC);

-- What each of another app's ids became here: assets, albums and people.
CREATE TABLE import_map (
    owner_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    source      text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('asset', 'album', 'person')),
    source_id   text NOT NULL,
    target_id   uuid NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (owner_id, source, kind, source_id)
);
CREATE INDEX import_map_target ON import_map (target_id);

-- The favorite and archive state the last import brought over for each item. A later import
-- changes only what changed in the other app since, so changes made here are kept.
CREATE TABLE import_asset_state (
    owner_id uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    source   text NOT NULL,
    asset_id uuid NOT NULL REFERENCES assets ON DELETE CASCADE,
    favorite boolean NOT NULL,
    archived boolean NOT NULL,
    PRIMARY KEY (owner_id, source, asset_id)
);

-- +goose Down
DROP TABLE import_asset_state;
DROP TABLE import_map;
DROP TABLE imports;
DROP TABLE connections;
DROP TABLE person_suggestions;
DROP TABLE people;
DROP TABLE album_assets;
DROP TABLE albums;
DROP TABLE asset_user_state;
