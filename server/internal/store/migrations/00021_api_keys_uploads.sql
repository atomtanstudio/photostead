-- +goose Up
-- API keys let other programs act for a person (Photostead Capture, the ComfyUI sync), kept only
-- as SHA-256 hashes and revocable by name.
CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);
CREATE UNIQUE INDEX api_keys_live_name ON api_keys (user_id, name) WHERE revoked_at IS NULL;

-- Uploads: what came with a file (its description, source link, keywords, album), kept until the
-- file is indexed and it can be applied to the item.
CREATE TABLE uploads (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id    uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    file_id     uuid REFERENCES files ON DELETE CASCADE,
    asset_id    uuid REFERENCES assets ON DELETE SET NULL,
    sha256      bytea NOT NULL,
    file_name   text NOT NULL,
    client      text NOT NULL DEFAULT '', -- the API key's name, or "web"
    description text NOT NULL DEFAULT '',
    source_url  text NOT NULL DEFAULT '',
    keywords    text[] NOT NULL DEFAULT '{}',
    album       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    applied_at  timestamptz
);
CREATE INDEX uploads_waiting ON uploads (file_id) WHERE applied_at IS NULL;

-- +goose Down
DROP TABLE uploads;
DROP TABLE api_keys;
