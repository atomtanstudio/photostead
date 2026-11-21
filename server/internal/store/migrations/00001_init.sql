-- Slice 1 schema: accounts, libraries, files, assets and the visibility gate.
-- See docs/architecture.md, "Data model". Later slices add tables; nothing here is ever dropped in place.

-- +goose Up
CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    email        text NOT NULL UNIQUE,
    username     text NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9][a-z0-9_-]{0,31}$'),
    display_name text NOT NULL,
    role         text NOT NULL CHECK (role IN ('admin', 'member', 'guest')),
    settings     jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE credentials (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('password', 'passkey', 'totp', 'oidc')),
    data       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX credentials_one_password ON credentials (user_id) WHERE kind = 'password';

CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash   bytea NOT NULL UNIQUE,
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);

CREATE TABLE libraries (
    id                    uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id              uuid NOT NULL REFERENCES users,
    name                  text NOT NULL,
    kind                  text NOT NULL CHECK (kind IN ('managed', 'indexed')),
    root_path             text NOT NULL UNIQUE,
    read_only             boolean NOT NULL DEFAULT true,
    sidecar_mode          text NOT NULL DEFAULT 'shadow' CHECK (sidecar_mode IN ('beside', 'shadow', 'off')),
    folder_template       text,
    watch                 boolean NOT NULL DEFAULT false,
    created_at            timestamptz NOT NULL DEFAULT now(),
    last_scan_started_at  timestamptz,
    last_scan_finished_at timestamptz,
    UNIQUE (owner_id, name)
);

CREATE TABLE assets (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id         uuid NOT NULL REFERENCES users,
    type             text NOT NULL CHECK (type IN ('image', 'video')),
    sha256           bytea NOT NULL,
    primary_file_id  uuid,
    mime             text,
    taken_at         timestamptz NOT NULL,
    taken_offset_min smallint,
    local_date       date NOT NULL,
    date_source      text NOT NULL,
    width            integer,
    height           integer,
    orientation      smallint,
    duration_ms      integer,
    source_type      text NOT NULL DEFAULT 'unknown' CHECK (source_type IN
                     ('camera', 'screenshot', 'scan', 'download', 'ai_generated', 'ai_edited', 'composite', 'unknown')),
    visibility       text NOT NULL DEFAULT 'timeline' CHECK (visibility IN ('timeline', 'archived', 'hidden', 'vault')),
    trashed_at       timestamptz,
    thumbhash        bytea,
    render_state     text NOT NULL DEFAULT 'pending' CHECK (render_state IN ('pending', 'done', 'failed')),
    render_error     text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, sha256)
);
CREATE INDEX assets_timeline ON assets (owner_id, local_date DESC, taken_at DESC)
    WHERE trashed_at IS NULL AND visibility = 'timeline';

CREATE TABLE files (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    library_id       uuid NOT NULL REFERENCES libraries ON DELETE CASCADE,
    rel_path         text NOT NULL,
    size             bigint NOT NULL,
    mtime            timestamptz NOT NULL,
    sha256           bytea,
    alt_hashes       jsonb NOT NULL DEFAULT '{}',
    mime             text,
    kind             text NOT NULL CHECK (kind IN ('image', 'video', 'sidecar', 'other')),
    role             text NOT NULL DEFAULT 'original' CHECK (role IN ('original', 'motion', 'sidecar', 'companion')),
    state            text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'present', 'missing', 'skipped', 'failed')),
    reason           text,
    asset_id         uuid REFERENCES assets ON DELETE SET NULL,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at     timestamptz NOT NULL DEFAULT now(),
    last_verified_at timestamptz,
    UNIQUE (library_id, rel_path)
);
CREATE INDEX files_asset ON files (asset_id);
CREATE INDEX files_state ON files (library_id, state);

ALTER TABLE assets ADD CONSTRAINT assets_primary_file
    FOREIGN KEY (primary_file_id) REFERENCES files ON DELETE SET NULL;

CREATE TABLE asset_metadata (
    asset_id    uuid PRIMARY KEY REFERENCES assets ON DELETE CASCADE,
    make        text,
    model       text,
    lens        text,
    focal_mm    real,
    fnumber     real,
    exposure_s  real,
    iso         integer,
    lat         double precision,
    lon         double precision,
    altitude    double precision,
    title       text,
    description text,
    raw         jsonb NOT NULL
);

-- The visibility gate. Every read of assets outside the store package goes through this function,
-- so archived, hidden, vault and trashed items are filtered in exactly one place.
-- (Household spaces join in here when they arrive.) LANGUAGE sql + STABLE lets Postgres inline it,
-- so callers' WHERE clauses still use the indexes on assets.
-- +goose StatementBegin
CREATE FUNCTION visible_assets(viewer uuid) RETURNS SETOF assets
LANGUAGE sql STABLE AS $$
    SELECT a.* FROM assets a
    WHERE a.owner_id = viewer
      AND a.trashed_at IS NULL
      AND a.visibility <> 'vault'
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION visible_assets(uuid);
DROP TABLE asset_metadata;
ALTER TABLE assets DROP CONSTRAINT assets_primary_file;
DROP TABLE files;
DROP TABLE assets;
DROP TABLE libraries;
DROP TABLE sessions;
DROP TABLE credentials;
DROP TABLE users;
