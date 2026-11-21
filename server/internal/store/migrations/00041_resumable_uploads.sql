-- +goose Up
-- Resumable uploads (IETF draft-ietf-httpbis-resumable-upload-14): an upload that can be sent in
-- pieces and carried on after a dropped connection. The bytes so far are a partial file next to
-- the managed share's uploads (its size is the offset); this is who it's for and what came with
-- it. Once complete it's filed like any upload, and its reply is kept to answer a retry.
CREATE TABLE upload_sessions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    token        text NOT NULL UNIQUE,            -- in the upload's address: hard to guess, a credential
    owner_id     uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    client       text NOT NULL,
    meta         jsonb NOT NULL DEFAULT '{}',     -- file name, date, description… (UploadMeta)
    length       bigint,                          -- the whole file's size, once known
    path         text NOT NULL,                   -- the partial file
    completed_at timestamptz,
    reply_status integer,
    reply        jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX upload_sessions_expires ON upload_sessions (expires_at);

-- +goose Down
DROP TABLE upload_sessions;
