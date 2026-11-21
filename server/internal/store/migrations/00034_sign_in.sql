-- +goose Up
-- Sign-in beyond a password: two-step codes from an authenticator app (TOTP), passkeys (WebAuthn)
-- and single sign-on (OIDC), each a credential. A sign-in under way (a password checked but its
-- code not given yet, a passkey being made or used, a trip to the sign-on provider) waits in
-- auth_flows for a few minutes, found by a token the browser holds (its hash is kept).
CREATE TABLE auth_flows (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash bytea NOT NULL UNIQUE,
    kind       text NOT NULL CHECK (kind IN ('second_step', 'passkey_add', 'passkey_sign_in', 'oidc')),
    user_id    uuid REFERENCES users ON DELETE CASCADE,
    data       jsonb NOT NULL DEFAULT '{}',
    attempts   smallint NOT NULL DEFAULT 0,
    expires_at timestamptz NOT NULL
);
CREATE INDEX auth_flows_expires ON auth_flows (expires_at);

ALTER TABLE credentials ADD COLUMN name text NOT NULL DEFAULT '',
                        ADD COLUMN last_used_at timestamptz;
CREATE UNIQUE INDEX credentials_one_totp ON credentials (user_id) WHERE kind = 'totp';
CREATE UNIQUE INDEX credentials_oidc_subject ON credentials ((data->>'issuer'), (data->>'subject')) WHERE kind = 'oidc';

-- +goose Down
DROP INDEX credentials_oidc_subject;
DROP INDEX credentials_one_totp;
ALTER TABLE credentials DROP COLUMN last_used_at, DROP COLUMN name;
DROP TABLE auth_flows;
