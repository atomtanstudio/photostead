-- +goose Up
-- Reaching Photostead from away, and the devices signed in to it. server_settings holds the
-- server's own choices (its id, which clients use to know it's the same server at another
-- address; its Tailscale node; the addresses it has been reached at). A device paired with a code
-- gets a session of its own, sent as a bearer token rather than a cookie.
CREATE TABLE server_settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO server_settings (key, value) VALUES ('server', jsonb_build_object('id', uuidv7()));

ALTER TABLE sessions ADD COLUMN device text,     -- a paired device's name (null: a browser)
                     ADD COLUMN platform text,
                     ADD COLUMN user_agent text;

ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check
    CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc', 'pair'));

-- +goose Down
DELETE FROM auth_flows WHERE kind = 'pair';
ALTER TABLE auth_flows DROP CONSTRAINT auth_flows_kind_check;
ALTER TABLE auth_flows ADD CONSTRAINT auth_flows_kind_check CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc'));
ALTER TABLE sessions DROP COLUMN user_agent, DROP COLUMN platform, DROP COLUMN device;
DROP TABLE server_settings;
