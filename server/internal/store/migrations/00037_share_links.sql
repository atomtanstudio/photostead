-- +goose Up
-- Share links: an album anyone with the link can see (from the internet through Tailscale Funnel,
-- or wherever Photostead is public), optionally behind a password, until a day, with downloads
-- (location removed) and uploads into the album when the owner allows them. The token is kept as
-- is, so its owner can copy the link again; it's a capability, not a password.
CREATE TABLE share_links (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    token          text NOT NULL UNIQUE,
    owner_id       uuid NOT NULL REFERENCES users ON DELETE CASCADE,
    album_id       uuid NOT NULL REFERENCES albums ON DELETE CASCADE,
    password_hash  text,
    expires_at     timestamptz,
    allow_download boolean NOT NULL DEFAULT false,
    allow_upload   boolean NOT NULL DEFAULT false,
    views          integer NOT NULL DEFAULT 0,
    last_viewed_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX share_links_album ON share_links (album_id);

ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check
    CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc', 'pair', 'invite', 'link_access'));

-- +goose Down
DELETE FROM auth_flows WHERE kind = 'link_access';
ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check
    CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc', 'pair', 'invite'));
DROP TABLE share_links;
